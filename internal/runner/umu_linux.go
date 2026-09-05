//go:build linux

package runner

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"asa-server/pkg/archive"
	"asa-server/pkg/download"
	"asa-server/pkg/logger"
	"asa-server/pkg/procx"
	"asa-server/pkg/steamrt"
)

// Directory layout, all rooted at Config.BaseDir — see
// docs/LINUX_COMPATIBILITY_PLAN.md §4.1.
func umuDir(cfg Config) string        { return filepath.Join(cfg.BaseDir, "umu-launcher") }
func umuRunPath(cfg Config) string    { return filepath.Join(umuDir(cfg), "umu-run") }
func protonBaseDir(cfg Config) string { return filepath.Join(cfg.BaseDir, "proton") }
func protonPath(cfg Config) string {
	return filepath.Join(protonBaseDir(cfg), cfg.ProtonVersion)
}

// umuCacheDir 是 umu 的下载中转目录 UMU_CACHE。
//
// umu_consts.py: UMU_CACHE = XDG_CACHE_HOME/umu，XDG_CACHE_HOME 缺省为 ~/.cache。
// 在我们的进程树里 XDG_* 恒为空 —— inheritedEnv 的白名单根本没有 XDG_*，runtimeEnv
// 还会再剥一道 —— 所以它恒等于 {runtimeHomeDir}/.cache/umu。
func umuCacheDir(cfg Config) string {
	return filepath.Join(runtimeHomeDir(cfg), ".cache", "umu")
}

// prefetchSteamRuntime 预取 Steam Linux Runtime 归档：本函数只解决"这次该不该
// 预取、预取到哪、预取完交给谁"这几件 ASA 特有的事，机制本身（下载、校验、截尾、
// 变体判定）在 asa-server/pkg/steamrt。
//
// 返回命中的变体（零值表示"本次不需要预取"，不是错误）。调用方必须把错误当成
// 「降级」而不是「失败」：这个优化的全部价值是省时间，为省时间制造一个新的安装
// 失败点是净亏。
func prefetchSteamRuntime(ctx context.Context, cfg Config, logf func(string, ...any)) (steamrt.Variant, error) {
	if cfg.Runtime != "umu" || !cfg.AutoDownload || !cfg.SteamRTPrefetch {
		return steamrt.Variant{}, nil
	}
	if _, ok := steamrt.ForProton(protonPath(cfg), cfg.ProtonVersion); !ok {
		logf("跳过 Steam Linux Runtime 预下载：认不出 %s 需要哪个运行时变体，交给 umu 自行判断", cfg.ProtonVersion)
		return steamrt.Variant{}, nil
	}
	if steamLinuxRuntimeReady(cfg) {
		return steamrt.Variant{}, nil
	}
	if runtimeHomeDir(cfg) == "" {
		return steamrt.Variant{}, fmt.Errorf("无法确定 umu 缓存所在的家目录")
	}
	return steamrt.Prefetch(ctx, protonPath(cfg), cfg.ProtonVersion, umuCacheDir(cfg), chownPathForRuntime, logf)
}

// protonNoXalia turns off Xalia, the accessibility / gamepad UI overlay
// GE-Proton starts alongside every launch. Its script defaults
// PROTON_USE_XALIA to "1" but honours a value supplied from outside
// (`if "PROTON_USE_XALIA" not in self.env`), which is how this works —
// GE-Proton10-34's proton, L2093.
//
// A dedicated server has no screen and nobody sitting at one, so the overlay
// has nothing to do here in *any* launch. And without a display it doesn't
// merely idle, it **fails**: SDL finds no video driver and Xalia dies with
//
//	System.PlatformNotSupportedException: Video driver  not supported
//	  at Xalia.Sdl.WindowingSystem.Create () ...
//
// (note the doubled space — the driver name is empty, i.e. no DISPLAY at all).
// That crash is harmless to the program actually being launched, and that is
// exactly why it earns removal: a plain instance start — the common case,
// NeedsDisplay is false for ArkAscendedServer.exe, so it never gets a display —
// buried a .NET stack trace in launcher.log that reads like a failure and is
// not one. Diagnostic noise that mimics a fault costs more than the process it
// comes from.
//
// Applied at all three places a umu/Proton command line is built (launch,
// warmPrefix, runInPrefix); 2026-08-31 真机确认，见
// docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md §12.
const protonNoXalia = "PROTON_USE_XALIA=0"

