package mirror

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/arkcache"
)

// 跨平台的测试脚手架：Windows 与 Linux（WSL2 下 `wsl -e zsh -lc ...`）共用。
// 「盘上是不是链接」的地面真相按平台拆在 linkattr_{windows,linux}_test.go。

// sqliteHeader 是 SQLite 数据库文件头。识别走的是魔数而不是扩展名，
// 所以测试里不需要真的建库。
const sqliteHeader = "SQLite format 3\x00"

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

func writeAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("建目录 %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写文件 %s: %v", path, err)
	}
}

func readAt(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件 %s: %v", path, err)
	}
	return string(b)
}
