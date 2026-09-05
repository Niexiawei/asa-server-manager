package arkcache

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

type zipEntry struct {
	name string
	body []byte
}

func buildZip(t *testing.T, entries ...zipEntry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cache.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func goodEntries(t *testing.T) []zipEntry {
	t.Helper()
	return []zipEntry{
		{offsetsFileName, buildRecords(t, offsetValueSize, "AShooterGameMode.BeginPlay")},
		{bitfieldsFileName, buildRecords(t, bitfieldValueSize, "bIsDead")},
	}
}

func TestExtractCacheZipHappyPath(t *testing.T) {
	src := buildZip(t, goodEntries(t)...)
	dest := filepath.Join(t.TempDir(), "gen")

	if err := extractCacheZip(src, dest, 0); err != nil {
		t.Fatalf("应当成功: %v", err)
	}
	for _, name := range []string{offsetsFileName, bitfieldsFileName} {
		if fi, err := os.Stat(filepath.Join(dest, name)); err != nil || fi.Size() == 0 {
			t.Fatalf("%s 未落盘: %v", name, err)
		}
	}
}

// ZIP 里自带的 cached_key.cache 允许存在，但**一律不落盘** —— 它是指针文件，
// 照抄进来会指向一个根本不存在的 generation。cached_offsets.txt 同理（只是调试转储）。
func TestExtractCacheZipIgnoresKeyAndTxt(t *testing.T) {
	entries := append(goodEntries(t),
		zipEntry{metadataFileName, []byte(`{"version":1}`)},
		zipEntry{offsetsTxtFileName, []byte("AShooterGameMode.BeginPlay\n")},
	)
	dest := filepath.Join(t.TempDir(), "gen")

	if err := extractCacheZip(buildZip(t, entries...), dest, 0); err != nil {
		t.Fatalf("应当成功: %v", err)
	}
	for _, name := range []string{metadataFileName, offsetsTxtFileName} {
		if _, err := os.Stat(filepath.Join(dest, name)); err == nil {
			t.Fatalf("%s 不应被提取", name)
		}
	}
}

func TestExtractCacheZipRejects(t *testing.T) {
	good := goodEntries(t)

	for _, tc := range []struct {
		name    string
		entries []zipEntry
		maxSize int64
	}{
		{"条目数不足 2", good[:1], 0},
		{"条目数超过 4", append(append([]zipEntry{}, good...), zipEntry{metadataFileName, []byte("{}")}, zipEntry{offsetsTxtFileName, []byte("x")}, zipEntry{"extra", []byte("y")}), 0},
		{"路径穿越", []zipEntry{{"../" + offsetsFileName, []byte("x")}, good[1]}, 0},
		{"子目录", []zipEntry{{"sub/" + offsetsFileName, []byte("x")}, good[1]}, 0},
		{"盘符", []zipEntry{{`C:\` + offsetsFileName, []byte("x")}, good[1]}, 0},
		{"不在白名单里的名字", []zipEntry{{"README.md", []byte("x")}, good[1]}, 0},
		{"同名重复", []zipEntry{good[0], good[0], good[1]}, 0},
		{"缺少 bitfields", []zipEntry{good[0], {metadataFileName, []byte("{}")}}, 0},
		{"空条目", []zipEntry{{offsetsFileName, nil}, good[1]}, 0},
		{"总量超过上限", good, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "gen")
			if err := extractCacheZip(buildZip(t, tc.entries...), dest, tc.maxSize); err == nil {
				t.Fatal("应当拒绝，却通过了")
			}
		})
	}
}
