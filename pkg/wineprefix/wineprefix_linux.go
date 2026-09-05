//go:build linux

package wineprefix

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"asa-server/pkg/logger"
	"asa-server/pkg/umu"
)

// Manager owns Wine prefix directory layout and lifecycle for one BaseDir.
// Config is held behind an atomic pointer, refreshed on every call —
// matching the pattern used by asa-server/pkg/xvfb.Manager and
// asa-server/pkg/umu.Runtime.
type Manager struct {
	cfg atomic.Pointer[Config]
	umu *umu.Runtime

	// prefixLocks serializes work on one prefix without serializing work on
	// different ones. creationSlots caps how many first-time prefix
	// creations run at once: each one spawns a full pressure-vessel
	// container running wineboot, and starting eight instances at once on a
	// small VPS would otherwise fire eight of them simultaneously and look
	// exactly like a hang. Two keeps the parallelism that motivated the
	// per-prefix locks while bounding the burst — only *creation* takes a
	// slot, the common path (prefix already there) never touches this.
	prefixLocks   sync.Map // prefix path -> *sync.Mutex
	creationSlots chan struct{}

	// remounted is unused here; overlay mounts don't touch SocketDir-style
	// remount state — kept out on purpose, unlike xvfb.
}

// New returns a Manager for cfg, using umuRT to warm prefixes and detect
// live wineserver processes.
func New(cfg Config, umuRT *umu.Runtime) *Manager {
	m := &Manager{umu: umuRT, creationSlots: make(chan struct{}, 2)}
	m.cfg.Store(&cfg)
	return m
}

// Reconfigure updates the live Config.
func (m *Manager) Reconfigure(cfg Config) {
	m.cfg.Store(&cfg)
}

func (m *Manager) config() Config {
	if c := m.cfg.Load(); c != nil {
		return *c
	}
	return Config{}
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

// KeyFor maps a caller-supplied name to the key Dir/EnsurePrefix/Remove
// should use. Both isolating modes key the prefix by name; only "shared"
// (and anything unrecognized) collapses every name onto one.
func (m *Manager) KeyFor(name string) string {
	switch m.config().PrefixMode {
	case "per-instance", "overlay":
		return name
	}
	return ""
}

// Dir resolves the Wine prefix directory for key ("" is the shared prefix,
// regardless of PrefixMode).
//
// This is also where "does the whole program serialize launches" is
// decided: SharesPrefix asks this method whether two different keys land in
// the same directory, rather than repeating the mode table. Anything
// unrecognized therefore falls through to the shared prefix on purpose.
func (m *Manager) Dir(key string) string {
	cfg := m.config()
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
		// wineserver. PrefixDir moves the lower only.
		return overlayMergedDir(cfg, key)
	}
	return base
}

// instanceDir is Dir's per-instance branch with the mode check removed:
// "where would this key's own prefix live". Cleanup and status need it
// regardless of the mode in force right now, because a prefix created
// during a past stint in per-instance mode outlives the switch back.
func (m *Manager) instanceDir(cfg Config, key string) string {
	if key == "" {
		return m.Dir("")
	}
	return m.Dir("") + "-" + key
}

// SharesPrefix reports whether two different keys would land in the same
// prefix directory.
//
// Derived from Dir rather than a hand-written mode check, so drift is
// impossible: whatever Dir decides IS the sharing model. The failure
// direction is also the safe one — an unconfigured Config, or an
// unrecognized mode, falls through Dir to the one shared prefix, so this
// returns true and the caller gets to serialize/exclude accordingly.
// Over-serializing costs time; under-serializing costs a multi-minute hang
// and an orphaned process tree.
func (m *Manager) SharesPrefix() bool {
	return m.Dir("instance-a") == m.Dir("instance-b")
}

