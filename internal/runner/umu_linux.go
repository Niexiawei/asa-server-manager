//go:build linux

package runner

// The umu/GE-Proton runtime mechanism (download, prefix warming, wineserver
// detection, Python interpreter resolution) lives in asa-server/pkg/umu; the
// Wine prefix directory layout and lifecycle (shared/per-instance/overlay,
// status, removal) in asa-server/pkg/wineprefix. This file is the
// composition root: it builds each package's Config from the live
// runner.Config, holds the process's single *umu.Runtime and
// *wineprefix.Manager, and orchestrates EnsureRuntime — the one sequence
// that has to coordinate both packages plus the runtime-user (sysuser) and
// VC++ runtime (vcredist) concerns neither package knows about.

import (
	"context"
	"fmt"
	"io"
	"sync"
	"syscall"

	"asa-server/pkg/logger"
	"asa-server/pkg/umu"
	"asa-server/pkg/vcredist"
	"asa-server/pkg/wineprefix"
)

// umuRuntime / wineprefixMgr are this process's single instances — the
// "only one of these per process" invariant is a property of holding
// exactly one of each, constructed once here (mirrors xvfbMgr in
// xvfb_linux.go).
var (
	umuRuntime    = umu.New(umu.Config{})
	wineprefixMgr = wineprefix.New(wineprefix.Config{}, umuRuntime)
)

// umuRuntimeFor refreshes umuRuntime's config from cfg and returns it. Cheap
// (an atomic pointer store) — called before every use rather than only from
// Configure(), so it needs no special hook there.
func umuRuntimeFor(cfg Config) *umu.Runtime {
	umuRuntime.Reconfigure(umu.Config{
		BaseDir:         cfg.BaseDir,
		UmuVersion:      cfg.UmuVersion,
		ProtonVersion:   cfg.ProtonVersion,
		GameID:          cfg.GameID,
		AutoDownload:    cfg.AutoDownload,
		SteamRTPrefetch: cfg.SteamRTPrefetch,
		PythonBin:       cfg.PythonBin,
		HomeDir:         func() string { return runtimeHomeDir(getConfig()) },
		ChildIDs:        func() (uint32, uint32, bool) { return runtimeChildIDs(getConfig()) },
		Credential: func() (*syscall.Credential, error) {
			cred, _, err := resolveRuntimeCredential(getConfig())
			return cred, err
		},
		ChownPath: chownPathForRuntime,
		UserName:  func() string { return runtimeUserName(getConfig()) },
	})
	return umuRuntime
}

// wineprefixMgrFor refreshes wineprefixMgr's config from cfg and returns it.
func wineprefixMgrFor(cfg Config) *wineprefix.Manager {
	wineprefixMgr.Reconfigure(wineprefix.Config{
		BaseDir:         cfg.BaseDir,
		PrefixDir:       cfg.PrefixDir,
		PrefixMode:      cfg.PrefixMode,
		ProtonVersion:   cfg.ProtonVersion,
		Runtime:         cfg.Runtime,
		InstallVCRedist: cfg.InstallVCRedist,
		ChownPath:       chownPathForRuntime,
		EnsureVCRedist: func(ctx context.Context, prefixKey string, logf func(string, ...any)) error {
			return ensureVCRedist(ctx, getConfig(), prefixKey, logf)
		},
		HasVCRedistOverrides: vcredist.OverridesApplied,
	})
	return wineprefixMgr
}

// Directory-layout / launch-mechanism thin wrappers — unchanged signatures
// so every other file in this package (runner_linux.go, vcredist_linux.go,
// runtimeuser_linux.go, sharedaccess_linux.go, preflight_linux.go) needs no
// further changes beyond what the sysuser/vcredist/xvfb/display phases
// already made.
func umuDir(cfg Config) string        { return umuRuntimeFor(cfg).Dir() }
func umuRunPath(cfg Config) string    { return umuRuntimeFor(cfg).RunPath() }
func protonBaseDir(cfg Config) string { return umuRuntimeFor(cfg).ProtonBaseDir() }
func protonPath(cfg Config) string    { return umuRuntimeFor(cfg).ProtonPath() }
func prefixDir(cfg Config, key string) string { return wineprefixMgrFor(cfg).Dir(key) }

func waitForWineserverDrain(prefix string) { umu.WaitForWineserverDrain(prefix) }

// Wine-prefix lifecycle thin wrappers — see runner.go's exported
// PrefixKeyFor/EnsurePrefix/RemoveInstancePrefix/PrefixStatus/
// PrepareSharedPrefixWrite/ReconcilePrefixes for the documented contract.
func prefixKeyFor(instanceName string) string { return wineprefixMgrFor(getConfig()).KeyFor(instanceName) }

func ensurePrefix(ctx context.Context, prefixKey string, progress io.Writer) error {
	return wineprefixMgrFor(getConfig()).EnsurePrefix(ctx, prefixKey, progress)
}

