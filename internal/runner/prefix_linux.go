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

func prefixKeyFor(instanceName string) string {
	if getConfig().PrefixMode != "per-instance" {
		return ""
	}
	return instanceName
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
	if prefixKey == "" || cfg.PrefixMode != "per-instance" {
		return checkRuntime()
	}

	// umu-run + GE-Proton + the shared prefix are the preconditions for
	// building any further prefix, and none of them are this function's to
	// install. Fail with checkRuntime's end-user wording.
	if err := checkRuntime(); err != nil {
		return err
	}

	prefix := instancePrefixDir(cfg, prefixKey)
	unlock := lockPrefix(prefix)
	defer unlock()

	logf := progressLogger(progress)

	// Fast path, and the reason this is cheap to call on every start.
	if prefixInitialized(prefix) && prefixMarker(prefix) == cfg.ProtonVersion {
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

func removeInstancePrefix(instanceName string) error {
	if instanceName == "" {
		return nil
	}
	cfg := getConfig()
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
	// Per-instance prefixes are "<shared>-<key>". The glob also catches the
	// ".bak-<version>" directories reconcilePrefixVersion leaves behind, which
	// is wanted: they occupy disk and nothing else reports them.
	if m, _ := filepath.Glob(shared + "-*"); len(m) > 0 {
		paths = append(paths, m...)
	}

	out := make([]PrefixInfo, 0, len(paths))
	for _, p := range paths {
		if !dirExists(p) {
			continue
		}
		out = append(out, PrefixInfo{
			Key:           strings.TrimPrefix(strings.TrimPrefix(p, shared), "-"),
			Path:          p,
			Initialized:   prefixInitialized(p),
			ProtonVersion: prefixMarker(p),
			InUse:         wineserverHoldsPrefix(p),
			SizeBytes:     dirSize(p),
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