// prefixDir resolves the Wine prefix directory for one launch. key is
// opt.PrefixKey; empty means the default shared prefix regardless of
// PrefixMode (isolating callers must supply a key to actually get isolation —
// see docs/LINUX_COMPATIBILITY_PLAN.md §6 risk 6).
//
// This is also where "does the whole program serialize launches" is decided:
// sharesWinePrefix asks this function whether two different keys land in the
// same directory, rather than repeating the mode table. Anything unrecognized
// therefore falls through to the shared prefix on purpose.
func prefixDir(cfg Config, key string) string {
	base := cfg.PrefixDir
	if base == "" {
		base = filepath.Join(cfg.BaseDir, "umu-prefix")
	}
	if key == "" {
		return base
	}
	switch cfg.PrefixMode {
	case "per-instance":
		return base + "-" + key
	case "overlay":
		// The mount point, not the lower: that directory sits on its own
		// overlay superblock, which is what gives the instance its own
		// wineserver. cfg.PrefixDir moves the lower only — see overlay.go.
		return overlayMergedDir(cfg, key)
	}
	return base
}

// runtimeMu serializes EnsureRuntime: concurrent first-time initialization
// (two instances starting at once on a fresh install) would otherwise race
// on the same umu-run/GE-Proton download and prefix warm-up — see
// docs/LINUX_COMPATIBILITY_PLAN.md §6 risk 6.
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

	// Create + reconcile the dedicated non-root user first: warmPrefix below
	// runs wineboot as that user, and it must be able to write the prefix and
	// its own HOME. See docs/UMU_RUNTIME_USER_PLAN.md §3.2.
	if err := ensureRuntimeUser(ctx); err != nil {
		return fmt.Errorf("failed to prepare the non-root runtime user: %w", err)
	}

	if cfg.Runtime == "custom" {
		logf("linux runtime mode is \"custom\": skipping umu/GE-Proton download, verifying the pre-configured runtime instead")
		return checkRuntime()
	}
	if !cfg.AutoDownload {
		return fmt.Errorf("runner: auto_download is disabled and runtime is not fully installed (see GET /api/system/preflight)")
	}

	if err := ensureUmu(ctx, cfg, logf); err != nil {
		return fmt.Errorf("failed to install umu-launcher: %w", err)
	}
	if err := ensureGEProton(ctx, cfg, logf); err != nil {
		return fmt.Errorf("failed to install %s: %w", cfg.ProtonVersion, err)
	}

	// warmPrefix 下面那次 wineboot 会让 umu 自己去 repo.steampowered.com 拉
	// 150~190 MB 的 Steam Linux Runtime，用的是它内置的 urllib3 —— 我们的重试、
	// 断点续传、download.http_proxy 一个都够不着。先用 pkg/download 下好塞进 umu
	// 自己的下载缓存，wineboot 起来时就只剩「续传补最后 1 MiB」。
	//
	// 失败只降级不阻断：最坏回到今天的行为（umu 自己下）。这个优化的全部价值是省
	// 时间，为省时间制造一个新的安装失败点是净亏。
	// 见 docs/STEAMRT_PREFETCH_PLAN.md。
	prefetched, err := prefetchSteamRuntime(ctx, cfg, logf)
	if err != nil {
		logf("Steam Linux Runtime 预下载失败（%v），改由 umu 自行下载", err)
	}

	// 从这里往下才开始动共享前缀本身。overlay 模式下它可能正被若干实例的可写层
	// 当 lowerdir 引用着 —— 这时改它是未定义行为。
	//
	// 判据是「还有没有事要做」而不是「有没有挂载」：本函数在每次 API 启动时都会
	// 后台跑一遍，而挂载是**故意**跨重启存活的（停实例不卸载），一见挂载就报错
	// 等于第一个实例起过之后永远起不来。见 lowerNeedsWork。
	doneWrite, err := prepareSharedPrefixWrite("环境准备 EnsureRuntime")
	if err != nil {
		if lowerNeedsWork(cfg) {
			return err
		}
		logf("共享 Wine 前缀已是最新，跳过重建（当前有实例的可写层挂在它上面）")
		return nil
	}
	defer doneWrite()

	if err := warmPrefix(ctx, cfg, "", logf, prefetched.Variant != ""); err != nil {
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

// downloadProgress 把 pkg/download 的字节级回调节流成人能看的进度行：每 5% 或每 2 秒
// 一行，外加收尾那一行。回调在 io.Copy 的单个 goroutine 里串行调用，无需加锁。
//
// 总长未知（total <= 0）时只按时间节流 —— 否则百分比恒为 0，「涨够 5%」永远不成立，
// 每读一个块就会打一行。
func downloadProgress(label string, logf func(string, ...any)) func(done, total int64) {
	var (
		lastAt  time.Time
		lastPct = -1
	)
	return func(done, total int64) {
		pct := 0
		if total > 0 {
			pct = int(done * 100 / total)
		}
		final := total > 0 && done >= total
		now := time.Now()
		if !final && pct < lastPct+5 && now.Sub(lastAt) < 2*time.Second {
			return
		}
		lastAt, lastPct = now, pct
		if total > 0 {
			logf("  %s: %d%% (%.1f/%.1f MiB)", label, pct, mib(done), mib(total))
		} else {
			logf("  %s: %.1f MiB", label, mib(done))
		}
	}
}

func mib(n int64) float64 { return float64(n) / (1 << 20) }

// ensureUmu downloads+extracts the umu-launcher zipapp if umu-run isn't
// already present at the pinned version's expected path.
func ensureUmu(ctx context.Context, cfg Config, logf func(string, ...any)) error {
	bin := umuRunPath(cfg)
	if fi, err := os.Stat(bin); err == nil && fi.Mode()&0111 != 0 {
		return nil
	}

	const owner, repo = "Open-Wine-Components", "umu-launcher"
	asset := fmt.Sprintf("umu-launcher-%s-zipapp.tar", cfg.UmuVersion)
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, cfg.UmuVersion, asset)

	logf("downloading umu-launcher %s from %s", cfg.UmuVersion, url)

	// umu's release page publishes no standalone checksum file (unlike
	// GE-Proton's .sha512sum) — the only trustworthy value is the digest
	// GitHub computes for the asset, exposed via the Releases API. This is
	// a single small metadata GET (not a "resolve latest" call, and not on
	// the hot path of every server start), so it doesn't reintroduce the
	// api.github.com rate-limit problem §4.3 warns about — that problem is
	// specifically about resolving "latest"/aliases on every run.
	// A failure here degrades to an unverified download rather than
	// blocking setup entirely: umu-run is a small, auditable launcher, not
	// the large untrusted payload GE-Proton is.
	checksum := ""
	if digest, err := fetchGithubAssetDigest(ctx, owner, repo, cfg.UmuVersion, asset); err != nil {
		logf("warning: could not fetch umu-launcher checksum (%v); proceeding without verification", err)
	} else {
		checksum = digest
	}

	archivePath := filepath.Join(umuDir(cfg), asset)
	if err := download.Fetch(ctx, download.Options{
		URL: url, Dest: archivePath, Checksum: checksum, Resume: true,
	}); err != nil {
		return err
	}
	defer os.Remove(archivePath)

	// The tar contains a single "umu/" directory (umu-run + a symlink);
	// strip that prefix so umu-run lands directly at umuDir(cfg)/umu-run.
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := archive.ExtractTar(f, umuDir(cfg), "umu/"); err != nil {
		return fmt.Errorf("failed to extract umu-launcher archive: %w", err)
	}

	if err := os.Chmod(bin, 0755); err != nil {
		return fmt.Errorf("failed to make umu-run executable: %w", err)
	}
	logf("umu-launcher %s installed at %s", cfg.UmuVersion, umuDir(cfg))
	return nil
}

