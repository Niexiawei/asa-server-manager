//go:build linux

package umu

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"asa-server/pkg/procx"
	"asa-server/pkg/steamrt"
)

// CacheDir is umu's download staging directory, UMU_CACHE.
//
// umu_consts.py: UMU_CACHE = XDG_CACHE_HOME/umu, XDG_CACHE_HOME defaulting to
// ~/.cache. In this process tree XDG_* is always empty (the launch env
// whitelist never lets it through, and the runtime-user env rewrite strips
// it again), so this is always {HomeDir}/.cache/umu.
func (r *Runtime) CacheDir() string {
	return filepath.Join(r.config().homeDir(), ".cache", "umu")
}

// PrefetchSteamRuntime prefetches the Steam Linux Runtime archive the
// configured Proton build needs (asa-server/pkg/steamrt), ahead of the
// wineboot call that would otherwise download it via umu's own unthrottled
// client. Returns the zero Variant, nil when prefetching isn't applicable —
// never treat a non-nil error as fatal; the caller degrades to "let umu
// download it" and logs.
func (r *Runtime) PrefetchSteamRuntime(ctx context.Context, logf func(string, ...any)) (steamrt.Variant, error) {
	cfg := r.config()
	if !cfg.SteamRTPrefetch || !cfg.AutoDownload {
		return steamrt.Variant{}, nil
	}
	if _, ok := steamrt.ForProton(r.ProtonPath(), cfg.ProtonVersion); !ok {
		logf("跳过 Steam Linux Runtime 预下载：认不出 %s 需要哪个运行时变体，交给 umu 自行判断", cfg.ProtonVersion)
		return steamrt.Variant{}, nil
	}
	if r.SteamLinuxRuntimeReady() {
		return steamrt.Variant{}, nil
	}
	if cfg.homeDir() == "" {
		return steamrt.Variant{}, fmt.Errorf("无法确定 umu 缓存所在的家目录")
	}
	return steamrt.Prefetch(ctx, r.ProtonPath(), cfg.ProtonVersion, r.CacheDir(), cfg.chownPath, logf)
}

// SteamLinuxRuntimeReady checks for the toolmanifest the configured Proton
// build needs under ~/.local/share/umu (umu's own runtime cache, independent
// of WINEPREFIX).
//
// Which variant that is comes from the same steamrt.ForProton mapping
// PrefetchSteamRuntime uses — "which runtime do we download" and "which
// runtime counts as already installed" must never disagree. A present
// steamrt4 must not mask a missing steamrt3 after a downgrade; an
// unrecognized/future generation conservatively accepts any installed
// runtime rather than forcing a re-download.
func (r *Runtime) SteamLinuxRuntimeReady() bool {
	cfg := r.config()
	home := cfg.homeDir()
	if home == "" {
		return false
	}
	glob := "steamrt*"
	if v, ok := steamrt.ForProton(r.ProtonPath(), cfg.ProtonVersion); ok {
		glob = v.Variant
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".local/share/umu", glob, "toolmanifest.vdf"))
	return len(matches) > 0
}

// EnsureUmu downloads+extracts the umu-launcher zipapp if umu-run isn't
// already present at the pinned version's expected path.
func (r *Runtime) EnsureUmu(ctx context.Context, logf func(string, ...any)) error {
	cfg := r.config()
	bin := r.RunPath()
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
	// api.github.com rate-limit problem. A failure here degrades to an
	// unverified download rather than blocking setup entirely: umu-run is a
	// small, auditable launcher, not the large untrusted payload GE-Proton is.
	checksum := ""
	if digest, err := fetchGithubAssetDigest(ctx, owner, repo, cfg.UmuVersion, asset); err != nil {
		logf("warning: could not fetch umu-launcher checksum (%v); proceeding without verification", err)
	} else {
		checksum = digest
	}

	archivePath := filepath.Join(r.Dir(), asset)
	if err := download.Fetch(ctx, download.Options{
		URL: url, Dest: archivePath, Checksum: checksum, Resume: true,
	}); err != nil {
		return err
	}
	defer os.Remove(archivePath)

	// The tar contains a single "umu/" directory (umu-run + a symlink);
	// strip that prefix so umu-run lands directly at r.Dir()/umu-run.
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := archive.ExtractTar(f, r.Dir(), "umu/"); err != nil {
		return fmt.Errorf("failed to extract umu-launcher archive: %w", err)
	}

	if err := os.Chmod(bin, 0755); err != nil {
		return fmt.Errorf("failed to make umu-run executable: %w", err)
	}
	logf("umu-launcher %s installed at %s", cfg.UmuVersion, r.Dir())
	return nil
}

