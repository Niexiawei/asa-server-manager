//go:build linux

package runner

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// prefixKeyFor: both isolating modes key the prefix by instance name; only
// "shared" (and anything unrecognized) collapses every instance onto one.
func prefixKeyFor(instanceName string) string {
	switch getConfig().PrefixMode {
	case "per-instance", "overlay":
		return instanceName
	}
	return ""
}

// instancePrefixDir is prefixDir's per-instance branch with the mode check
// removed: "where would this instance's own prefix live". Cleanup and status
// need it regardless of the mode in force right now, because a prefix created
// during a past stint in per-instance mode outlives the switch back.
func instancePrefixDir(cfg Config, key string) string {
	if key == "" {
		return prefixDir(cfg, "")
	}
	return prefixDir(cfg, "") + "-" + key
}

// prefixLocks serializes work on one prefix without serializing work on
// different ones. The old package-level runtimeMu still guards EnsureRuntime
// as a whole (it also downloads the shared umu/GE-Proton), but per-instance
// prefix creation must be able to run in parallel across instances — a single
// lock would turn "start 5 instances" into five sequential wineboots.
var prefixLocks sync.Map // prefix path -> *sync.Mutex

func lockPrefix(path string) func() {
	v, _ := prefixLocks.LoadOrStore(path, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// prefixCreationSlots caps how many first-time prefix creations run at once.
//
// Each one spawns a full pressure-vessel container running wineboot; starting
// eight instances at once on a small VPS would otherwise fire eight of them
// simultaneously and look exactly like a hang. Two keeps the parallelism that
// motivated the per-prefix locks while bounding the burst. Only *creation*
// takes a slot — the common path (prefix already there) never touches this.
var prefixCreationSlots = make(chan struct{}, 2)

func ensurePrefix(ctx context.Context, prefixKey string, progress io.Writer) error {
	cfg := getConfig()

	// The shared prefix belongs to EnsureRuntime: it's created during setup,
	// alongside the umu/GE-Proton downloads it depends on. Verifying is in
	// scope here; rebuilding it behind a server start is not.
	if prefixKey == "" || (cfg.PrefixMode != "per-instance" && cfg.PrefixMode != "overlay") {
		return checkRuntime()
	}

	// umu-run + GE-Proton + the shared prefix are the preconditions for
	// building any further prefix, and none of them are this function's to
	// install. Fail with checkRuntime's end-user wording.
	if err := checkRuntime(); err != nil {
		return err
	}

	// overlay builds its private layer on top of that same shared prefix
	// instead of running a wineboot of its own — milliseconds instead of a
	// minute, and no second copy of the runtime on disk.
	if cfg.PrefixMode == "overlay" {
		return ensureOverlayPrefix(ctx, cfg, prefixKey, progressLogger(progress))
	}

	prefix := instancePrefixDir(cfg, prefixKey)
	unlock := lockPrefix(prefix)
	defer unlock()

	logf := progressLogger(progress)

	// Fast path, and the reason this is cheap to call on every start.
	if prefixInitialized(prefix) && prefixMarker(prefix) == cfg.ProtonVersion {
		// ...但 VC++ 的 DLL override 要单独过一眼。装它的那一步（下面 warmPrefix
		// 之后）只在**新建 prefix** 时跑，所以任何比那段代码更早创建的 per-instance
		// prefix 会永远停在没有 override 的状态：闸门放行、实例起来、ArkApi 加载
		// 不了，而且每次启动都只有一条「没检测到 VC++ 运行时」的告警。
		// 这正是 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §2.2 记的「per-instance 暂不
		// 覆盖」——那条备注当时的理由是"没人传 PrefixKey"，而 start 路径现在传了。
		//
		// 判据用 override 而不是 prefixHasVCRedist：见前者的注释，安装器在无头机上
		// 装不上，用它当判据会让每次启动都重跑一遍 regedit 容器。
		if !prefixHasVCRedistOverrides(prefix) {
			if err := ensureVCRedist(ctx, cfg, prefixKey, logf); err != nil {
				logf("实例 %s 的 Wine 前缀里补装 VC++ 运行时失败（%v）；不使用 ArkApi 可忽略", prefixKey, err)
			}
		}
		return nil
	}

	select {
	case prefixCreationSlots <- struct{}{}:
		defer func() { <-prefixCreationSlots }()
	case <-ctx.Done():
		return fmt.Errorf("等待 Wine 前缀创建槽位时被取消: %w", ctx.Err())
	}

	logf("实例 %s 使用独立 Wine 前缀（linux.prefix_mode=per-instance），正在创建 %s；"+
		"首次创建约需一分钟，之后每次启动都是秒过", prefixKey, prefix)

	// warmPrefix does the whole job for one prefix: version reconcile,
	// wineboot --init, chown to the runtime user, wineserver drain, and the
	// .created-by-proton marker. prefetched=false because the Steam Linux
	// Runtime is global and was already fetched for the shared prefix.
	if err := warmPrefix(ctx, cfg, prefixKey, logf, false); err != nil {
		return fmt.Errorf("创建实例 %s 的 Wine 前缀失败: %w", prefixKey, err)
	}

	// Same rule as EnsureRuntime: ArkApi is optional, so a failed VC++ install
	// must not block a start — but it has to be loud, because the people who
	// need it have no other way to find out. See
	// docs/ARKAPI_LINUX_VCREDIST_PLAN.md §3.2.
	if err := ensureVCRedist(ctx, cfg, prefixKey, logf); err != nil {
		logf("实例 %s 的 Wine 前缀里安装 VC++ 运行时失败（%v）；不使用 ArkApi 可忽略", prefixKey, err)
	}
	return nil
}

// removeInstancePrefix deletes everything this instance owns, in both shapes.
//
// Mode-independent on purpose (see the exported doc comment): an instance that
// ran under per-instance and then under overlay has left a directory of each,
// and deleting the instance has to take both. Each half is a no-op when its
// directory isn't there.
func removeInstancePrefix(instanceName string) error {
	if instanceName == "" {
		return nil
	}
	cfg := getConfig()

	if err := removeOverlayPrefix(cfg, instanceName); err != nil {
		return err
	}

	prefix := instancePrefixDir(cfg, instanceName)
	if prefix == prefixDir(cfg, "") {
		// Belt and braces: never let a bad key delete the shared prefix.
		return nil
	}
	if !dirExists(prefix) {
		return nil
	}

	unlock := lockPrefix(prefix)
	defer unlock()

	if wineserverHoldsPrefix(prefix) {
		return fmt.Errorf("Wine 前缀 %s 仍被 wineserver 占用，请先停止实例 %s 再删除", prefix, instanceName)
	}
	return os.RemoveAll(prefix)
}

func prefixStatus() []PrefixInfo {
	cfg := getConfig()
	shared := prefixDir(cfg, "")

	paths := []string{shared}
	// Two shapes to find, and they need two patterns:
	//   "<shared>-<key>"        per-instance prefixes
	//   "<shared>.bak-<版本>"   what reconcilePrefixVersion moves aside on a
	//                           Proton bump — a full ~700MB prefix that nothing
	//                           will ever open again
	// The second one used to be missing while this function's callers claimed
	// to manage it, so `prefix status` never showed those directories and
	// `prefix gc` never offered to reclaim them. They just sat there.
	//
	// "<shared>-*" also catches "umu-prefix-overlay", which is NOT a prefix but
	// the directory holding every instance's writable layer — reporting it as
	// an instance named "overlay" would be wrong twice over: a bogus row, and a
	// gc candidate pointing at everyone's data. Excluded by exact path.
	overlays := overlayRoot(cfg)
	for _, pattern := range []string{shared + "-*", shared + ".bak-*"} {
		m, _ := filepath.Glob(pattern)
		for _, p := range m {
			if p == overlays {
				continue
			}
			paths = append(paths, p)
		}
	}

	out := make([]PrefixInfo, 0, len(paths))
	for _, p := range paths {
		if !dirExists(p) {
			continue
		}
		// per-instance 前缀是 "<shared>-<key>"，版本备份是 "<shared>.bak-<版本>" ——
		// 两种后缀都要剥掉。只剥 "-" 的话备份目录的 Key 会带上那个点，于是
		// 调用方的 HasPrefix(Key, "bak-") 永远不成立：报表把它当成一个名叫
		// ".bak-<版本>" 的实例，而 gc 会去删一个根本不存在的路径然后报「完成」。
		key := strings.TrimPrefix(p, shared)
		key = strings.TrimPrefix(key, "-")
		key = strings.TrimPrefix(key, ".")

		out = append(out, PrefixInfo{
			Key:           key,
			Path:          p,
			Initialized:   prefixInitialized(p),
			ProtonVersion: prefixMarker(p),
			InUse:         wineserverHoldsPrefix(p),
			SizeBytes:     dirSize(p),
			Current:       prefixDir(cfg, key) == p,
		})
	}
	return append(out, overlayStatus(cfg)...)
}

// overlayStatus reports one row per writable layer under umu-prefix-overlay/,
// independent of the mode currently configured (layers outlive a switch back
// to shared, exactly like per-instance prefixes do).
//
// SizeBytes is the **upper** layer, never the mount point: walking merged
// would count the shared lower once per instance and report hundreds of MB
// each, turning this mode's entire selling point upside down in the one place
// meant to demonstrate it. See docs/UMU_PREFIX_OVERLAY_PLAN.md §12.3.
func overlayStatus(cfg Config) []PrefixInfo {
	entries, err := os.ReadDir(overlayRoot(cfg))
	if err != nil {
		return nil
	}
	mounts := listOverlayMounts()

	var out []PrefixInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		key := e.Name()
		merged := overlayMergedDir(cfg, key)
		mounted := mounts[merged]

		// 独占占用：挂载时是 upper（底层是共享的，量 merged 会把它按实例数重复计），
		// 没挂载时 merged 里就是**真的一整份拷贝**（降级路径），那才是它的占用。
		// 这一栏正是用来看「这台机器上 overlay 到底有没有在省盘」的，报错方向
		// 会让人得出完全相反的结论。
		measured := overlayUpperDir(cfg, key)
		if !mounted {
			measured = merged
		}

		out = append(out, PrefixInfo{
			Key:           key,
			Path:          merged,
			Initialized:   prefixInitialized(merged),
			ProtonVersion: readOverlayStamp(cfg, key),
			InUse:         wineserverHoldsPrefix(merged),
			SizeBytes:     dirSize(measured),
			Overlay:       true,
			Mounted:       mounted,
			Current:       prefixDir(cfg, key) == merged,
		})
	}
	return out
}

// prefixMarker reads .created-by-proton, "" when it isn't there.
func prefixMarker(prefix string) string {
	b, err := os.ReadFile(filepath.Join(prefix, ".created-by-proton"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// dirSize is best-effort: unreadable entries are skipped rather than failing
// the whole listing, since this only feeds a human-readable report.
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