// ensureGEProton downloads+extracts the pinned GE-Proton build if its
// `proton` entry point isn't already present.
func ensureGEProton(ctx context.Context, cfg Config, logf func(string, ...any)) error {
	protonBin := filepath.Join(protonPath(cfg), "proton")
	if fi, err := os.Stat(protonBin); err == nil && !fi.IsDir() {
		return nil
	}

	const owner, repo = "GloriousEggroll", "proton-ge-custom"
	tag := cfg.ProtonVersion
	assetName := tag + ".tar.gz"
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, tag, assetName)

	logf("downloading %s (~450 MB, this can take a while)", tag)

	checksum, err := fetchSha512Checksum(ctx, owner, repo, tag, assetName)
	if err != nil {
		// Unlike umu, GE-Proton is a large third-party binary blob running
		// as the actual game process's runtime — docs/LINUX_COMPATIBILITY_PLAN.md
		// risk #17 is explicit that a proxy/CDN silently returning a
		// truncated or tampered file here fails in the worst possible way
		// (GE-Proton11-style silent hang, no log line at all). Refuse to
		// proceed without a verified checksum.
		return fmt.Errorf("failed to fetch published checksum for %s (refusing to download unverified): %w", assetName, err)
	}

	archivePath := filepath.Join(protonBaseDir(cfg), assetName)
	if err := download.Fetch(ctx, download.Options{
		URL: url, Dest: archivePath, Checksum: checksum, Resume: true,
	}); err != nil {
		return err
	}
	defer os.Remove(archivePath)

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to open %s as gzip: %w", assetName, err)
	}
	defer gz.Close()

	// No strip-prefix: the archive's own top-level entry is "GE-Proton10-34/",
	// which is exactly the directory name we want under protonBaseDir.
	if err := archive.ExtractTar(gz, protonBaseDir(cfg), ""); err != nil {
		return fmt.Errorf("failed to extract %s: %w", assetName, err)
	}

	if fi, statErr := os.Stat(protonBin); statErr != nil || fi.IsDir() {
		return fmt.Errorf("%s extracted but %s is missing — the archive layout may have changed", tag, protonBin)
	}
	logf("%s installed at %s", tag, protonPath(cfg))
	return nil
}