func (m *Manager) lockPrefix(path string) func() {
	v, _ := m.prefixLocks.LoadOrStore(path, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// CheckSharedReady reports whether the shared prefix has been warmed at
// least once, with no network access. Error text is end-user facing.
func (m *Manager) CheckSharedReady() error {
	prefix := m.Dir("")
	if _, err := os.Stat(filepath.Join(prefix, "system.reg")); err != nil {
		return fmt.Errorf("Wine 前缀尚未初始化：%s。请运行 asa-server setup 完成环境准备", prefix)
	}
	return nil
}

// EnsurePrefix makes sure the Wine prefix identified by key exists and is
// usable, creating it if this is the first launch under prefix_mode
// "per-instance" (or the first-ever writable layer under "overlay").
//
// It never downloads umu/GE-Proton/the Steam Linux Runtime — those are
// global, shared, and remain the caller's EnsureRuntime job; a missing one
// is reported as "run asa-server setup" rather than silently fetched on a
// start path. An empty key therefore only verifies the shared prefix, it
// never rebuilds it.
func (m *Manager) EnsurePrefix(ctx context.Context, key string, progress io.Writer) error {
	cfg := m.config()

	// The shared prefix belongs to EnsureRuntime: created during setup,
	// alongside the umu/GE-Proton downloads it depends on. Verifying is in
	// scope here; rebuilding it behind a server start is not.
	if key == "" || (cfg.PrefixMode != "per-instance" && cfg.PrefixMode != "overlay") {
		if err := m.umu.CheckRuntime(); err != nil {
			return err
		}
		return m.CheckSharedReady()
	}

	// umu-run + GE-Proton + the shared prefix are the preconditions for
	// building any further prefix, and none of them are this method's to
	// install.
	if err := m.umu.CheckRuntime(); err != nil {
		return err
	}
	if err := m.CheckSharedReady(); err != nil {
		return err
	}

	logf := progressLogger(progress)

	// overlay builds its private layer on top of that same shared prefix
	// instead of running a wineboot of its own — milliseconds instead of a
	// minute, and no second copy of the runtime on disk.
	if cfg.PrefixMode == "overlay" {
		return m.ensureOverlayPrefix(ctx, cfg, key, logf)
	}

	prefix := m.instanceDir(cfg, key)
	unlock := m.lockPrefix(prefix)
	defer unlock()

	// Fast path, and the reason this is cheap to call on every start.
	if umu.PrefixInitialized(prefix) && umu.PrefixMarker(prefix) == cfg.ProtonVersion {
		// ...但 VC++ 的 DLL override 要单独过一眼。装它的那一步（warmPrefix 之后）
		// 只在**新建 prefix** 时跑，所以任何比那段代码更早创建的 per-instance
		// prefix 会永远停在没有 override 的状态：闸门放行、实例起来、ArkApi 加载
		// 不了，而且每次启动都只有一条「没检测到 VC++ 运行时」的告警。
		//
		// 判据用 override 而不是「有没有原生 DLL」：安装器在无头机上装不上，
		// 用它当判据会让每次启动都重跑一遍 regedit 容器。
		if !cfg.hasVCRedistOverrides(prefix) {
			if cfg.EnsureVCRedist != nil {
				if err := cfg.EnsureVCRedist(ctx, key, logf); err != nil {
					logf("实例 %s 的 Wine 前缀里补装 VC++ 运行时失败（%v）；不使用 ArkApi 可忽略", key, err)
				}
			}
		}
		return nil
	}

	select {
	case m.creationSlots <- struct{}{}:
		defer func() { <-m.creationSlots }()
	case <-ctx.Done():
		return fmt.Errorf("等待 Wine 前缀创建槽位时被取消: %w", ctx.Err())
	}

	logf("实例 %s 使用独立 Wine 前缀（prefix_mode=per-instance），正在创建 %s；"+
		"首次创建约需一分钟，之后每次启动都是秒过", key, prefix)

	// WarmPrefix does the whole job for one prefix: version reconcile,
	// wineboot --init, chown to the runtime user, wineserver drain, and the
	// .created-by-proton marker. prefetched=false because the Steam Linux
	// Runtime is global and was already fetched for the shared prefix.
	if err := m.umu.WarmPrefix(ctx, prefix, logf, false); err != nil {
		return fmt.Errorf("创建实例 %s 的 Wine 前缀失败: %w", key, err)
	}

	// Same rule as EnsureRuntime: ArkApi is optional, so a failed VC++
	// install must not block a start — but it has to be loud, because the
	// people who need it have no other way to find out.
	if cfg.EnsureVCRedist != nil {
		if err := cfg.EnsureVCRedist(ctx, key, logf); err != nil {
			logf("实例 %s 的 Wine 前缀里安装 VC++ 运行时失败（%v）；不使用 ArkApi 可忽略", key, err)
		}
	}
	return nil
}

// Remove deletes everything key owns, in both shapes (a past per-instance
// prefix and a past overlay writable layer — mode-independent on purpose,
// since an instance that ran under both modes has left one of each).
func (m *Manager) Remove(key string) error {
	if key == "" {
		return nil
	}
	cfg := m.config()

	if err := m.removeOverlayPrefix(cfg, key); err != nil {
		return err
	}

	prefix := m.instanceDir(cfg, key)
	if prefix == m.Dir("") {
		// Belt and braces: never let a bad key delete the shared prefix.
		return nil
	}
	if !dirExists(prefix) {
		return nil
	}

	unlock := m.lockPrefix(prefix)
	defer unlock()

	if umu.WineserverHoldsPrefix(prefix) {
		return fmt.Errorf("Wine 前缀 %s 仍被 wineserver 占用，请先停止实例 %s 再删除", prefix, key)
	}
	return os.RemoveAll(prefix)
}

// Status lists every Wine prefix directory under BaseDir — the shared one
// plus any per-instance ones and overlay writable layers. Read-only and
// offline.
func (m *Manager) Status() []Info {
	cfg := m.config()
	shared := m.Dir("")

	paths := []string{shared}
	// Two shapes to find, and they need two patterns:
	//   "<shared>-<key>"        per-instance prefixes
	//   "<shared>.bak-<版本>"   what a Proton bump moves aside — a full
	//                           prefix that nothing will ever open again
	// "<shared>-*" also catches "umu-prefix-overlay", which is NOT a prefix
	// but the directory holding every instance's writable layer.
	overlays := overlayRoot(cfg)
	for _, pattern := range []string{shared + "-*", shared + ".bak-*"} {
		matches, _ := filepath.Glob(pattern)
		for _, p := range matches {
			if p == overlays {
				continue
			}
			paths = append(paths, p)
		}
	}

	out := make([]Info, 0, len(paths))
	for _, p := range paths {
		if !dirExists(p) {
			continue
		}
		// per-instance 前缀是 "<shared>-<key>"，版本备份是 "<shared>.bak-<版本>" ——
		// 两种后缀都要剥掉。只剥 "-" 的话备份目录的 Key 会带上那个点。
		key := strings.TrimPrefix(p, shared)
		key = strings.TrimPrefix(key, "-")
		key = strings.TrimPrefix(key, ".")

		out = append(out, Info{
			Key:           key,
			Path:          p,
			Initialized:   umu.PrefixInitialized(p),
			ProtonVersion: umu.PrefixMarker(p),
			InUse:         umu.WineserverHoldsPrefix(p),
			SizeBytes:     dirSize(p),
			Current:       m.Dir(key) == p,
		})
	}
	return append(out, m.overlayStatus(cfg)...)
}

// overlayStatus reports one row per writable layer under umu-prefix-overlay/,
// independent of the mode currently configured (layers outlive a switch
// back to shared, exactly like per-instance prefixes do).
//
// SizeBytes is the **upper** layer, never the mount point: walking merged
// would count the shared lower once per instance.
func (m *Manager) overlayStatus(cfg Config) []Info {
	entries, err := os.ReadDir(overlayRoot(cfg))
	if err != nil {
		return nil
	}
	mounts := listOverlayMounts()

	var out []Info
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		key := e.Name()
		merged := overlayMergedDir(cfg, key)
		mounted := mounts[merged]

		// 独占占用：默认量 upper（底层是共享的，量 merged 会把它按实例数重复计）。
		// 只有**降级复制**形态例外——那时 merged 里是真的一整份拷贝，upper 是空的。
		seeded := !mounted && umu.PrefixInitialized(merged)
		measured := overlayUpperDir(cfg, key)
		if seeded {
			measured = merged
		}

		out = append(out, Info{
			Key:           key,
			Path:          merged,
			Initialized:   umu.PrefixInitialized(merged),
			ProtonVersion: readOverlayStamp(cfg, key),
			InUse:         umu.WineserverHoldsPrefix(merged),
			SizeBytes:     dirSize(measured),
			Overlay:       true,
			Mounted:       mounted,
			Current:       m.Dir(key) == merged,
		})
	}
	return out
}

func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // 报告用途，读不到就跳过
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// --- overlay mounting ---

// ensureOverlayPrefix makes prefix_mode "overlay" true for one key: the
// shared lower prefix stays untouched and read-only, and this key gets a
// private writable layer stacked on it, mounted at
// {BaseDir}/umu-prefix-overlay/<key>/merged.
//
// That mount point is a directory on a *new* overlay superblock, so its
// dev/ino differ from the lower's and from every other key's — which is
// the entire point, because Wine picks its wineserver socket directory from
// stat() of WINEPREFIX. Different inode, different wineserver, so instances
// get the isolation of per-instance mode at roughly shared mode's disk and
// startup cost.
func (m *Manager) ensureOverlayPrefix(ctx context.Context, cfg Config, key string, logf func(string, ...any)) error {
	if key == "" {
		return nil // the lower itself; EnsureRuntime owns it
	}

	lower := m.Dir("")
	if !umu.PrefixInitialized(lower) {
		return fmt.Errorf("Wine 前缀尚未初始化：%s。请运行 asa-server setup 完成环境准备", lower)
	}

	instDir := overlayInstanceDir(cfg, key)
	unlock := m.lockPrefix(instDir)
	defer unlock()

	merged := overlayMergedDir(cfg, key)
	want := umu.PrefixMarker(lower)

	mounted := overlayMounted(merged)
	// Not mounted but a usable prefix on disk = the copy fallback ran on an
	// earlier start. Both shapes are valid, and both live at the same path.
	seeded := !mounted && umu.PrefixInitialized(merged)

	if (mounted || seeded) && readOverlayStamp(cfg, key) == want {
		return fixPfxSymlink(merged, cfg.chownPath, logf)
	}

	if mounted || seeded {
		logf("实例 %s 的 Wine 可写层是基于旧的底层前缀建立的（记录 %q，当前 %q），正在重建",
			key, orUnknown(readOverlayStamp(cfg, key)), orUnknown(want))
	}
	if mounted {
		if err := unmountOverlay(merged); err != nil {
			return fmt.Errorf("卸载实例 %s 的旧 Wine 可写层失败: %w", key, err)
		}
	}

	// Nothing here is user data — saves live elsewhere — so a rebuild is a
	// plain wipe.
	work := overlayWorkDir(cfg, key)
	for _, d := range []string{overlayUpperDir(cfg, key), work, merged} {
		if err := os.RemoveAll(d); err != nil {
			return fmt.Errorf("清理实例 %s 的 Wine 可写层失败: %w", key, err)
		}
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("创建 %s 失败: %w", d, err)
		}
		if d == work {
			// workdir 是内核的私有暂存区，不是我们的目录：挂载时它会在里面建一个
			// root 所有、mode 000 的 work/work，userspace 不该碰其中任何东西。
			continue
		}
		// 独占目录语义：其余部分归降权运行时用户。
		if err := cfg.chownPath(d); err != nil {
			return fmt.Errorf("把 %s 交给运行时用户失败: %w", d, err)
		}
	}
	if err := cfg.chownPath(instDir); err != nil {
		return fmt.Errorf("把 %s 交给运行时用户失败: %w", instDir, err)
	}

	if err := mountOverlay(cfg, key, logf); err != nil {
		// Degrade rather than refuse to start: a kernel upgrade or a move
		// to a filesystem that can't hold an upperdir would otherwise take
		// every instance down. The copy costs disk and a few seconds of
		// I/O and is functionally identical.
		logf("实例 %s 无法使用 overlayfs（%v）；改为从底层前缀复制一份可写层 —— "+
			"功能相同，只是多占磁盘。要恢复省盘效果，请解决上面的原因后重启", key, err)
		if err := seedFromLower(ctx, m.creationSlots, lower, merged, cfg.chownPath, logf); err != nil {
			return fmt.Errorf("为实例 %s 准备 Wine 前缀失败: %w", key, err)
		}
	}

	if err := fixPfxSymlink(merged, cfg.chownPath, logf); err != nil {
		return err
	}
	return writeOverlayStamp(cfg, key, want, cfg.chownPath)
}