// EnsureGEProton downloads+extracts the pinned GE-Proton build if its
// `proton` entry point isn't already present.
func (r *Runtime) EnsureGEProton(ctx context.Context, logf func(string, ...any)) error {
	cfg := r.config()
	protonBin := filepath.Join(r.ProtonPath(), "proton")
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
		// as the actual game process's runtime — a proxy/CDN silently
		// returning a truncated or tampered file here fails in the worst
		// possible way (a silent hang, no log line at all). Refuse to
		// proceed without a verified checksum.
		return fmt.Errorf("failed to fetch published checksum for %s (refusing to download unverified): %w", assetName, err)
	}

	archivePath := filepath.Join(r.ProtonBaseDir(), assetName)
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
	// which is exactly the directory name we want under ProtonBaseDir.
	if err := archive.ExtractTar(gz, r.ProtonBaseDir(), ""); err != nil {
		return fmt.Errorf("failed to extract %s: %w", assetName, err)
	}

	if fi, statErr := os.Stat(protonBin); statErr != nil || fi.IsDir() {
		return fmt.Errorf("%s extracted but %s is missing — the archive layout may have changed", tag, protonBin)
	}
	logf("%s installed at %s", tag, r.ProtonPath())
	return nil
}

// CheckRuntime verifies umu-run and the pinned GE-Proton build are present,
// with no network access. Error text is end-user facing. Does not check any
// Wine prefix — a caller that manages prefixes checks that separately.
func (r *Runtime) CheckRuntime() error {
	cfg := r.config()
	bin := r.RunPath()
	if fi, err := os.Stat(bin); err != nil || fi.Mode()&0111 == 0 {
		return fmt.Errorf("Wine/Proton 运行时尚未初始化：缺少 umu-run（%s）。请运行 asa-server setup 完成环境准备", bin)
	}
	proton := r.ProtonPath()
	if fi, err := os.Stat(filepath.Join(proton, "proton")); err != nil || fi.IsDir() {
		return fmt.Errorf("Wine/Proton 运行时尚未初始化：缺少 %s（%s）。请运行 asa-server setup 完成环境准备", cfg.ProtonVersion, proton)
	}
	return nil
}

