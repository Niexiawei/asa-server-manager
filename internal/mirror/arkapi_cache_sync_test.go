//go:build windows

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

const (
	arkApiCacheDirRel = win64RelPath + "/ArkApi/Cache"
	keyFileName       = "cached_key.cache"
)

// seedSourceArkApiCache 在**源目录**里造一份对当前 exe 有效的 offsets cache，
// 也就是 pkg/arkcache 预取成功后的形态。返回 exe 哈希与 generation 相对路径。
func seedSourceArkApiCache(t *testing.T) (string, string) {
	t.Helper()
	exe := filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64RelPath), "ArkAscendedServer.exe")
	hash, err := arkcache.ExeHash(exe)
	if err != nil {
		t.Fatalf("算 exe 哈希: %v", err)
	}
	genRel := fmt.Sprintf("generations/%s-1-2-0", hash)

	srcCache := filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(arkApiCacheDirRel))
	writeAt(t, filepath.Join(srcCache, filepath.FromSlash(genRel), "cached_offsets.cache"), "offsets-v1")
	writeAt(t, filepath.Join(srcCache, filepath.FromSlash(genRel), "cached_bitfields.cache"), "bitfields-v1")
	writeAt(t, filepath.Join(srcCache, keyFileName), fmt.Sprintf(
		`{"version":1,"executable_hash":%q,"last_modified":"LM-v1","cache_directory":%q}`, hash, genRel))

	if res, err := arkcache.Inspect(srcCache, hash); err != nil || !res.Ready {
		t.Fatalf("造出来的源缓存自己就不合格: %v %s", err, res.Reason)
	}
	return hash, genRel
}

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