// warmPrefix runs `umu-run wineboot --init` once to force both the Steam
// Linux Runtime download and Wine prefix creation, exactly as
// scripts/ark_instance_manager.sh's install_base_server() does — including
// its two preconditions: a version-mismatch check that recreates a prefix
// left by an incompatible Proton generation, and a wineserver-drain poll
// afterward so a caller-visible "ready" doesn't race the prefix still being
// held open.
//
// key selects which prefix to warm, with Options.PrefixKey's meaning: "" is
// the shared prefix (EnsureRuntime's caller), a non-empty key is one
// instance's own prefix under prefix_mode "per-instance" (ensurePrefix's
// caller). Everything below is per-prefix — the version marker, the chown, the
// wineserver drain — so the only thing the two callers don't share is who
// takes the lock around it.
func warmPrefix(ctx context.Context, cfg Config, key string, logf func(string, ...any), prefetched bool) error {
	prefix := prefixDir(cfg, key)
	if err := os.MkdirAll(prefix, 0755); err != nil {
		return err
	}

	if err := reconcilePrefixVersion(prefix, cfg.ProtonVersion, logf); err != nil {
		return err
	}

	runtimeReady := steamLinuxRuntimeReady(cfg)

	if prefixInitialized(prefix) && runtimeReady {
		return writePrefixMarker(prefix, cfg.ProtonVersion)
	}

	if prefetched {
		logf("first-time umu setup: 正在解压已预下载的 Steam Linux Runtime 并初始化 Wine 前缀（可能需要几分钟）")
	} else {
		logf("first-time umu setup: downloading Steam Linux Runtime and initializing the Wine prefix (this can take several minutes)")
	}

	// wineboot below may run as the dropped non-root user (see below); it must
	// be able to write into the prefix dir, which was just MkdirAll'd (or
	// recreated by reconcilePrefixVersion) as root. No-op when not managing a
	// user. See docs/UMU_RUNTIME_USER_PLAN.md §3.2.
	if err := chownPathForRuntime(prefix); err != nil {
		return fmt.Errorf("failed to hand prefix dir to the runtime user: %w", err)
	}

	py, err := umuInterpreter()
	if err != nil {
		return fmt.Errorf("failed to resolve a Python interpreter for umu-run: %w", err)
	}

	cmd := exec.CommandContext(ctx, py.Path, umuRunPath(cfg), "wineboot", "--init")
	// inheritedEnv, not os.Environ(): a leaked DBUS_SESSION_BUS_ADDRESS
	// pointing at root's session bus makes bwrap refuse to start the whole
	// container. See inheritedEnv's comment.
	cmd.Env = append(inheritedEnv(),
		"WINEPREFIX="+prefix,
		"GAMEID="+cfg.GameID,
		"PROTONPATH="+protonPath(cfg),
		// Deliberately no UMU_RUNTIME_UPDATE=0 here: this is the one
		// invocation that must be allowed to fetch a missing runtime.
		protonNoXalia,
	)
	// Warm the prefix as the same non-root user that will later run the
	// game, so the prefix + Steam Linux Runtime cache are created with the
	// right owner from the start (docs/UMU_RUNTIME_USER_PLAN.md §3.2).
	if cred, home, err := resolveRuntimeCredential(cfg); err != nil {
		return err
	} else if cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
		cmd.Env = runtimeEnv(cmd.Env, home, runtimeUserName(cfg))
	}
	out := &progressWriter{logf: logf}
	cmd.Stdout = out
	cmd.Stderr = out
	// A non-zero exit is tolerated on its own — the reference script's `|| true`
	// is right that wineboot can grumble and still have built the prefix. What
	// is NOT tolerated is an unverified result: the exit code is kept only to
	// annotate the verdict the filesystem gives us below.
	runErr := cmd.Run()

	waitForWineserverDrain(prefix)

	// The post-condition. Without it a failed wineboot was announced as
	// "ready", and the operator only found out minutes later, from a different
	// package, via a message telling them to run the very command that had just
	// silently failed. See docs/UMU_PREFIX_INIT_TROUBLESHOOTING.md.
	if !prefixInitialized(prefix) {
		return fmt.Errorf("Wine 前缀初始化失败：%s 里没有生成 system.reg%s。wineboot 最后的输出：\n%s",
			prefix, exitNote(runErr), out.tail())
	}

	logf("umu runtime and Wine prefix ready")
	return writePrefixMarker(prefix, cfg.ProtonVersion)
}