// WarmPrefix runs `umu-run wineboot --init` once against prefix to force
// both the Steam Linux Runtime download and Wine prefix creation, exactly as
// scripts/ark_instance_manager.sh's install_base_server() does — including
// its two preconditions: a version-mismatch check that recreates a prefix
// left by an incompatible Proton generation, and a wineserver-drain poll
// afterward so a caller-visible "ready" doesn't race the prefix still being
// held open.
//
// prefix is the directory to warm — the caller (asa-server/pkg/wineprefix)
// decides which one that is; this method has no notion of "shared" vs
// "per-instance". prefetched marks whether the Steam Linux Runtime was
// already prefetched (only changes the log wording).
func (r *Runtime) WarmPrefix(ctx context.Context, prefix string, logf func(string, ...any), prefetched bool) error {
	cfg := r.config()
	if err := os.MkdirAll(prefix, 0755); err != nil {
		return err
	}

	if err := reconcilePrefixVersion(prefix, cfg.ProtonVersion, logf); err != nil {
		return err
	}

	runtimeReady := r.SteamLinuxRuntimeReady()

	if PrefixInitialized(prefix) && runtimeReady {
		return writePrefixMarker(prefix, cfg.ProtonVersion, cfg.chownPath)
	}

	if prefetched {
		logf("first-time umu setup: 正在解压已预下载的 Steam Linux Runtime 并初始化 Wine 前缀（可能需要几分钟）")
	} else {
		logf("first-time umu setup: downloading Steam Linux Runtime and initializing the Wine prefix (this can take several minutes)")
	}

	// wineboot below may run as the dropped non-root user; it must be able
	// to write into the prefix dir, which was just MkdirAll'd (or recreated
	// by reconcilePrefixVersion) as root. No-op when not managing a user.
	if err := cfg.chownPath(prefix); err != nil {
		return fmt.Errorf("failed to hand prefix dir to the runtime user: %w", err)
	}

	// A non-zero exit is tolerated on its own — the reference script's `|| true`
	// is right that wineboot can grumble and still have built the prefix. What
	// is NOT tolerated is an unverified result: the exit code is kept only to
	// annotate the verdict the filesystem gives us below.
	//
	// Deliberately no NoRuntimeUpdate and no Verb: this is the one invocation
	// that must be allowed to fetch a missing runtime, and the one that wants
	// umu's default waitforexitandrun. See RunOptions' field comments.
	tail, runErr := r.RunInPrefix(ctx, prefix, []string{"wineboot", "--init"}, RunOptions{}, logf)
	if errors.Is(runErr, ErrNoInterpreter) {
		return runErr
	}

	WaitForWineserverDrain(prefix)

	// The post-condition. Without it a failed wineboot was announced as
	// "ready", and the operator only found out minutes later, from a
	// different package, via a message telling them to run the very command
	// that had just silently failed.
	if !PrefixInitialized(prefix) {
		return fmt.Errorf("Wine 前缀初始化失败：%s 里没有生成 system.reg%s。wineboot 最后的输出：\n%s",
			prefix, exitNote(runErr), tail)
	}

	logf("umu runtime and Wine prefix ready")
	return writePrefixMarker(prefix, cfg.ProtonVersion, cfg.chownPath)
}

// RunOptions tunes a single RunInPrefix call. The zero value is what
// WarmPrefix's wineboot needs; every field exists because exactly one caller
// deviates from it.
type RunOptions struct {
	// Timeout caps the whole run. 0 = no cap.
	//
	// Needed because /quiet does not actually guarantee no dialog: under Wine
	// a WiX Burn bootstrapper can still pop up a box nobody will ever click,
	// hanging the caller forever.
	Timeout time.Duration
	// ExtraEnv is appended **last**, after the runtime-user rewrite. That
	// ordering is deliberate and load-bearing for DISPLAY: RuntimeEnv strips
	// XDG_*, and exec takes the last occurrence of a duplicated name, so
	// appending last keeps a future filter from eating it.
	//
	// This is also how "a display" reaches umu-run without this package having
	// to know what a display is — resolving one is a caller-side business
	// rule (internal/runner's display candidate chain).
	ExtraEnv []string
	// NoRuntimeUpdate sets UMU_RUNTIME_UPDATE=0 — the runtime is already
	// installed by the time those callers run, so there is no reason to let umu
	// go ask repo.steampowered.com about updates. WarmPrefix must NOT set it:
	// fetching a missing runtime is that call's whole job.
	NoRuntimeUpdate bool
	// Verb sets PROTON_VERB. Empty keeps umu's default (waitforexitandrun).
	//
	// "run" is required for anything touching a prefix that may already have a
	// game in it: waitforexitandrun starts with `wineserver -w`, which never
	// returns while another instance holds the same prefix — the caller then
	// hangs until Timeout with no useful clue as to why.
	Verb string
}

