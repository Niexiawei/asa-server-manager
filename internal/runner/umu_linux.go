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
	"path/filepath"
	"syscall"

	"asa-server/pkg/logger"
	"asa-server/pkg/umu"
	"asa-server/pkg/umuruntime"
	"asa-server/pkg/umuruntime/plugins/xdisplay"
	"asa-server/pkg/wineprefix"
	"asa-server/pkg/xvfb"
)

// runtimeHost is this process's single runtime host. "Only one per process"
// holds because it is constructed exactly once, here; configuration changes
// go through hostFor's Reconfigure, never a second New.
//
// Plugins registered here are the only ones the process has. Both are
// Optional: only ArkApi needs either (see checkDisplay and describeOutcome),
// so neither may fail environment setup. Registration order is also the
// order providers of one capability are tried in.
var runtimeHost = umuruntime.MustNew(umuruntime.Config{},
	umuruntime.With(displayRes, umuruntime.Optional),
	umuruntime.With(vcrtPlugin, umuruntime.Optional),
)

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

// displayRes is this process's single display resolver. It owns the one
// *xvfb.Manager — "only one self-managed display per process" holds because
// this is the only xdisplay.New call outside tests. See
// docs/UMU_RUNTIME_PLUGIN_PLAN.md §4.4.
//
// 为什么本项目在 Linux 上把「图形显示」当依赖（尽管跑的是无头服务端），见
// xdisplay 的包注释与 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §9：ArkAscendedServer.exe
// 本身**不需要**显示，只有 AsaApiLoader.exe（ArkApi）与 vc_redist.x64.exe 这两条路径要。
var displayRes = xdisplay.New(xdisplay.Config{})

// displayFor refreshes displayRes (and its Xvfb manager) from cfg and returns it.
func displayFor(cfg Config) *xdisplay.Resolver {
	displayRes.Reconfigure(xdisplay.Config{
		Display: cfg.Display,
		Xvfb: xvfb.Config{
			Bin:             cfg.XvfbBin,
			Screen:          cfg.XvfbScreen,
			StatePath:       xvfbStatePath(cfg),
			AllowX11Remount: cfg.AllowX11Remount,
			HomeDir:         runtimeIdentity.HomeDir,
			ChildIDs:        runtimeIdentity.ChildIDs,
			Credential: func() (*syscall.Credential, error) {
				cred, _, err := runtimeIdentity.Credential()
				return cred, err
			},
			// 自管 Xvfb 会改宿主状态（remount /tmp/.X11-unix、chmod 1777），也会意外退出——
			// 这些都必须留在日志里。
			Infof: logger.Infof,
			Warnf: logger.Warnf,
		},
	})
	return displayRes
}

func xvfbStatePath(cfg Config) string {
	if cfg.BaseDir == "" {
		return ""
	}
	return filepath.Join(cfg.BaseDir, "xvfb.state")
}

// --- runner 导出 API 的 Linux 实现（runner.go 无 build tag，Windows 版在 plugins_windows.go）---

func checkNeeds(prefixKey string, caps []Capability) []Unmet {
	return hostFor(getConfig()).CheckNeeds(prefixKey, caps)
}

func provision(ctx context.Context, prefixKey string, progress io.Writer, caps []Capability) error {
	err := hostFor(getConfig()).Provision(ctx, prefixKey, progressLogger(progress), caps...)
	return withSetupHint(describeVCRedistError(err))
}

func pluginStatuses() []PluginStatus { return hostFor(getConfig()).Status() }

func capabilityStatus(c Capability, prefixKey, exeDir string) []PluginStatus {
	return hostFor(getConfig()).CapabilityStatus(c, prefixKey, exeDir)
}

// closeRuntime 关闭所有插件持有的进程级资源（今天只有显示插件的自管 Xvfb）。
func closeRuntime() { hostFor(getConfig()).Close() }

// hostFor refreshes runtimeHost's config from cfg and returns it. Cheap (a
// few atomic pointer stores) — called before every use rather than only from
// Configure(), so it needs no special hook there.
//
// Plugins are configured here too: the host never configures the plugins it
// was handed, so this is the one place that keeps them in step with cfg.
func hostFor(cfg Config) *umuruntime.Host {
	displayFor(cfg)
	vcrtFor(cfg)
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
			BaseDir:       cfg.BaseDir,
			PrefixDir:     cfg.PrefixDir,
			PrefixMode:    cfg.PrefixMode,
			ProtonVersion: cfg.ProtonVersion,
		},
		Identity: runtimeIdentity,
		BeforeEnsure: func(ctx context.Context) error {
			if err := ensureRuntimeUser(ctx); err != nil {
				return fmt.Errorf("failed to prepare the non-root runtime user: %w", err)
			}
			return nil
		},
		OnOutcome: describeOutcome,
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

func removePrefixLayer(key string) error { return hostFor(getConfig()).Prefixes().RemoveLayer(key) }

func removePrefixDir(key string) error { return hostFor(getConfig()).Prefixes().RemovePrefix(key) }

func removePrefixBackup(path string) error { return hostFor(getConfig()).Prefixes().RemoveBackup(path) }

func prepareSharedPrefixWrite(op string) (func(), error) {
	return hostFor(getConfig()).Prefixes().PrepareSharedWrite(op)
}

func lockRuntime(ctx context.Context, progress io.Writer) (context.Context, func(), error) {
	return hostFor(getConfig()).Lock(ctx, progressLogger(progress))
}

func holdPrefix(prefixKey string) func() {
	return hostFor(getConfig()).Prefixes().HoldLayer(prefixKey)
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