// prefixInitialized is the single judgement of "this Wine prefix is usable",
// shared by the pre-check and the post-check so they can't drift apart.
func prefixInitialized(prefix string) bool {
	return fileExists(filepath.Join(prefix, "system.reg")) &&
		dirExists(filepath.Join(prefix, "drive_c", "windows", "system32"))
}

func exitNote(err error) string {
	if err == nil {
		return "（wineboot 进程本身是正常退出的）"
	}
	return fmt.Sprintf("（wineboot: %v）", err)
}

// progressWriter forwards every line to logf and keeps the last few, so a
// failure can quote them inline instead of sending the operator digging
// through asaServer.log for one line buried in hundreds.
type progressWriter struct {
	logf func(string, ...any)
	mu   sync.Mutex // Stdout and Stderr are written from two goroutines
	last []string
}

const progressTailLines = 8

func (w *progressWriter) Write(p []byte) (int, error) {
	if line := strings.TrimSpace(string(p)); line != "" {
		w.logf("%s", line)

		w.mu.Lock()
		w.last = append(w.last, line)
		if len(w.last) > progressTailLines {
			w.last = w.last[len(w.last)-progressTailLines:]
		}
		w.mu.Unlock()
	}
	return len(p), nil
}

func (w *progressWriter) tail() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.last) == 0 {
		return "  （没有任何输出）"
	}
	return "  " + strings.Join(w.last, "\n  ")
}

