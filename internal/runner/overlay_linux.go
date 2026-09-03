//go:build linux

package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"asa-server/pkg/logger"
)

// ensureOverlayPrefix makes prefix_mode "overlay" true for one instance: the
// shared lower prefix stays untouched and read-only, and this instance gets a
// private writable layer stacked on it, mounted at
// {BaseDir}/umu-prefix-overlay/<key>/merged.
//
// That mount point is a directory on a *new* overlay superblock, so its
// dev/ino differ from the lower's and from every other instance's — which is
// the entire point, because Wine picks its wineserver socket directory from
// stat() of WINEPREFIX (docs/UMU_PREFIX_OVERLAY_PLAN.md §2). Different inode,
// different wineserver, so instances get the isolation of per-instance mode at
// roughly shared mode's disk and startup cost.
//
// Idempotent and cheap on the common path: an already-mounted layer whose
// stamp still matches the lower costs a mountinfo read and two stats.
func ensureOverlayPrefix(ctx context.Context, cfg Config, key string, logf func(string, ...any)) error {
	if key == "" {
		return nil // the lower itself; EnsureRuntime owns it
	}

	lower := prefixDir(cfg, "")
	if !prefixInitialized(lower) {
		// Same wording checkRuntime uses — the fix is the same command.
		return fmt.Errorf("Wine 前缀尚未初始化：%s。请运行 asa-server setup 完成环境准备", lower)
	}

	instDir := overlayInstanceDir(cfg, key)
	unlock := lockPrefix(instDir)
	defer unlock()

	merged := overlayMergedDir(cfg, key)
	want := prefixMarker(lower)

	mounted := overlayMounted(merged)
	// Not mounted but a usable prefix on disk = the copy fallback ran on an
	// earlier start (§6.3). Both shapes are valid, and both live at the same
	// path, so everything below treats them the same.
	seeded := !mounted && prefixInitialized(merged)

	if (mounted || seeded) && readOverlayStamp(cfg, key) == want {
		return fixPfxSymlink(merged, logf)
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

	// Nothing here is user data — saves live in instances/<name>/Save and
	// plugin data in the mirror — so a rebuild is a plain wipe.
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
			// 把它交给运行时用户既没意义，也会让属主检查跟着走进去 —— 那正是
			// 第一次上真机时挡住启动的东西（见 overlayRWSubtrees）。
			continue
		}
		// 独占目录语义：其余部分归降权运行时用户
		// （docs/ACL_PERMISSION_HARDENING_PLAN.md）。
		if err := chownPathForRuntime(d); err != nil {
			return fmt.Errorf("把 %s 交给运行时用户失败: %w", d, err)
		}
	}
	if err := chownPathForRuntime(instDir); err != nil {
		return fmt.Errorf("把 %s 交给运行时用户失败: %w", instDir, err)
	}

	if err := mountOverlay(cfg, key, logf); err != nil {
		// Degrade rather than refuse to start: a kernel upgrade or a move to
		// a filesystem that can't hold an upperdir would otherwise take every
		// instance down. The copy costs disk and a few seconds of I/O and is
		// functionally identical — same path, same isolation, same wineserver
		// story. Loud, because nobody would otherwise find out they stopped
		// getting the mode they configured. See §6.3 option b.
		logf("实例 %s 无法使用 overlayfs（%v）；改为从底层前缀复制一份可写层 —— "+
			"功能相同，只是多占磁盘。要恢复省盘效果，请解决上面的原因后重启 asa-server", key, err)
		if err := seedFromLower(ctx, cfg, key, logf); err != nil {
			return fmt.Errorf("为实例 %s 准备 Wine 前缀失败: %w", key, err)
		}
	}

	if err := fixPfxSymlink(merged, logf); err != nil {
		return err
	}
	return writeOverlayStamp(cfg, key, want)
}