func removeInstancePrefix(instanceName string) error {
	return wineprefixMgrFor(getConfig()).Remove(instanceName)
}

func prefixStatus() []PrefixInfo { return wineprefixMgrFor(getConfig()).Status() }

func prepareSharedPrefixWrite(op string) (func(), error) {
	return wineprefixMgrFor(getConfig()).PrepareSharedWrite(op)
}

func reconcilePrefixes() { wineprefixMgrFor(getConfig()).Reconcile() }

// runtimeMu serializes EnsureRuntime: concurrent first-time initialization
// (two instances starting at once on a fresh install) would otherwise race
// on the same umu-run/GE-Proton download and prefix warm-up.
var runtimeMu sync.Mutex

// ensureRuntime downloads umu-run + the pinned GE-Proton build if missing,
// and warms the default shared Wine prefix. Mirrors
// scripts/ark_instance_manager.sh's install_base_server() umu/Proton
// section — that script is the verified reference this logic is copied
// from, not re-derived.
func ensureRuntime(ctx context.Context, progress io.Writer) error {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()

	cfg := getConfig()
	logf := progressLogger(progress)

	// Create + reconcile the dedicated non-root user first: WarmPrefix below
	// runs wineboot as that user, and it must be able to write the prefix and
	// its own HOME. See docs/UMU_RUNTIME_USER_PLAN.md §3.2.
	if err := ensureRuntimeUser(ctx); err != nil {
		return fmt.Errorf("failed to prepare the non-root runtime user: %w", err)
	}

	umuRT := umuRuntimeFor(cfg)
	wpMgr := wineprefixMgrFor(cfg)

	if cfg.Runtime == "custom" {
		logf("linux runtime mode is \"custom\": skipping umu/GE-Proton download, verifying the pre-configured runtime instead")
		if err := umuRT.CheckRuntime(); err != nil {
			return err
		}
		return wpMgr.CheckSharedReady()
	}
	if !cfg.AutoDownload {
		return fmt.Errorf("runner: auto_download is disabled and runtime is not fully installed (see GET /api/system/preflight)")
	}

	if err := umuRT.EnsureUmu(ctx, logf); err != nil {
		return fmt.Errorf("failed to install umu-launcher: %w", err)
	}
	if err := umuRT.EnsureGEProton(ctx, logf); err != nil {
		return fmt.Errorf("failed to install %s: %w", cfg.ProtonVersion, err)
	}

	// WarmPrefix below runs `umu-run wineboot --init`, which would let umu go
	// fetch the Steam Linux Runtime itself via its own unthrottled client.
	// Prefetch it first via pkg/download so wineboot only has to resume the
	// last sliver. Failure only degrades — the reference behaviour (umu
	// downloads it) — and must never block setup. See
	// docs/STEAMRT_PREFETCH_PLAN.md.
	prefetched, err := umuRT.PrefetchSteamRuntime(ctx, logf)
	if err != nil {
		logf("Steam Linux Runtime 预下载失败（%v），改由 umu 自行下载", err)
	}

	// 从这里往下才开始动共享前缀本身。overlay 模式下它可能正被若干实例的可写层
	// 当 lowerdir 引用着 —— 这时改它是未定义行为。
	//
	// 判据是「还有没有事要做」而不是「有没有挂载」：本函数在每次 API 启动时都会
	// 后台跑一遍，而挂载是**故意**跨重启存活的（停实例不卸载），一见挂载就报错
	// 等于第一个实例起过之后永远起不来。见 wineprefix.Manager.LowerNeedsWork。
	doneWrite, err := wpMgr.PrepareSharedWrite("环境准备 EnsureRuntime")
	if err != nil {
		if wpMgr.LowerNeedsWork() {
			return err
		}
		logf("共享 Wine 前缀已是最新，跳过重建（当前有实例的可写层挂在它上面）")
		return nil
	}
	defer doneWrite()

	if err := umuRT.WarmPrefix(ctx, wpMgr.Dir(""), logf, prefetched.Variant != ""); err != nil {
		return fmt.Errorf("failed to prepare Wine prefix: %w", err)
	}

	// ArkApi（AsaApiLoader.exe）依赖微软 VC++ 运行时，Wine/GE-Proton 的 prefix 里
	// 只有 Wine 自己的同名实现。放在这里是因为 prefix 必须先初始化好才能往里装东西。
	//
	// 失败不阻断 EnsureRuntime：这一步服务的是一个**可选功能**，不开 ArkApi 的用户
	// 占绝大多数，为它让整个环境准备失败不成比例。但与 steamrt 预取那种「无声降级」
	// 不同，这里的失败必须响亮 —— 真要用 ArkApi 的人必须看见这条。
	// 见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §3.2。
	if err := ensureVCRedist(ctx, cfg, "", logf); err != nil {
		logf("VC++ 运行时安装失败（%v）；不使用 ArkApi 可忽略，使用 ArkApi 请看上面的输出", err)
	}
	return nil
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