func mountOverlay(cfg Config, key string, logf func(string, ...any)) error {
	lower := filepath.Join(cfg.BaseDir, "umu-prefix")
	if cfg.PrefixDir != "" {
		lower = cfg.PrefixDir
	}
	upper := overlayUpperDir(cfg, key)
	work := overlayWorkDir(cfg, key)
	merged := overlayMergedDir(cfg, key)

	if !mountOptionsSafe(lower, upper, work) {
		return fmt.Errorf("前缀路径里含有 overlayfs 挂载参数无法表达的字符（逗号、冒号或反斜杠）：lower=%s upper=%s", lower, upper)
	}

	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lower, upper, work)
	if err := syscall.Mount("overlay", merged, "overlay", 0, opts); err != nil {
		return translateMountError(err, upper)
	}
	logf("实例 %s 的 Wine 前缀已挂载：%s（共享底层 %s + 私有可写层）", key, merged, lower)
	return nil
}

// translateMountError turns the three mount(2) errnos that actually happen
// here into something an operator can act on. Everything else is passed
// through.
func translateMountError(err error, upper string) error {
	switch {
	case os.IsPermission(err) || err == syscall.EPERM:
		return fmt.Errorf("挂载 overlayfs 需要 root 权限（%w）", err)
	case err == syscall.ENODEV:
		return fmt.Errorf("当前内核没有 overlay 文件系统支持（%w）；/proc/filesystems 里应当有一行 \"nodev\\toverlay\"", err)
	case err == syscall.EINVAL:
		return fmt.Errorf("overlayfs 拒绝了这个可写层位置（%w）；%s 所在的文件系统可能不支持作为 upperdir —— "+
			"xfs 需要 ftype=1（xfs_info 可查），NFS 与「已经是 overlay 的目录」都不行", err, upper)
	}
	return err
}

