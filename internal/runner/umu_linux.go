//go:build linux

package runner

// The umu/GE-Proton runtime — downloading it, warming and laying out Wine
// prefixes, building launch command lines, hosting plugins — lives in
// asa-server/pkg/umuruntime (on top of pkg/umu and pkg/wineprefix). This file
// is the composition root: it maps the live runner.Config onto the process's
// single *umuruntime.Host, supplies the drop-privileges identity and the
// hooks the host calls back into, and turns the host's typed errors into
// this program's own wording. See docs/UMU_RUNTIME_PLUGIN_PLAN.md.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"

	"asa-server/pkg/logger"
	"asa-server/pkg/umu"
	"asa-server/pkg/umuruntime"
	"asa-server/pkg/vcredist"
	"asa-server/pkg/wineprefix"
)

// runtimeHost is this process's single runtime host. "Only one per process"
// holds because it is constructed exactly once, here; configuration changes
// go through hostFor's Reconfigure, never a second New.
var runtimeHost = umuruntime.MustNew(umuruntime.Config{})

// runtimeIdentity is the drop-privileges account every Wine-side mechanism
// runs as. Each callback reads the config fresh: the account may not exist
// yet when the host is configured.
var runtimeIdentity = umuruntime.Identity{
	HomeDir:  func() string { return runtimeHomeDir(getConfig()) },
	ChildIDs: func() (uint32, uint32, bool) { return runtimeChildIDs(getConfig()) },
	Credential: func() (*syscall.Credential, string, error) {
		return resolveRuntimeCredential(getConfig())
	},
	ChownPath: chownPathForRuntime,
	UserName:  func() string { return runtimeUserName(getConfig()) },
}

// hostFor refreshes runtimeHost's config from cfg and returns it. Cheap (a
// few atomic pointer stores) — called before every use rather than only from
// Configure(), so it needs no special hook there.
func hostFor(cfg Config) *umuruntime.Host {
	runtimeHost.Reconfigure(hostConfig(cfg))
	return runtimeHost
}

func hostConfig(cfg Config) umuruntime.Config {
	return umuruntime.Config{
		Runtime:          cfg.Runtime,
		WineDLLOverrides: cfg.WineDLLOverrides,
		Umu: umu.Config{
			BaseDir:         cfg.BaseDir,
			UmuVersion:      cfg.UmuVersion,
			ProtonVersion:   cfg.ProtonVersion,
			GameID:          cfg.GameID,
			AutoDownload:    cfg.AutoDownload,
			SteamRTPrefetch: cfg.SteamRTPrefetch,
			PythonBin:       cfg.PythonBin,
		},
		Prefix: wineprefix.Config{
			BaseDir:         cfg.BaseDir,
			PrefixDir:       cfg.PrefixDir,
			PrefixMode:      cfg.PrefixMode,
			ProtonVersion:   cfg.ProtonVersion,
			Runtime:         cfg.Runtime,
			InstallVCRedist: cfg.InstallVCRedist,
			EnsureVCRedist: func(ctx context.Context, prefixKey string, logf func(string, ...any)) error {
				return ensureVCRedist(ctx, getConfig(), prefixKey, logf)
			},
			HasVCRedistOverrides: vcredist.OverridesApplied,
		},
		Identity: runtimeIdentity,
		BeforeEnsure: func(ctx context.Context) error {
			if err := ensureRuntimeUser(ctx); err != nil {
				return fmt.Errorf("failed to prepare the non-root runtime user: %w", err)
			}
			return nil
		},
		// ArkApi（AsaApiLoader.exe）依赖微软 VC++ 运行时，Wine/GE-Proton 的 prefix 里
		// 只有 Wine 自己的同名实现。放在预热之后是因为 prefix 必须先初始化好才能往里装东西。
		//
		// 失败不阻断 EnsureRuntime：这一步服务的是一个**可选功能**，不开 ArkApi 的用户
		// 占绝大多数，为它让整个环境准备失败不成比例。但与 steamrt 预取那种「无声降级」
		// 不同，这里的失败必须响亮 —— 真要用 ArkApi 的人必须看见这条。
		// 见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §3.2。
		//
		// cfg 是本次 hostFor 拿到的那一份，与 Ensure 全程所用的同源。
		AfterWarm: func(ctx context.Context, logf func(string, ...any)) {
			if err := ensureVCRedist(ctx, cfg, "", logf); err != nil {
				logf("VC++ 运行时安装失败（%v）；不使用 ArkApi 可忽略，使用 ArkApi 请看上面的输出", err)
			}
		},
	}
}

// setupHintError appends this program's fix — `asa-server setup` — to a
// runtime-not-ready error. pkg/umu and pkg/wineprefix only say what is
// missing (umuruntime.NotReadyError); how to get it is ours to say.
type setupHintError struct{ err error }

func (e *setupHintError) Error() string {
	return e.err.Error() + "。请运行 asa-server setup 完成环境准备"
}
func (e *setupHintError) Unwrap() error { return e.err }

// withSetupHint wraps err in setupHintError when it is (or wraps) a
// NotReadyError. Applied at every exported entry point such an error can
// leave this package through.
func withSetupHint(err error) error {
	var nr *umuruntime.NotReadyError
	if err != nil && errors.As(err, &nr) {
		return &setupHintError{err: err}
	}
	return err
}

// Wine-prefix lifecycle. These six look like thin forwards, but they are the
// **platform seam**, not leftovers: runner.go carries no build tag, so every
// unexported name it calls needs a Windows definition too — that is what
// prefix_windows.go's six no-ops are for. See runner.go's exported
// PrefixKeyFor/EnsurePrefix/RemoveInstancePrefix/PrefixStatus/
// PrepareSharedPrefixWrite/ReconcilePrefixes for the documented contract.
func prefixKeyFor(instanceName string) string {
	return hostFor(getConfig()).Prefixes().KeyFor(instanceName)
}

func ensurePrefix(ctx context.Context, prefixKey string, progress io.Writer) error {
	return withSetupHint(hostFor(getConfig()).Prefixes().EnsurePrefix(ctx, prefixKey, progress))
}

func removeInstancePrefix(instanceName string) error {
	return hostFor(getConfig()).Prefixes().Remove(instanceName)
}

func prefixStatus() []PrefixInfo { return hostFor(getConfig()).Prefixes().Status() }

func prepareSharedPrefixWrite(op string) (func(), error) {
	return hostFor(getConfig()).Prefixes().PrepareSharedWrite(op)
}

func reconcilePrefixes() { hostFor(getConfig()).Prefixes().Reconcile() }

// ensureRuntime downloads umu-run + the pinned GE-Proton build if missing,
// and warms the default shared Wine prefix — see umuruntime.Host.Ensure.
func ensureRuntime(ctx context.Context, progress io.Writer) error {
	err := hostFor(getConfig()).Ensure(ctx, progressLogger(progress))
	if errors.Is(err, umuruntime.ErrAutoDownloadDisabled) {
		return fmt.Errorf("runner: auto_download is disabled and runtime is not fully installed (see GET /api/system/preflight)")
	}
	return withSetupHint(err)
}

func progressLogger(w io.Writer) func(format string, args ...any) {
	return func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		logger.Info(msg)
		if w != nil {
			fmt.Fprintln(w, msg)
		}
	}
}
