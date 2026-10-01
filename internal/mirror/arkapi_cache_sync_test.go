package mirror

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/arkcache"
)

// arkApiCacheDirRel / keyFileName / seedSourceArkApiCache 在 helpers_test.go。

// 源缓存由我们接管之后，源目录就是权威 —— 守卫必须让开，否则两个静默故障：
// 镜像里的 cached_key.cache 永远不被更新（还指向旧哈希的 generation，ArkApi 判定
// 失效、照样去下，预取白做），旧 generation 永远不被删（每次 ARK 更新每个实例多留
// 几百 MB）。见 docs/ARKAPI_CACHE_PREFETCH_PLAN.md §7。
func TestSyncReconcilesManagedArkApiCache(t *testing.T) {
	mirrorDir, exceptionTargets := setupPluginMirror(t)
	hash, genRel := seedSourceArkApiCache(t)

	if err := syncMirrorEntries(mirrorDir, exceptionTargets); err != nil {
		t.Fatalf("首轮同步失败: %v", err)
	}

	mirrorCache := filepath.Join(mirrorDir, filepath.FromSlash(arkApiCacheDirRel))
	if res, err := arkcache.Inspect(mirrorCache, hash); err != nil || !res.Ready {
		t.Fatalf("缓存没被同步进镜像: %v %s", err, res.Reason)
	}

	// 模拟 ARK 更新前留下的残骸：镜像里的指针文件指向另一个哈希的旧代，
	// 并且那一代还实实在在占着盘。
	staleHash := strings.Repeat("a", 64)
	staleGen := fmt.Sprintf("generations/%s-1-1-0", staleHash)
	writeAt(t, filepath.Join(mirrorCache, filepath.FromSlash(staleGen), "cached_offsets.cache"), "old")
	writeAt(t, filepath.Join(mirrorCache, keyFileName), fmt.Sprintf(
		`{"version":1,"executable_hash":%q,"last_modified":"LM-old","cache_directory":%q}`, staleHash, staleGen))

	if err := syncMirrorEntries(mirrorDir, exceptionTargets); err != nil {
		t.Fatalf("二轮同步失败: %v", err)
	}

	if res, err := arkcache.Inspect(mirrorCache, hash); err != nil || !res.Ready {
		t.Fatalf("镜像里的 cached_key.cache 没被回写: %v %s", err, res.Reason)
	} else if res.Generation != genRel {
		t.Fatalf("cached_key.cache 还指向 %q，want %q", res.Generation, genRel)
	}
	if _, err := os.Stat(filepath.Join(mirrorCache, filepath.FromSlash(staleGen))); err == nil {
		t.Fatal("镜像里的旧 generation 没被删掉")
	}
}

// 源缓存不是我们备的（用户没启用预取，或这台机器还没下成）时，守卫必须照旧生效：
// Cache 里全是 ArkApi 运行期自己写的东西，源目录对它一无所知，一律不删不比对。
// 这是回归护栏 —— 收窄守卫不能把原来的行为也收掉。
func TestSyncStillProtectsUnmanagedArkApiCache(t *testing.T) {
	mirrorDir, exceptionTargets := setupPluginMirror(t)

	// 源侧只有一份形状不对的缓存（哈希对不上当前 exe）→ sourceCacheManaged 为 false
	srcCache := filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(arkApiCacheDirRel))
	writeAt(t, filepath.Join(srcCache, keyFileName), "source-side-key")

	if err := syncMirrorEntries(mirrorDir, exceptionTargets); err != nil {
		t.Fatalf("首轮同步失败: %v", err)
	}

	mirrorCache := filepath.Join(mirrorDir, filepath.FromSlash(arkApiCacheDirRel))
	// ArkApi 自己下的东西：源里根本没有的文件 + 与源不一致的内容
	writeAt(t, filepath.Join(mirrorCache, "generations", "runtime-gen", "cached_offsets.cache"), "downloaded-by-arkapi")
	writeAt(t, filepath.Join(mirrorCache, keyFileName), "written-by-arkapi")

	if err := syncMirrorEntries(mirrorDir, exceptionTargets); err != nil {
		t.Fatalf("二轮同步失败: %v", err)
	}

	if _, err := os.Stat(filepath.Join(mirrorCache, "generations", "runtime-gen", "cached_offsets.cache")); err != nil {
		t.Errorf("ArkApi 运行期写入的文件被当成多余条目删了: %v", err)
	}
	if got := readAt(t, filepath.Join(mirrorCache, keyFileName)); got != "written-by-arkapi" {
		t.Errorf("ArkApi 自己的 cached_key.cache 被源版本回写覆盖了: %q", got)
	}
}

// 源目录里解压中的 generation（generations/.staging-*）是半成品：managed 模式下
// generations/ 不做内容对账，一旦被复制进镜像就永远不会被修复。
func TestSyncSkipsArkApiStagingGeneration(t *testing.T) {
	mirrorDir, exceptionTargets := setupPluginMirror(t)
	seedSourceArkApiCache(t)

	srcCache := filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(arkApiCacheDirRel))
	staging := "generations/" + arkcache.StagingDirPrefix + strings.Repeat("c", 64) + "-1-1-0"
	writeAt(t, filepath.Join(srcCache, filepath.FromSlash(staging), "cached_offsets.cache"), "half")

	if err := syncMirrorEntries(mirrorDir, exceptionTargets); err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	mirrorCache := filepath.Join(mirrorDir, filepath.FromSlash(arkApiCacheDirRel))
	if _, err := os.Stat(filepath.Join(mirrorCache, filepath.FromSlash(staging))); err == nil {
		t.Fatal("解压中的 staging 目录被同步进了镜像")
	}
}

// ArkApi 自己留下的历史格式（cached_key.cache 是裸哈希、缓存在 Cache 根）对当前 exe
// 也是 Ready 的，但它不是我们备的 —— 守卫不能因此翻转。
func TestBareHashSourceCacheIsNotManaged(t *testing.T) {
	setupPluginMirror(t)
	hash, _ := seedSourceArkApiCache(t)

	srcCache := filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(arkApiCacheDirRel))
	writeAt(t, filepath.Join(srcCache, keyFileName), hash)
	writeAt(t, filepath.Join(srcCache, "cached_offsets.cache"), "x")
	writeAt(t, filepath.Join(srcCache, "cached_bitfields.cache"), "x")

	if sourceCacheManaged() {
		t.Fatal("裸哈希格式的缓存被认成了我们备的")
	}
}