// seedFromLower is the fallback when overlayfs is unavailable: copy the
// lower prefix into the same merged path, so the launch path sees an
// ordinary directory at the location it already expects.
//
// Shelling out to `cp -a` rather than walking the tree in Go is deliberate.
// A Wine prefix is full of symlinks, and one of them is dosdevices/z: -> / —
// a copier that follows symlinks would try to copy the entire filesystem
// into the prefix. cp -a is the well-tested thing that gets links, modes,
// xattrs and ownership right in one call.
func seedFromLower(ctx context.Context, slots chan struct{}, lower, merged string, chownPath func(string) error, logf func(string, ...any)) error {
	// One creation at a time, same reasoning as per-instance wineboot.
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return fmt.Errorf("等待前缀创建槽位时被取消: %w", ctx.Err())
	}

	logf("正在从 %s 复制 Wine 前缀到 %s（首次需要几秒到几十秒）", lower, merged)
	// "<lower>/." copies the directory's *contents*, dotfiles included, into
	// the already-existing merged dir.
	cmd := exec.CommandContext(ctx, "cp", "-a", lower+"/.", merged+"/")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cp -a %s: %w: %s", lower, err, strings.TrimSpace(string(out)))
	}
	// cp -a preserves ownership (running as root and the lower already
	// belongs to the runtime user), so only the merged dir itself — created
	// by us — needs handing over.
	return chownPath(merged)
}

