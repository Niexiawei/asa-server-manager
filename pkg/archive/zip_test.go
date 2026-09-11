package archive

import (
	"archive/zip"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zipItem struct {
	name    string
	content string
	mode    fs.FileMode // 非零时写入条目模式（用于构造符号链接）
	nonUTF8 bool
}

func writeZip(t *testing.T, items ...zipItem) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pkg.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, it := range items {
		h := &zip.FileHeader{Name: it.name, Method: zip.Deflate, NonUTF8: it.nonUTF8}
		if it.mode != 0 {
			h.SetMode(it.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(it.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractZipNormalPackage(t *testing.T) {
	zp := writeZip(t,
		zipItem{name: "TidyDamsASA/"},
		zipItem{name: "TidyDamsASA/PluginInfo.json", content: `{"FullName":"TidyDamsASA"}`},
		zipItem{name: "TidyDamsASA/TidyDamsASA.dll", content: "MZ"},
		zipItem{name: `TidyDamsASA\sub\config.json`, content: "{}"}, // 反斜杠分隔
		zipItem{name: "__MACOSX/TidyDamsASA/._PluginInfo.json", content: "junk"},
		zipItem{name: "TidyDamsASA/.DS_Store", content: "junk"},
		zipItem{name: "TidyDamsASA/Thumbs.db", content: "junk"},
	)
	dest := t.TempDir()
	entries, err := ExtractZip(zp, dest, Limits{})
	if err != nil {
		t.Fatalf("ExtractZip: %v", err)
	}

	var got []string
	for _, e := range entries {
		got = append(got, e.Path)
	}
	want := "TidyDamsASA TidyDamsASA/PluginInfo.json TidyDamsASA/TidyDamsASA.dll TidyDamsASA/sub/config.json"
	if strings.Join(got, " ") != want {
		t.Errorf("条目 = %v，want %s", got, want)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "TidyDamsASA", "sub", "config.json")); string(b) != "{}" {
		t.Errorf("反斜杠分隔的条目没有解到正确位置，内容 %q", b)
	}
	if entries[2].Size != 2 {
		t.Errorf("Size 应是实际写出的字节数 2，实际 %d", entries[2].Size)
	}
	for _, junk := range []string{"__MACOSX", filepath.Join("TidyDamsASA", ".DS_Store"), filepath.Join("TidyDamsASA", "Thumbs.db")} {
		if _, err := os.Stat(filepath.Join(dest, junk)); !os.IsNotExist(err) {
			t.Errorf("%s 应被忽略", junk)
		}
	}
}

func TestExtractZipRejectsUnsafeEntries(t *testing.T) {
	cases := []struct {
		name  string
		items []zipItem
		want  string
	}{
		{"zip slip", []zipItem{{name: "ok.txt"}, {name: "../evil.txt"}}, ".."},
		{"反斜杠 zip slip", []zipItem{{name: `a\..\..\evil.txt`}}, ".."},
		{"绝对路径", []zipItem{{name: "/etc/passwd"}}, "绝对路径"},
		{"盘符", []zipItem{{name: "C:/Windows/evil.dll"}}, "盘符"},
		{"备用数据流", []zipItem{{name: "a.txt:stream"}}, "冒号"},
		{"符号链接", []zipItem{{name: "link", content: "/etc/passwd", mode: fs.ModeSymlink | 0777}}, "符号链接"},
		{"仅大小写不同的文件", []zipItem{{name: "A.dll"}, {name: "a.dll"}}, "仅大小写不同"},
		{"仅大小写不同的目录", []zipItem{{name: "Plug/x.dll"}, {name: "plug/y.dll"}}, "仅大小写不同"},
		{"重复条目", []zipItem{{name: "x.dll"}, {name: "x.dll"}}, "重复"},
		{"既是文件又是目录", []zipItem{{name: "a"}, {name: "a/b"}}, "既是文件又是目录"},
		{"非 UTF-8 条目名", []zipItem{{name: "\xb2\xe5\xbc\xfe.dll", nonUTF8: true}}, "UTF-8"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dest := t.TempDir()
			_, err := ExtractZip(writeZip(t, c.items...), dest, Limits{})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v，want 包含 %q", err, c.want)
			}
			// 先校验后写盘：被拒的包一个文件都不该落地
			if left, _ := os.ReadDir(dest); len(left) != 0 {
				t.Errorf("被拒的包不应写出任何文件，实际留下 %d 个", len(left))
			}
		})
	}
}

func TestExtractZipLimits(t *testing.T) {
	t.Run("条目数", func(t *testing.T) {
		zp := writeZip(t, zipItem{name: "a"}, zipItem{name: "b"}, zipItem{name: "c"})
		if _, err := ExtractZip(zp, t.TempDir(), Limits{MaxEntries: 2}); err == nil || !strings.Contains(err.Error(), "条目") {
			t.Fatalf("err = %v，want 条目数超限", err)
		}
	})
	t.Run("字节数", func(t *testing.T) {
		zp := writeZip(t, zipItem{name: "a", content: "12345"}, zipItem{name: "b", content: "123456"})
		if _, err := ExtractZip(zp, t.TempDir(), Limits{MaxTotalBytes: 10}); err == nil || !strings.Contains(err.Error(), "上限") {
			t.Fatalf("err = %v，want 总大小超限", err)
		}
		if _, err := ExtractZip(zp, t.TempDir(), Limits{MaxTotalBytes: 11}); err != nil {
			t.Fatalf("恰好等于上限应当通过: %v", err)
		}
	})
}

func TestExtractZipNotAZip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fake.zip")
	if err := os.WriteFile(p, []byte("not a zip"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractZip(p, t.TempDir(), Limits{}); err == nil || !strings.Contains(err.Error(), "不是有效的 zip") {
		t.Fatalf("err = %v", err)
	}
}