// runEnv assembles the environment every umu-run invocation starts from —
// everything except the runtime-user rewrite and ExtraEnv, both of which have
// to come after it (see RunInPrefix).
//
// Split out so the two deliberate deviations between callers are unit
// testable: WarmPrefix's wineboot passes the zero RunOptions and must end up
// with neither UMU_RUNTIME_UPDATE nor PROTON_VERB set, because it is the one
// call allowed to fetch a missing runtime and the one that wants umu's
// default verb.
func (r *Runtime) runEnv(prefix string, opt RunOptions) []string {
	cfg := r.config()
	// InheritedEnv, not os.Environ(): a leaked DBUS_SESSION_BUS_ADDRESS
	// pointing at root's session bus makes bwrap refuse to start the whole
	// container. See InheritedEnv's comment.
	env := append(InheritedEnv(),
		"WINEPREFIX="+prefix,
		"GAMEID="+cfg.GameID,
		"PROTONPATH="+r.ProtonPath(),
		ProtonNoXalia,
	)
	if opt.NoRuntimeUpdate {
		env = append(env, "UMU_RUNTIME_UPDATE=0")
	}
	if opt.Verb != "" {
		env = append(env, "PROTON_VERB="+opt.Verb)
	}
	return env
}

// ErrNoInterpreter marks "there is no usable Python for umu-run", so a caller
// can tell a launch that never happened from one that ran and failed without
// matching on error text.
var ErrNoInterpreter = errors.New("no usable Python interpreter for umu-run")

// RunInPrefix runs a command under umu-run against prefix, to completion, and
// returns the tail of its output.
//
// argv is everything after umu-run — either a Windows exe path plus its
// arguments, or a Proton verb like "wineboot". The full command line is
// `<python> <umu-run> <argv...>`, matching scripts/ark_instance_manager.sh.
//
// This is the one place that knows how to run something in a prefix: both
// WarmPrefix's wineboot and internal/runner's VC++ runtime installation go
// through it, so their environments cannot drift apart. It deliberately does
// **not** WaitForWineserverDrain — whether the prefix must be quiet again
// before the caller continues is the caller's judgement, and one of them (the
// DLL-override regedit import) has no reason to wait.
func (r *Runtime) RunInPrefix(ctx context.Context, prefix string, argv []string,
	opt RunOptions, logf func(string, ...any)) (string, error) {

	cfg := r.config()

	py, err := r.Interpreter()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoInterpreter, err)
	}

	if opt.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opt.Timeout)
		defer cancel()
	}

	env := r.runEnv(prefix, opt)

	cmd := exec.CommandContext(ctx, py.Path, append([]string{r.RunPath()}, argv...)...)
	// Run as the same non-root user that will later run the game, so whatever
	// this writes into the prefix (and into the Steam Linux Runtime cache) has
	// the right owner from the start.
	if cred, err := cfg.credential(); err != nil {
		return "", err
	} else if cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
		env = RuntimeEnv(env, cfg.homeDir(), cfg.userName())
	}
	cmd.Env = append(env, opt.ExtraEnv...)

	out := NewOutputCapture(logf)
	cmd.Stdout, cmd.Stderr = out, out
	runErr := cmd.Run()
	return out.Tail(), runErr
}

// runtimeUserNameHint is a best-effort label for RuntimeEnv's USER/LOGNAME
// rewrite. Runtime doesn't own the runtime user's name (sysuser does) — the
// caller only injected uid/gid/credential resolution, not the name — so this
// falls back to a generic placeholder rather than needing a fifth callback
// for a cosmetic env var.
func (r *Runtime) runtimeUserNameHint() string {
	if uid, _, managed := r.config().childIDs(); managed {
		return fmt.Sprintf("uid%d", uid)
	}
	return ""
}

// PrefixInitialized is the single judgement of "this Wine prefix is usable",
// shared by every pre-check and post-check so they can't drift apart.
func PrefixInitialized(prefix string) bool {
	return fileExists(filepath.Join(prefix, "system.reg")) &&
		dirExists(filepath.Join(prefix, "drive_c", "windows", "system32"))
}

func exitNote(err error) string {
	if err == nil {
		return "（wineboot 进程本身是正常退出的）"
	}
	return fmt.Sprintf("（wineboot: %v）", err)
}

