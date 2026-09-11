package arkapimanage

import (
	"debug/pe"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"asa-server/pkg/archive"
)

// fakePE 构造一个最小的、debug/pe 能解析的 PE 头：MZ + e_lfanew + "PE\0\0" + COFF 文件头，
// 没有节也没有可选头。fixture 里不提交真实 dll（方案 §12）。
func fakePE(machine uint16) string {
	b := make([]byte, 0x100)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[0x3c:], 0x80)
	copy(b[0x80:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x84:], machine)
	return string(b)
}

var amd64PE = fakePE(pe.IMAGE_FILE_MACHINE_AMD64)

// extracted 把 files 写进一个临时目录，返回目录与对应的条目清单——等价于 ExtractZip 的产物。
func extracted(t *testing.T, files map[string]string) (string, []archive.Entry) {
	t.Helper()
	dir := t.TempDir()
	var entries []archive.Entry
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, archive.Entry{Path: rel, Size: int64(len(content))})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return dir, entries
}

// TidyDamsASA.zip 的结构：外面包一层以插件名命名的目录（方案 §3.3）。
func tidyDams() map[string]string {
	return map[string]string{
		"TidyDamsASA/PluginInfo.json": `{"FullName":"TidyDamsASA","Description":"No more only wood in beaver dams!","Version":1.3,"MinApiVersion":2}`,
		"TidyDamsASA/TidyDamsASA.dll": amd64PE,
		"TidyDamsASA/TidyDamsASA.pdb": "pdb",
		"TidyDamsASA/config.json":     `{}`,
	}
}

var coreOK = PluginCheck{CoreInstalled: true}

func TestFakePEHelperParses(t *testing.T) {
	dir, _ := extracted(t, map[string]string{"x.dll": amd64PE, "arm.dll": fakePE(pe.IMAGE_FILE_MACHINE_ARM64)})
	if err := checkAMD64PE(filepath.Join(dir, "x.dll")); err != nil {
		t.Fatalf("fakePE 应当被认成 x64 PE: %v", err)
	}
	if err := checkAMD64PE(filepath.Join(dir, "arm.dll")); err == nil {
		t.Fatal("ARM64 的 PE 应当被拒")
	}
}

func TestValidatePluginTidyDamsStructure(t *testing.T) {
	dir, entries := extracted(t, tidyDams())
	rep := ValidatePluginPackage(dir, entries, coreOK)
	if len(rep.Errors) != 0 {
		t.Fatalf("errors = %v", rep.Errors)
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("warnings = %v", rep.Warnings)
	}
	if rep.Name != "TidyDamsASA" || rep.Version != "1.3" || rep.MinApiVersion != "2" {
		t.Errorf("元数据 = %+v", rep)
	}
	if rep.root != filepath.Join(dir, "TidyDamsASA") {
		t.Errorf("插件根 = %s", rep.root)
	}
	if len(rep.Files) != 4 || rep.Files[0].Path != "PluginInfo.json" {
		t.Errorf("Files 应相对插件根列出: %+v", rep.Files)
	}
}

func TestValidatePluginFlatStructure(t *testing.T) {
	dir, entries := extracted(t, map[string]string{
		"PluginInfo.json": `{"FullName":"Flat","Version":"1.10"}`,
		"Flat.dll":        amd64PE,
		"Flat.pdb":        "pdb",
		"Helper.dll":      amd64PE, // 没有同名 pdb 的 dll 不参与推断插件名
	})
	rep := ValidatePluginPackage(dir, entries, coreOK)
	if len(rep.Errors) != 0 {
		t.Fatalf("errors = %v", rep.Errors)
	}
	if rep.Name != "Flat" || rep.root != dir {
		t.Errorf("name=%s root=%s", rep.Name, rep.root)
	}
	if rep.Version != "1.10" {
		t.Errorf("版本号必须保留原文，实际 %q", rep.Version)
	}
}

func TestValidatePluginAcceptsBOM(t *testing.T) {
	files := tidyDams()
	files["TidyDamsASA/PluginInfo.json"] = "\xef\xbb\xbf" + `{"FullName":"TidyDamsASA","Version":1.10}`
	dir, entries := extracted(t, files)
	rep := ValidatePluginPackage(dir, entries, coreOK)
	if len(rep.Errors) != 0 || rep.Version != "1.10" {
		t.Fatalf("errors=%v version=%q", rep.Errors, rep.Version)
	}
}

func TestValidatePluginErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]string)
		chk    PluginCheck
		want   string
	}{
		{"缺 dll", func(f map[string]string) { delete(f, "TidyDamsASA/TidyDamsASA.dll") }, coreOK, "缺少 TidyDamsASA.dll"},
		{"缺 pdb", func(f map[string]string) { delete(f, "TidyDamsASA/TidyDamsASA.pdb") }, coreOK, "缺少 TidyDamsASA.pdb"},
		{"目录名与 dll 名不同", func(f map[string]string) {
			delete(f, "TidyDamsASA/TidyDamsASA.dll")
			f["TidyDamsASA/Other.dll"] = amd64PE
		}, coreOK, "缺少 TidyDamsASA.dll"},
		{"dll 大小写不同", func(f map[string]string) {
			delete(f, "TidyDamsASA/TidyDamsASA.dll")
			f["TidyDamsASA/tidydamsasa.dll"] = amd64PE
		}, coreOK, "大小写"},
		{"两个 PluginInfo.json", func(f map[string]string) {
			f["Other/PluginInfo.json"] = `{"FullName":"Other"}`
		}, coreOK, "只能包含一个插件"},
		{"没有 PluginInfo.json", func(f map[string]string) { delete(f, "TidyDamsASA/PluginInfo.json") }, coreOK, "不是 ArkApi 插件包"},
		{"插件包里带加载器", func(f map[string]string) { f["AsaApiLoader.exe"] = amd64PE }, coreOK, "主程序包"},
		{"改后缀的伪 dll", func(f map[string]string) { f["TidyDamsASA/TidyDamsASA.dll"] = "just text renamed to dll" }, coreOK, "不是有效的 64 位"},
		{"缺 FullName", func(f map[string]string) { f["TidyDamsASA/PluginInfo.json"] = `{"Version":1}` }, coreOK, "FullName"},
		{"PluginInfo 不是对象", func(f map[string]string) { f["TidyDamsASA/PluginInfo.json"] = `[1,2]` }, coreOK, "PluginInfo.json 无效"},
		{"插件名含逗号", func(f map[string]string) {
			for k, v := range map[string]string{"A,B/PluginInfo.json": `{"FullName":"x"}`, "A,B/A,B.dll": amd64PE, "A,B/A,B.pdb": "p"} {
				f[k] = v
			}
			for k := range f {
				if strings.HasPrefix(k, "TidyDamsASA/") {
					delete(f, k)
				}
			}
		}, coreOK, "非法的插件名"},
		{"expect 不匹配", func(map[string]string) {}, PluginCheck{CoreInstalled: true, Expect: "Permissions"}, "不是要更新的 Permissions"},
		{"主程序未安装", func(map[string]string) {}, PluginCheck{}, "尚未安装 ArkApi 主程序"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := tidyDams()
			c.mutate(files)
			dir, entries := extracted(t, files)
			rep := ValidatePluginPackage(dir, entries, c.chk)
			if !strings.Contains(strings.Join(rep.Errors, "\n"), c.want) {
				t.Fatalf("errors = %v，want 包含 %q", rep.Errors, c.want)
			}
		})
	}
}

func TestValidatePluginFlatAmbiguous(t *testing.T) {
	dir, entries := extracted(t, map[string]string{
		"PluginInfo.json": `{"FullName":"x"}`,
		"A.dll":           amd64PE, "A.pdb": "p",
		"B.dll": amd64PE, "B.pdb": "p",
	})
	rep := ValidatePluginPackage(dir, entries, coreOK)
	if !strings.Contains(strings.Join(rep.Errors, "\n"), "无法确定插件名") {
		t.Fatalf("errors = %v", rep.Errors)
	}
}

func TestValidatePluginWarnings(t *testing.T) {
	files := tidyDams()
	files["TidyDamsASA/PluginInfo.json"] = `{"FullName":"Tidy Dams","MinApiVersion":2.1}`
	files["README.txt"] = "outside the plugin dir"
	dir, entries := extracted(t, files)
	rep := ValidatePluginPackage(dir, entries, PluginCheck{CoreInstalled: true, CoreVersion: "2.03"})
	if len(rep.Errors) != 0 {
		t.Fatalf("警告项不应阻断: %v", rep.Errors)
	}
	joined := strings.Join(rep.Warnings, "\n")
	for _, want := range []string{"FullName", "README.txt", "不低于 2.1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings 缺少 %q: %v", want, rep.Warnings)
		}
	}
	if len(rep.Ignored) != 1 {
		t.Errorf("Ignored = %v", rep.Ignored)
	}
}