// fixPfxSymlink points <merged>/pfx at merged itself.
//
// umu exports WINEPREFIX as "<prefix>/pfx/", and creates that pfx entry as a
// symlink to an **absolute, resolved** path. The lower's copy therefore
// points at the lower — so until something rewrites it, WINEPREFIX resolves
// back to the shared prefix and every instance lands on one wineserver
// again. Mount succeeds, the directory looks right, nothing is logged.
//
// umu is expected to rewrite it on each launch, which would make this
// redundant. "Expected" is not "observed", so the symlink is fixed up here
// unconditionally. It is idempotent and costs one lstat on the common path.
func fixPfxSymlink(merged string, chownPath func(string) error, logf func(string, ...any)) error {
	p := filepath.Join(merged, "pfx")

	fi, err := os.Lstat(p)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink == 0:
		// A real directory here would mean a prefix laid out the way
		// Proton does it, not the way umu does it. Deleting it would
		// destroy the prefix, so don't.
		logf("警告：%s 是真实目录而不是软链，跳过 pfx 修正 —— 这不是 umu 的前缀布局，请检查", p)
		return nil
	case err == nil:
		// 判据是「它解析出来是不是 merged」，不是「字面等不等于 merged」——
		// umu 在启动时会把这条链重写成相对形式 "."（同样解析到 merged），
		// 字面比对会判定不符、每次启动都删掉重建，在 upper 里白白制造一次改动。
		if pointsAt(p, merged) {
			return nil
		}
		if rerr := os.Remove(p); rerr != nil {
			return fmt.Errorf("移除指向底层的 pfx 软链失败: %w", rerr)
		}
	case !os.IsNotExist(err):
		return fmt.Errorf("检查 %s 失败: %w", p, err)
	}

	if err := os.Symlink(merged, p); err != nil {
		return fmt.Errorf("重建 pfx 软链失败: %w", err)
	}
	return chownPath(p)
}