// OutputCapture forwards every line to logf and keeps the last few, so a
// failure can quote them inline instead of sending the operator digging
// through a log file for one line buried in hundreds.
type OutputCapture struct {
	logf func(string, ...any)
	mu   sync.Mutex // Stdout and Stderr are written from two goroutines
	last []string
}

const outputTailLines = 8

// NewOutputCapture returns an io.Writer that logs each line via logf and
// remembers the last few for Tail.
func NewOutputCapture(logf func(string, ...any)) *OutputCapture {
	return &OutputCapture{logf: logf}
}

func (w *OutputCapture) Write(p []byte) (int, error) {
	if line := strings.TrimSpace(string(p)); line != "" {
		w.logf("%s", line)

		w.mu.Lock()
		w.last = append(w.last, line)
		if len(w.last) > outputTailLines {
			w.last = w.last[len(w.last)-outputTailLines:]
		}
		w.mu.Unlock()
	}
	return len(p), nil
}

// ExitCode extracts a process's exit code from the error umu-run's Wait/Run
// returned; -1 means it didn't end normally (killed by a signal, including a
// context timeout) rather than exiting with a nonzero status.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// Tail returns the last few lines written, for quoting in an error.
func (w *OutputCapture) Tail() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.last) == 0 {
		return "  （没有任何输出）"
	}
	return "  " + strings.Join(w.last, "\n  ")
}