// reconcilePrefixVersion moves an existing prefix aside if it was created
// by a different Proton build than cfg.ProtonVersion (Wine prefixes don't
// tolerate cross-generation reuse — see
// docs/LINUX_COMPATIBILITY_PLAN.md §6 risk 5). A prefix with no marker at
// all is treated as unknown provenance and rebuilt too — that covers every
// prefix created before this mechanism existed, including ones from the
// briefly-pinned, ASA-incompatible GE-Proton11-1.
func reconcilePrefixVersion(prefix, wantVersion string, logf func(string, ...any)) error {
	markerPath := filepath.Join(prefix, ".created-by-proton")
	if !fileExists(filepath.Join(prefix, "system.reg")) {
		return nil // nothing to reconcile yet
	}

	got, _ := os.ReadFile(markerPath)
	gotVersion := strings.TrimSpace(string(got))
	if gotVersion == wantVersion {
		return nil
	}

	backup := prefix + ".bak-" + firstNonEmpty(gotVersion, "unknown")
	logf("existing Wine prefix was created by %q, current is %q; moving it to %s and creating a fresh prefix",
		firstNonEmpty(gotVersion, "an unknown Proton build"), wantVersion, backup)

	_ = os.RemoveAll(backup)
	if err := os.Rename(prefix, backup); err != nil {
		return fmt.Errorf("failed to move aside stale prefix: %w", err)
	}
	return os.MkdirAll(prefix, 0755)
}

func writePrefixMarker(prefix, version string) error {
	path := filepath.Join(prefix, ".created-by-proton")
	if err := os.WriteFile(path, []byte(version), 0644); err != nil {
		return err
	}
	// prefix 归运行时用户，别在里面留 root 属主的文件 —— 同 writeVCRedistMarker。
	//
	// 这一行不是可选的收尾：warmPrefix 的 chownPathForRuntime 在 wineboot **之前**，
	// 而这个标记是最后由 root 写的，于是它是整个 prefix 里唯一属主错误的条目，
	// 而 VerifyRuntimeAccessForLaunch 的 umu-runtime-owner-drift 抽样恰好逮它。
	// 共享 prefix 上一直没暴露：它在 setup 期间创建，等到实例启动时
	// asa-server 早已重启过，reconcileRuntimeOwnership 顺手就修了。
	// per-instance 把创建挪进启动流程本身，中间没有重启，于是当场失败。
	return chownPathForRuntime(path)
}

// steamLinuxRuntimeReady checks for the toolmanifest the Proton generation
// behind cfg.ProtonVersion needs under ~/.local/share/umu (umu's own runtime
// cache, independent of WINEPREFIX — see docs/LINUX_COMPATIBILITY_PLAN.md §4.1).
//
// Which variant that is comes from steamrtForProton, the same single mapping
// the prefetch uses — "which runtime do we download" and "which runtime counts
// as already installed" must never be able to answer differently. A present
// steamrt4 must not mask a missing steamrt3 after a downgrade; an
// unrecognized/future generation conservatively accepts any installed runtime
// rather than forcing a re-download.
func steamLinuxRuntimeReady(cfg Config) bool {
	// umu's runtime cache lives under the HOME the game process runs with —
	// the dropped non-root user's home when we manage one, not root's.
	home := runtimeHomeDir(cfg)
	if home == "" {
		return false
	}
	glob := "steamrt*"
	if v, ok := steamrt.ForProton(protonPath(cfg), cfg.ProtonVersion); ok {
		glob = v.Variant
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".local/share/umu", glob, "toolmanifest.vdf"))
	return len(matches) > 0
}