// pointsAt reports whether path (after following symlinks) is the same
// directory as want.
func pointsAt(path, want string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	wfi, err := os.Stat(want)
	if err != nil {
		return false
	}
	return os.SameFile(fi, wfi)
}

// unmountOverlay unmounts path; not being a mount point at all is success.
func unmountOverlay(path string) error {
	err := syscall.Unmount(path, 0)
	switch err {
	case nil, syscall.EINVAL, syscall.ENOENT:
		// EINVAL from umount(2) means "not a mount point".
		return nil
	}
	return err
}

func overlayMounted(path string) bool { return listOverlayMounts()[path] }

func listOverlayMounts() map[string]bool {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return map[string]bool{}
	}
	return parseOverlayMounts(string(b))
}

// overlayKeysMounted lists the keys whose writable layer is mounted right
// now. Used to refuse operations that would write the lower while overlays
// reference it — modifying a lowerdir under a live mount is explicitly
// undefined behaviour.
func overlayKeysMounted(cfg Config) []string {
	root := overlayRoot(cfg)
	var keys []string
	for mp := range listOverlayMounts() {
		if key, ok := overlayKeyFromMerged(root, mp); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

// OverlayRoot is the directory holding every key's overlay writable layer
// (not a prefix itself — a caller enumerating "<shared>-*" siblings must
// exclude this path explicitly, since it matches that glob too).
func (m *Manager) OverlayRoot() string { return overlayRoot(m.config()) }

// UnmountedOverlayDirs lists each overlay writable layer that is NOT
// currently mounted — i.e. the copy-fallback shape, an ordinary directory
// of real files that needs the same ownership accounting as any other
// independent directory.
//
// A **mounted** layer must never appear here: chown is a metadata write,
// and a metadata write through an overlay mount copies the file up,
// silently duplicating the entire shared lower into that layer on every
// caller that walks this list.
func (m *Manager) UnmountedOverlayDirs() []string {
	cfg := m.config()
	entries, err := os.ReadDir(overlayRoot(cfg))
	if err != nil {
		return nil
	}
	mounts := listOverlayMounts()

	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if merged := overlayMergedDir(cfg, e.Name()); !mounts[merged] {
			out = append(out, merged)
		}
	}
	return out
}

// Reconcile cleans up prefix state a crash could have left behind. Cheap
// (one /proc read plus a stat per layer), read-only unless something is
// actually broken.
//
// Deliberately narrow: it does NOT unmount layers whose instance simply
// isn't running — overlay mounts live in the host mount namespace and are
// meant to survive restarts, so "mounted but idle" is the normal resting
// state, not garbage. Only a mount whose upper layer is gone (impossible
// combination — crash leftover) gets unmounted.
func (m *Manager) Reconcile() {
	cfg := m.config()
	root := overlayRoot(cfg)
	for mp := range listOverlayMounts() {
		key, ok := overlayKeyFromMerged(root, mp)
		if !ok {
			continue
		}
		if dirExists(overlayUpperDir(cfg, key)) {
			continue
		}
		logger.Warnf("Wine 可写层 %s 仍挂载着，但它的 upper 目录已经不存在（崩溃残留），正在卸载", mp)
		if err := unmountOverlay(mp); err != nil {
			logger.Warnf("卸载残留的 Wine 可写层 %s 失败：%v", mp, err)
		}
	}
}

// removeOverlayPrefix unmounts and deletes one key's writable layer.
func (m *Manager) removeOverlayPrefix(cfg Config, key string) error {
	instDir := overlayInstanceDir(cfg, key)
	if !dirExists(instDir) {
		return nil
	}
	unlock := m.lockPrefix(instDir)
	defer unlock()

	merged := overlayMergedDir(cfg, key)
	if umu.WineserverHoldsPrefix(merged) {
		return fmt.Errorf("Wine 前缀 %s 仍被 wineserver 占用，请先停止实例 %s 再删除", merged, key)
	}
	if err := unmountOverlay(merged); err != nil {
		return fmt.Errorf("卸载 %s 失败: %w", merged, err)
	}
	return os.RemoveAll(instDir)
}

func readOverlayStamp(cfg Config, key string) string {
	b, err := os.ReadFile(overlayStampPath(cfg, key))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeOverlayStamp(cfg Config, key, version string, chownPath func(string) error) error {
	p := overlayStampPath(cfg, key)
	if err := os.WriteFile(p, []byte(version), 0644); err != nil {
		return err
	}
	// Same trap the prefix marker documents: a root-owned file inside a
	// tree that belongs to the runtime user is what ownership-drift
	// sampling trips over.
	return chownPath(p)
}

func orUnknown(s string) string {
	if s == "" {
		return "未知"
	}
	return s
}

// LowerNeedsWork reports whether the shared lower prefix still has
// something to change, as opposed to just re-verifying it.
//
// The distinction only matters under prefix_mode "overlay", and it matters
// a lot: EnsureRuntime runs in the background on every API server start,
// while overlay mounts deliberately survive restarts. Refusing outright
// whenever a mount exists would break every restart after the first
// instance ever started; doing the work anyway would modify a lowerdir
// that live mounts reference, which overlayfs documents as undefined
// behaviour. So the guard asks whether there is any work at all, and only
// then refuses.
func (m *Manager) LowerNeedsWork() bool {
	cfg := m.config()
	lower := m.Dir("")
	switch {
	case !umu.PrefixInitialized(lower):
		return true
	case umu.PrefixMarker(lower) != cfg.ProtonVersion:
		return true // a version bump would move it aside and rebuild
	case !m.umu.SteamLinuxRuntimeReady():
		return true // WarmPrefix would run wineboot again
	case cfg.Runtime == "umu" && cfg.InstallVCRedist && !cfg.hasVCRedistOverrides(lower):
		return true // EnsureVCRedist would write the prefix registry
	}
	return false
}

// PrepareSharedWrite clears the way for an operation that writes the shared
// lower prefix: it unmounts every writable layer that nothing is using, and
// refuses when one is still live.
//
// Unmounting rather than merely refusing is what keeps this from
// deadlocking. Layers stay mounted after their instance stops — that's
// deliberate — and it means "stop the instances" would NOT clear the
// mounts. A guard that only refused would therefore be unclearable by any
// action the operator can take: the very first VC++ reinstall or Proton
// bump after one instance had ever started would be permanently blocked.
//
// Unmounting an idle layer loses nothing. The upper directory stays on
// disk, and the next start remounts it — and if the lower did change
// underneath, the .lower-stamp mismatch rebuilds it, which is exactly what
// should happen.
//
// op names the operation for the log; the returned closure marks the end of
// the window in which the shared prefix may be written (defer is the
// natural shape). Never nil when err is nil, and a no-op outside prefix_mode
// "overlay".
func (m *Manager) PrepareSharedWrite(op string) (func(), error) {
	cfg := m.config()

	var live, freed []string
	for _, key := range overlayKeysMounted(cfg) {
		merged := overlayMergedDir(cfg, key)
		if umu.WineserverHoldsPrefix(merged) {
			live = append(live, key)
			continue
		}
		if err := unmountOverlay(merged); err != nil {
			// Can't unmount and can't prove it's idle: treat as live
			// rather than writing the lower anyway.
			logger.Warnf("卸载空闲的 Wine 可写层 %s 失败：%v", merged, err)
			live = append(live, key)
			continue
		}
		freed = append(freed, key)
	}

	if len(freed) > 0 {
		sort.Strings(freed)
		logger.Infof("为修改共享 Wine 前缀，已卸载 %d 个空闲的可写层（%s）；下次启动这些实例时会自动重新挂载",
			len(freed), strings.Join(freed, "、"))
	}
	if len(live) == 0 {
		return openLowerWriteWindow(cfg, op), nil
	}

	sort.Strings(live)
	return nil, fmt.Errorf("底层 Wine 前缀 %s 现在不能被修改：实例 %s 还在运行，它们的可写层正挂在它上面"+
		"（prefix_mode=overlay）。修改被挂载引用的 lowerdir 是 overlayfs 明确的未定义行为，"+
		"请先停止这些实例后重试",
		m.Dir(""), strings.Join(live, "、"))
}

// openLowerWriteWindow marks the span during which the shared lower prefix
// may be modified, and returns the closer.
func openLowerWriteWindow(cfg Config, op string) func() {
	if cfg.PrefixMode != "overlay" {
		return func() {}
	}

	lower := cfg.PrefixDir
	if lower == "" {
		lower = filepath.Join(cfg.BaseDir, "umu-prefix")
	}
	start := time.Now()
	logger.Infof("共享 Wine 前缀 %s 的修改窗口已打开（%s）；此刻起到窗口关闭之间启动的实例，"+
		"其可写层会把一个正在被修改的底层当 lowerdir —— 实例事后出现莫名其妙的症状时，"+
		"先拿它的「已挂载」时刻和这一对时间戳对一下", lower, op)
	return func() {
		logger.Infof("共享 Wine 前缀的修改窗口已关闭（%s），持续 %s", op, time.Since(start).Round(time.Millisecond))
	}
}