// reconcilePrefixVersion moves an existing prefix aside if it was created by
// a different Proton build than wantVersion (Wine prefixes don't tolerate
// cross-generation reuse). A prefix with no marker at all is treated as
// unknown provenance and rebuilt too.
func reconcilePrefixVersion(prefix, wantVersion string, logf func(string, ...any)) error {
	markerPath := filepath.Join(prefix, PrefixMarkerFile)
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

// PrefixMarkerFile records which Proton build created a prefix, so a
// version bump can be detected and the prefix moved aside and rebuilt.
const PrefixMarkerFile = ".created-by-proton"

func writePrefixMarker(prefix, version string, chownPath func(string) error) error {
	path := filepath.Join(prefix, PrefixMarkerFile)
	if err := os.WriteFile(path, []byte(version), 0644); err != nil {
		return err
	}
	// prefix 归运行时用户，别在里面留 root 属主的文件——同 vcredist 的安装标记。
	return chownPath(path)
}

// PrefixMarker reads PrefixMarkerFile, "" when it isn't there.
func PrefixMarker(prefix string) string {
	b, err := os.ReadFile(filepath.Join(prefix, PrefixMarkerFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// WaitForWineserverDrain polls (up to 90s, matching the reference script)
// for no wineserver process to still be holding prefix open, so a
// caller-visible "runtime ready" doesn't race a prefix that's still being
// written to.
func WaitForWineserverDrain(prefix string) {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if !WineserverHoldsPrefix(prefix) {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// WineserverHoldsPrefix reports whether a wineserver process is still
// serving prefix.
//
// This asks each wineserver process's environment for WINEPREFIX rather than
// matching against the *command line* — wineserver is launched as a bare
// path and learns its prefix from the environment, so a command-line match
// always returns an empty set.
//
// The value umu actually exports is "<prefix>/pfx/" — one level deeper than
// the configured prefix, with a trailing slash — so this asks "is that value
// inside prefix" rather than comparing for equality; see PrefixValueUnder,
// which must stay a **path**-boundary test, not a string-prefix test:
// prefixes are name-stem siblings ("umu-prefix", "umu-prefix-<instance>"),
// so a plain strings.HasPrefix would report the shared prefix as held
// whenever any per-instance one is.
func WineserverHoldsPrefix(prefix string) bool {
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
			if PrefixValueUnder(v, want) {
				return true
			}
		}
	}
	return false
}

// PrefixValueUnder reports whether a live wineserver's WINEPREFIX value
// refers to the prefix directory `prefix` — either that directory itself or
// something inside it.
//
// The comparison has to be on **path** boundaries, not on string boundaries:
// prefixes are name-stem siblings, so a plain strings.HasPrefix answers "is
// the shared prefix in use?" with "yes" whenever *any* per-instance prefix
// is in use.
//
// Both sides are compared as-written: symlinks are not resolved (another
// process's view cannot be resolved reliably) and neither side is made
// absolute, which is fine because both come from the same layout code.
func PrefixValueUnder(value, prefix string) bool {
	v := strings.TrimRight(value, "/")
	want := strings.TrimRight(prefix, "/")
	if v == "" || want == "" {
		return false
	}
	return v == want || strings.HasPrefix(v, want+"/")
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

// InheritedEnv is os.Environ() filtered down to the variables a launched
// Wine/umu process has any business seeing.
//
// It is a whitelist on purpose. The child is normally re-credentialed to a
// dedicated non-root user, while the host process is often started from a
// root login shell — and such a shell exports a pile of variables naming
// root-private sockets under /run/user/0. pressure-vessel dutifully tries to
// bind whatever they name into the container, so a single leaked variable
// kills the launch before Wine ever starts:
//
//	bwrap: Can't find source path /run/user/0/bus: Permission denied
//
// That one came from DBUS_SESSION_BUS_ADDRESS. A denylist cannot win this
// game — XDG_* was already being stripped (see RuntimeEnv) and D-Bus still
// got through, costing an entire evening of "setup says it succeeded but
// nothing works".
func InheritedEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		if k, _, ok := strings.Cut(kv, "="); ok && launchEnvAllowed(k) {
			out = append(out, kv)
		}
	}
	return out
}

func launchEnvAllowed(key string) bool {
	switch key {
	// HOME/USER/LOGNAME are rewritten by RuntimeEnv when dropping
	// privileges, but must survive when we aren't (umu keeps its runtime
	// cache under HOME).
	case "PATH", "TERM", "TZ", "HOME", "USER", "LOGNAME":
		return true
	case "LANG":
		return true
	}
	switch {
	case strings.HasPrefix(key, "LC_"):
		return true
	// umu-launcher downloads the Steam Linux Runtime with its own HTTP
	// client (urllib3), which honours these and nothing else — a
	// configured HTTP proxy elsewhere does not reach it.
	case strings.HasSuffix(key, "_PROXY"), strings.HasSuffix(key, "_proxy"):
		return true
	// Deliberate operator tuning of the Wine/Proton/umu stack (UMU_LOG,
	// PROTON_LOG, WINEDEBUG, ...). The ones the caller sets itself are
	// appended after this and win, since exec keeps the last occurrence of
	// a key.
	case strings.HasPrefix(key, "UMU_"), strings.HasPrefix(key, "PROTON_"), strings.HasPrefix(key, "WINE"):
		return true
	}
	return false
}

// RuntimeEnv rewrites HOME/USER/LOGNAME to the dropped user and strips
// root-inherited XDG_* so the child's runtime cache lands under the right
// home. Duplicated from pkg/sysuser.Env rather than imported: this package
// must not depend on sysuser (Config's callbacks are its only coupling to
// drop-privileges at all), and the transform is three lines.
func RuntimeEnv(base []string, home, userName string) []string {
	if home == "" {
		return base
	}
	out := make([]string, 0, len(base)+3)
	for _, kv := range base {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		switch {
		case k == "HOME", k == "USER", k == "LOGNAME":
			continue
		case strings.HasPrefix(k, "XDG_"):
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "HOME="+home)
	if userName != "" {
		out = append(out, "USER="+userName, "LOGNAME="+userName)
	}
	return out
}

// GamePath translates a host path to the Windows path Wine sees: it maps
// its Z: drive to /, so /home/x/asa becomes Z:\home\x\asa on a launched
// exe's command line.
func GamePath(hostPath string) string {
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		abs = hostPath
	}
	return "Z:" + strings.ReplaceAll(abs, "/", `\`)
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
// small metadata request). Uses download.Client() so it still honors a
// configured HTTP proxy fallback.
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
// rate-limited, unlike the GitHub API) and extracts the hash for assetName.
// The file's format is the standard `sha512sum` tool output:
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