// waitForWineserverDrain polls (up to 90s, matching the reference script)
// for no wineserver process to still be holding prefix open, so a
// caller-visible "runtime ready" doesn't race a prefix that's still being
// written to.
func waitForWineserverDrain(prefix string) {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if !wineserverHoldsPrefix(prefix) {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// wineserverHoldsPrefix reports whether a wineserver process is still serving
// prefix.
//
// This used to be QueryProcess("wineserver", prefix) — matching the prefix
// against the *command line*, which never contains it: wineserver is launched
// as a bare path and learns its prefix from the environment. The query
// therefore returned an empty set every time and the whole 90s drain was a
// no-op. Verified on a live server (docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md Q6):
//
//	cmdline: /opt/.../GE-Proton10-34/files/bin/wineserver
//	environ: WINEPREFIX=/opt/.../umu-prefix/pfx/
//
// Note the value umu actually exports is "<prefix>/pfx/" — one level deeper
// than the configured prefix, with a trailing slash — so this asks "is that
// value inside prefix" rather than comparing for equality. That containment
// test is wineprefixValueUnder, and it must stay a **path**-boundary test:
// our prefixes are name-stem siblings, so a plain strings.HasPrefix reports
// the shared prefix as held whenever any per-instance one is. See its comment.
func wineserverHoldsPrefix(prefix string) bool {
	procs, err := procx.QueryProcess("wineserver", "")
	if err != nil {
		return false
	}
	want := strings.TrimRight(prefix, "/")
	if want == "" {
		return len(procs) > 0
	}
	for _, p := range procs {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", p.ProcessId))
		if err != nil {
			continue // exited, or not ours to read
		}
		for _, kv := range strings.Split(string(data), "\x00") {
			v, ok := strings.CutPrefix(kv, "WINEPREFIX=")
			if !ok {
				continue
			}
			if wineprefixValueUnder(v, want) {
				return true
			}
		}
	}
	return false
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- Checksums ---

type ghAsset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type ghRelease struct {
	Assets []ghAsset `json:"assets"`
}

// fetchGithubAssetDigest fetches the sha256 digest GitHub computes for one
// release asset via the Releases API (GET .../releases/tags/{tag}, a single
// small metadata request — not the "resolve latest" pattern
// docs/LINUX_COMPATIBILITY_PLAN.md §4.3 avoids). Uses download.Client() so
// it still honors the configured corporate HTTPProxy fallback.
func fetchGithubAssetDigest(ctx context.Context, owner, repo, tag, assetName string) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := download.Client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api returned %s", resp.Status)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	for _, a := range rel.Assets {
		if a.Name == assetName {
			if a.Digest == "" {
				return "", fmt.Errorf("asset %s has no digest in the API response", assetName)
			}
			return a.Digest, nil
		}
	}
	return "", fmt.Errorf("asset %s not found in release %s", assetName, tag)
}

// fetchSha512Checksum downloads GE-Proton's published "<tag>.sha512sum"
// companion file (a normal release-download URL — proxy-able, not
// rate-limited, unlike the GitHub API) and extracts the hash for
// assetName. The file's format is the standard `sha512sum` tool output:
// "<hex>  <filename>" per line.
func fetchSha512Checksum(ctx context.Context, owner, repo, tag, assetName string) (string, error) {
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s.sha512sum", owner, repo, tag, tag)

	tmp, err := os.CreateTemp("", "ge-proton-sha512sum-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := download.Fetch(ctx, download.Options{URL: url, Dest: tmpPath}); err != nil {
		return "", err
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash, name := fields[0], fields[1]
		if strings.TrimPrefix(name, "*") == assetName {
			return "sha512:" + hash, nil
		}
	}
	return "", fmt.Errorf("%s.sha512sum has no entry for %s", tag, assetName)
}