// AsaApi_2.03.zip 的结构：文件平铺在 zip 根，附带 Permissions 插件（方案 §3.3）。
func asaApiCore() map[string]string {
	return map[string]string{
		"AsaApiLoader.exe":      amd64PE,
		"AsaApiLoader.pdb":      "pdb",
		"config.json":           `{"settings":{"AutomaticPluginReloading":true}}`,
		"libcrypto-3-x64.dll":   amd64PE,
		"msvcp140.dll":          amd64PE,
		"ArkApi/AsaApi.dll":     amd64PE,
		"ArkApi/AsaApi.pdb":     "pdb",
		"ArkApi/pdbignores.txt": "",
		"ArkApi/Plugins/Permissions/PluginInfo.json": `{"FullName":"Ark:SA Permissions","Version":1.1,"MinApiVersion":1.19}`,
		"ArkApi/Plugins/Permissions/Permissions.dll": amd64PE,
		"ArkApi/Plugins/Permissions/Permissions.pdb": "pdb",
		"ArkApi/Plugins/Permissions/config.json":     `{}`,
		"Lib/AsaApi.lib":                             "lib",
	}
}

func TestValidateCoreAsaApiStructure(t *testing.T) {
	dir, entries := extracted(t, asaApiCore())
	rep := ValidateCorePackage(dir, entries, "AsaApi_2.03.zip")
	if len(rep.Errors) != 0 {
		t.Fatalf("errors = %v", rep.Errors)
	}
	if rep.Version != "2.03" {
		t.Errorf("版本号应从文件名提取，实际 %q", rep.Version)
	}
	if len(rep.Bundled) != 1 {
		t.Fatalf("附带插件 = %d 个", len(rep.Bundled))
	}
	perm := rep.Bundled[0]
	if perm.Name != "Permissions" || len(perm.Errors) != 0 || perm.Version != "1.1" {
		t.Errorf("附带插件 = %+v", perm)
	}
	// 方案 D1：官方包自带的 Permissions 就是 FullName 与目录名不同，只能警告不能拒
	if !strings.Contains(strings.Join(perm.Warnings, "\n"), "Ark:SA Permissions") {
		t.Errorf("附带的 Permissions 应有 FullName 警告: %v", perm.Warnings)
	}
}

func TestValidateCoreNested(t *testing.T) {
	files := map[string]string{}
	for k, v := range asaApiCore() {
		files["AsaApi-2.03/"+k] = v
	}
	files["readme.md"] = "outside"
	dir, entries := extracted(t, files)
	rep := ValidateCorePackage(dir, entries, "whatever.zip")
	if len(rep.Errors) != 0 || rep.root != filepath.Join(dir, "AsaApi-2.03") {
		t.Fatalf("errors=%v root=%s", rep.Errors, rep.root)
	}
	if rep.Version != "" {
		t.Errorf("文件名里没有版本号时应为空，实际 %q", rep.Version)
	}
	if len(rep.Ignored) != 1 || rep.Ignored[0] != "readme.md" {
		t.Errorf("Ignored = %v", rep.Ignored)
	}
}

func TestValidateCoreErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{"没有加载器", func(f map[string]string) { delete(f, "AsaApiLoader.exe") }, "没有 AsaApiLoader.exe"},
		{"两个加载器", func(f map[string]string) { f["copy/AsaApiLoader.exe"] = amd64PE }, "2 个 AsaApiLoader.exe"},
		{"缺 AsaApi.dll", func(f map[string]string) { delete(f, "ArkApi/AsaApi.dll") }, "缺少 ArkApi/AsaApi.dll"},
		{"缺 lib", func(f map[string]string) { delete(f, "Lib/AsaApi.lib") }, "缺少 Lib/AsaApi.lib"},
		{"伪 exe", func(f map[string]string) { f["AsaApiLoader.exe"] = "text" }, "AsaApiLoader.exe 不是有效的 64 位"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := asaApiCore()
			c.mutate(files)
			dir, entries := extracted(t, files)
			rep := ValidateCorePackage(dir, entries, "AsaApi_2.03.zip")
			if !strings.Contains(strings.Join(rep.Errors, "\n"), c.want) {
				t.Fatalf("errors = %v，want 包含 %q", rep.Errors, c.want)
			}
		})
	}
}

// API 版本是十进制小数：2.1 就是 2.10，高于 2.03。按点分段比会把它判成更低，漏掉警告。
func TestCompareAPIVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.1", "2.03", 1},
		{"1.19", "2.03", -1},
		{"2", "2.03", -1},
		{"2.03", "2.030", 0},
		{"x.1", "x.0", 1}, // 不是数字时退回点分比较
	}
	for _, c := range cases {
		if got := compareAPIVersions(c.a, c.b); got != c.want {
			t.Errorf("compareAPIVersions(%q, %q) = %d，want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.10", "1.9", 1},
		{"1.3", "1.3", 0},
		{"1.3", "1.3.0", 0},
		{"2", "2.03", -1},
		{"1.19", "2.03", -1},
		{"1.2b", "1.2a", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d，want %d", c.a, c.b, got, c.want)
		}
	}
}
