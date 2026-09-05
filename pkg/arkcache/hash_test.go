package arkcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExeHashCacheInvalidatesOnChange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ArkAscendedServer.exe")
	if err := os.WriteFile(p, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := ExeHash(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 {
		t.Fatalf("哈希长度 = %d", len(first))
	}
	if again, _ := ExeHash(p); again != first {
		t.Fatal("同一个文件两次结果不一致")
	}

	// 服务器更新：内容与 modTime 都变了，缓存必须失效。
	if err := os.WriteFile(p, []byte("v2-longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, err := ExeHash(p)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("exe 变了，缓存没失效")
	}
}

func TestExeHashMissingFile(t *testing.T) {
	if _, err := ExeHash(filepath.Join(t.TempDir(), "nope.exe")); err == nil {
		t.Fatal("文件不存在时应当报错")
	}
}