// mountOverlay stacks key's private writable layer on the shared lower prefix.
func mountOverlay(cfg Config, key string, logf func(string, ...any)) error {
	lower := prefixDir(cfg, "")
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
// through — a made-up explanation for an unfamiliar errno would be worse than
// the raw one. See docs/UMU_PREFIX_OVERLAY_PLAN.md §12.6.
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

// seedFromLower is the fallback when overlayfs is unavailable: copy the lower
// prefix into the same merged path, so the launch path sees an ordinary
// directory at the location it already expects.
//
// Shelling out to `cp -a` rather than walking the tree in Go is deliberate. A
// Wine prefix is full of symlinks, and one of them is dosdevices/z: -> / —
// a copier that follows symlinks would try to copy the entire filesystem into
// the prefix. cp -a is the well-tested thing that gets links, modes, xattrs
// and ownership right in one call, and it is present on every Linux that can
// run this program.
func seedFromLower(ctx context.Context, cfg Config, key string, logf func(string, ...any)) error {
	lower := prefixDir(cfg, "")
	merged := overlayMergedDir(cfg, key)

	// One creation at a time, same reasoning as per-instance wineboot: this is
	// a few hundred MB of I/O, and eight instances starting at once should not
	// turn into eight concurrent copies.
	select {
	case prefixCreationSlots <- struct{}{}:
		defer func() { <-prefixCreationSlots }()
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
	// cp -a preserves ownership (we run as root and the lower already belongs
	// to the runtime user), so only the merged dir itself — created by us —
	// needs handing over.
	return chownPathForRuntime(merged)
}

// fixPfxSymlink points <merged>/pfx at merged itself.
//
// umu exports WINEPREFIX as "<prefix>/pfx/", and creates that pfx entry as a
// symlink to an **absolute, resolved** path. The lower's copy therefore points
// at the lower — so until something rewrites it, WINEPREFIX resolves back to
// the shared prefix and every instance lands on one wineserver again. Mount
// succeeds, the directory looks right, nothing is logged: it is the one
// failure mode of this whole design that looks completely normal.
//
// umu is expected to rewrite it on each launch, which would make this
// redundant. "Expected" is not "observed", and this repo has been burned by
// exactly that distinction before, so the symlink is fixed up here
// unconditionally. It is idempotent and costs one lstat on the common path.
// See docs/UMU_PREFIX_OVERLAY_PLAN.md §12.1.
func fixPfxSymlink(merged string, logf func(string, ...any)) error {
	p := filepath.Join(merged, "pfx")

	fi, err := os.Lstat(p)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink == 0:
		// A real directory here would mean a prefix laid out the way Proton
		// does it (STEAM_COMPAT_DATA_PATH/pfx), not the way umu does it.
		// Deleting it would destroy the prefix, so don't.
		logf("警告：%s 是真实目录而不是软链，跳过 pfx 修正 —— 这不是 umu 的前缀布局，请检查", p)
		return nil
	case err == nil:
		// 判据是「它解析出来是不是 merged」，不是「字面等不等于 merged」——
		// 后者会跟 umu 打架：真机上 umu 在启动时把这条链重写成了相对形式 "."
		// （同样解析到 merged，完全正确），字面比对会判定不符、每次启动都删掉重建，
		// 在 upper 里白白制造一次改动。SameFile 问的正是 Wine 会问的那个问题：
		// stat(WINEPREFIX) 落在哪个 dev/ino 上。
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
	return chownPathForRuntime(p)
}

// pointsAt reports whether path (after following symlinks) is the same
// directory as want — absolute, relative and chained links all included.
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

// overlayMounted reports whether path is currently an overlay mount point.
func overlayMounted(path string) bool { return listOverlayMounts()[path] }

func listOverlayMounts() map[string]bool {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return map[string]bool{}
	}
	return parseOverlayMounts(string(b))
}

// overlayKeysMounted lists the instance keys whose writable layer is mounted
// right now. Used to refuse operations that would write the lower while
// overlays reference it — modifying a lowerdir under a live mount is
// explicitly undefined behaviour (docs/UMU_PREFIX_OVERLAY_PLAN.md §6.1/§12.4).
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

// reconcileOverlays cleans up after a crash: a mount whose upper layer is gone
// is a leftover nothing can use.
//
// Deliberately narrow. Overlay mounts live in the host mount namespace, so
// they survive an asa-server restart on purpose — "mounted but the instance
// isn't running" is the normal resting state (§3.3 keeps the mount when an
// instance stops), not something to clean up. Only the impossible combination
// gets unmounted. See docs/UMU_PREFIX_OVERLAY_PLAN.md §12.5.
func reconcilePrefixes() { reconcileOverlays(getConfig()) }

func reconcileOverlays(cfg Config) {
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

// removeOverlayPrefix unmounts and deletes one instance's writable layer.
func removeOverlayPrefix(cfg Config, key string) error {
	instDir := overlayInstanceDir(cfg, key)
	if !dirExists(instDir) {
		return nil
	}
	unlock := lockPrefix(instDir)
	defer unlock()

	merged := overlayMergedDir(cfg, key)
	if wineserverHoldsPrefix(merged) {
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

func writeOverlayStamp(cfg Config, key, version string) error {
	p := overlayStampPath(cfg, key)
	if err := os.WriteFile(p, []byte(version), 0644); err != nil {
		return err
	}
	// Same trap writePrefixMarker documents: a root-owned file inside a tree
	// that belongs to the runtime user is what the ownership-drift sampling
	// trips over.
	return chownPathForRuntime(p)
}

func orUnknown(s string) string {
	if s == "" {
		return "未知"
	}
	return s
}

// lowerNeedsWork reports whether EnsureRuntime still has something to change
// in the shared lower prefix, as opposed to just re-verifying it.
//
// The distinction only matters under prefix_mode "overlay", and it matters a
// lot: EnsureRuntime runs in the background on every API server start, while
// overlay mounts deliberately survive restarts. Refusing outright whenever a
// mount exists would break every restart after the first instance ever
// started; doing the work anyway would modify a lowerdir that live mounts
// reference, which overlayfs documents as undefined behaviour. So the guard
// asks whether there is any work at all, and only then refuses.
func lowerNeedsWork(cfg Config) bool {
	lower := prefixDir(cfg, "")
	switch {
	case !prefixInitialized(lower):
		return true
	case prefixMarker(lower) != cfg.ProtonVersion:
		return true // reconcilePrefixVersion would move it aside and rebuild
	case !steamLinuxRuntimeReady(cfg):
		return true // warmPrefix would run wineboot again
	case cfg.Runtime == "umu" && cfg.InstallVCRedist && !prefixHasVCRedistOverrides(lower):
		return true // ensureVCRedist would write the prefix registry
	}
	return false
}

// prepareSharedPrefixWrite clears the way for an operation that writes the
// shared lower prefix: it unmounts every writable layer that nothing is using,
// and refuses when one is still live.
//
// Unmounting rather than merely refusing is what keeps this from deadlocking.
// Layers stay mounted after their instance stops — that's deliberate (§3.3),
// and it means "stop the instances" would NOT clear the mounts. A guard that
// only refused would therefore be unclearable by any action the operator can
// take from this program: the very first VC++ reinstall or Proton bump after
// one instance had ever started would be permanently blocked.
//
// Unmounting an idle layer loses nothing. The upper directory stays on disk,
// and the next start remounts it — and if the lower did change underneath, the
// .lower-stamp mismatch rebuilds it, which is exactly what should happen.
//
// A start racing this is possible in principle (unmount, then the layer is
// remounted while the lower is being written). The verify paths can't hit it —
// they hold the server-files lock, which instance start checks — and for
// EnsureRuntime the window is a few milliseconds on a machine whose lower is
// being built or upgraded. Not worth a lock spanning the whole operation.
//
// It IS worth being able to see the window afterwards, which is what op and
// the returned closure are for: the symptoms of losing that race land on an
// *instance*, minutes later and at random, so the only way to tie one back to
// its cause is a timestamped pair of "window opened / window closed" lines to
// compare an instance's mount time against. Deliberately just logging — a lock
// spanning the whole operation is a bigger change than the evidence so far
// justifies. See docs/UMU_PREFIX_OVERLAY_TODO.md §1.4.
func prepareSharedPrefixWrite(op string) (func(), error) {
	cfg := getConfig()

	var live, freed []string
	for _, key := range overlayKeysMounted(cfg) {
		merged := overlayMergedDir(cfg, key)
		if wineserverHoldsPrefix(merged) {
			live = append(live, key)
			continue
		}
		if err := unmountOverlay(merged); err != nil {
			// Can't unmount and can't prove it's idle: treat as live rather
			// than writing the lower anyway.
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
		"（linux.prefix_mode=overlay）。修改被挂载引用的 lowerdir 是 overlayfs 明确的未定义行为，"+
		"请先停止这些实例后重试",
		prefixDir(cfg, ""), strings.Join(live, "、"))
}

// openLowerWriteWindow marks the span during which the shared lower prefix may
// be modified, and returns the closer.
//
// Only under prefix_mode "overlay" is there anything to correlate: elsewhere
// nothing references the shared prefix as a lowerdir, so the pair would be two
// lines of noise on every API server start.
func openLowerWriteWindow(cfg Config, op string) func() {
	if cfg.PrefixMode != "overlay" {
		return func() {}
	}

	lower := prefixDir(cfg, "")
	start := time.Now()
	logger.Infof("共享 Wine 前缀 %s 的修改窗口已打开（%s）；此刻起到窗口关闭之间启动的实例，"+
		"其可写层会把一个正在被修改的底层当 lowerdir —— 实例事后出现莫名其妙的症状时，"+
		"先拿它的「已挂载」时刻和这一对时间戳对一下", lower, op)
	return func() {
		logger.Infof("共享 Wine 前缀的修改窗口已关闭（%s），持续 %s", op, time.Since(start).Round(time.Millisecond))
	}
}
