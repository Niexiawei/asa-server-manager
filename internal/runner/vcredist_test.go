package runner

import (
	"fmt"
	"strings"
	"testing"
)

// realMSDownloadURL 是 aka.ms/vs/17/release/vc_redist.x64.exe 跟随重定向后的真实地址
// （2026-08 实测，14.44.35211.0）。路径倒数第二段就是文件的 sha256 —— winetricks 手工
// 维护的哈希表里同一版本也是这个值。见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §1.2。
const realMSDownloadURL = "https://download.visualstudio.microsoft.com/download/pr/" +
	"9d270333-8b7b-4f96-9458-6fcdb2ec0b25/" +
	"CC0FF0EB1DC3F5188AE6300FAEF32BF5BEEBA4BDD6E8E445A9184072096B713B/VC_redist.x64.exe"

const realMSDownloadSHA256 = "cc0ff0eb1dc3f5188ae6300faef32bf5beeba4bdd6e8e445a9184072096b713b"

func TestSHA256FromMSDownloadURL(t *testing.T) {
	got, ok := sha256FromMSDownloadURL(realMSDownloadURL)
	if !ok {
		t.Fatal("真实下载地址里应当能抠出 sha256")
	}
	if got != realMSDownloadSHA256 {
		t.Errorf("got %q, want %q", got, realMSDownloadSHA256)
	}
}

func TestSHA256FromMSDownloadURLRejects(t *testing.T) {
	hex64 := strings.Repeat("a", 64)
	for name, u := range map[string]string{
		"短链本身（无哈希段）": defaultVCRedistURL,
		"自建镜像":       "https://mirror.example.com/vcredist/vc_redist.x64.exe",
		"哈希段少一位":     "https://x/download/pr/guid/" + strings.Repeat("a", 63) + "/VC_redist.x64.exe",
		"非十六进制":      "https://x/download/pr/guid/" + strings.Repeat("z", 64) + "/VC_redist.x64.exe",
		"哈希在末段而非倒数第二": "https://x/download/pr/guid/" + hex64,
		"空":          "",
	} {
		if got, ok := sha256FromMSDownloadURL(u); ok {
			t.Errorf("%s: 不应抠出哈希，实际得到 %q（url=%q）", name, got, u)
		}
	}
}

func TestSHA256FromMSDownloadURLIgnoresQuery(t *testing.T) {
	// 查询串不该影响「倒数第二段」的判断
	got, ok := sha256FromMSDownloadURL(realMSDownloadURL + "?foo=bar")
	if !ok || got != realMSDownloadSHA256 {
		t.Errorf("got (%q, %v)", got, ok)
	}
}

// --- 注册表判据 ---------------------------------------------------------------

// wineSystemReg 复刻 Wine system.reg 的形状：段名里的反斜杠是转义过的，段头 ']'
// 之后还跟着时间戳。
func wineSystemReg(sections ...string) string {
	return "WINE REGISTRY Version 2\n\n" + strings.Join(sections, "\n")
}

func regSection(path string, body ...string) string {
	return fmt.Sprintf("[%s] 1756000000\n#time=1dcf000000000000\n%s\n", path, strings.Join(body, "\n"))
}

// freshProtonPrefixReg 复刻真机上 GE-Proton10-34 建出来的**全新** prefix 的
// system.reg 片段（2026-08-30 实测，WSL2）。注意它已经带着
// "Installed"=dword:00000001 —— Wine/Proton 主动伪造这个键，好让游戏自带的安装器
// 别去装 VC++。这正是「不能拿注册表当判据」的证据。
const freshProtonPrefixReg = `WINE REGISTRY Version 2

[Software\\Microsoft\\VisualStudio\\14.0\\VC\\Runtimes\\x64] 1774238072
#time=1dcba78c2166f4c
"Bld"=dword:00008681
"Installed"=dword:00000001
"Major"=dword:0000000e
"Minor"=dword:0000002a
"Rbld"=dword:00000000
"Version"="14.42.34433.0"

[Software\\Wow6432Node\\Microsoft\\VisualStudio\\14.0\\VC\\Runtimes\\x64] 1774238073
#time=1dcba78c28e104c
"Installed"=dword:00000001
"Version"="9.9.9.9"
`

func TestVCRuntimeRegistryVersion(t *testing.T) {
	// 取的是原生 x64 视图，不是 Wow6432Node 那一份
	if got := vcRuntimeRegistryVersion([]byte(freshProtonPrefixReg)); got != "14.42.34433.0" {
		t.Errorf("vcRuntimeRegistryVersion() = %q, want 14.42.34433.0", got)
	}

	const x86Only = `Software\\Wow6432Node\\Microsoft\\VisualStudio\\14.0\\VC\\Runtimes\\x86`
	for name, reg := range map[string]string{
		"空文件":     "",
		"只有 x86 段": wineSystemReg(regSection(x86Only, `"Version"="14.0.0.0"`)),
		"不相干的注册表": wineSystemReg(regSection(`Software\\Foo`, `"Version"="1.2.3"`)),
		"x64 段里没有 Version": wineSystemReg(regSection(
			`Software\\Microsoft\\VisualStudio\\14.0\\VC\\Runtimes\\x64`, `"Installed"=dword:00000001`)),
	} {
		if got := vcRuntimeRegistryVersion([]byte(reg)); got != "" {
			t.Errorf("%s: 应返回空，实际 %q", name, got)
		}
	}
}

// 这条测试守的是一个已经犯过一次的错：曾经把「注册表里有 Installed=1」当作
// 「原生运行时已就位」的主判据，而 GE-Proton 的全新 prefix 里它本来就是 1 ——
// 结果是永远认为已装好、于是永远不装。判据必须只看 PE 头。
func TestFreshProtonPrefixIsNotConsideredInstalled(t *testing.T) {
	if v := vcRuntimeRegistryVersion([]byte(freshProtonPrefixReg)); v == "" {
		t.Fatal("前提不成立：全新 prefix 的注册表里本来就有版本号")
	}
	// 判据函数不接受注册表输入 —— 这本身就是设计上的防线。
	// 真正的判据走 isWineOwnDLL，见 TestIsWineOwnDLL。
	wineHeader := append([]byte("MZ\x90\x00"), []byte("Wine builtin DLL")...)
	if !isWineOwnDLL(wineHeader) {
		t.Error("全新 prefix 的 system32 里探针 DLL 应当被判为 Wine 自产")
	}
}

// --- Wine 自产 PE 的标记 ---------------------------------------------------------

func TestIsWineOwnDLL(t *testing.T) {
	// DOS stub 大意如此：MZ 头 + 一段明文
	placeholder := append([]byte("MZ\x90\x00"), []byte("Wine placeholder DLL")...)
	builtin := append([]byte("MZ\x90\x00"), []byte("Wine builtin DLL")...)
	native := append([]byte("MZ\x90\x00"),
		[]byte("This program cannot be run in DOS mode.")...)

	if !isWineOwnDLL(placeholder) {
		t.Error("占位 DLL 应判为 Wine 自产")
	}
	if !isWineOwnDLL(builtin) {
		t.Error("内建 DLL 应判为 Wine 自产")
	}
	if isWineOwnDLL(native) {
		t.Error("原生 DLL 不应判为 Wine 自产")
	}
	if isWineOwnDLL(nil) {
		t.Error("空内容不应判为 Wine 自产")
	}
}

// --- DLL override ---------------------------------------------------------------

func TestBuildVCRedistOverrideReg(t *testing.T) {
	got := buildVCRedistOverrideReg()

	if !strings.HasPrefix(got, "REGEDIT4\r\n") {
		t.Error("缺少 REGEDIT4 头")
	}
	if !strings.Contains(got, `[HKEY_CURRENT_USER\Software\Wine\DllOverrides]`) {
		t.Error("段名不对：Wine 的 DLL override 在 HKCU\\Software\\Wine\\DllOverrides")
	}

	for _, dll := range vcRedistOverrideDLLs {
		// '*' 前缀是这套东西最容易漏、又最难从现象反推的失效方式：
		// 不带它时，用绝对路径 LoadLibrary 的 DLL 不受 override 影响。
		want := fmt.Sprintf("\"*%s\"=\"native,builtin\"\r\n", dll)
		if !strings.Contains(got, want) {
			t.Errorf("缺少（或写错）override 行: %q", strings.TrimSpace(want))
		}
	}

	// 行数 = REGEDIT4 + 空行 + 段头 + 11 条
	if n := strings.Count(got, "\r\n"); n != len(vcRedistOverrideDLLs)+3 {
		t.Errorf("行数 %d 与预期不符，是不是多写/漏写了条目", n)
	}
}

// vcrun2022 的模块清单必须与 winetricks 一致 —— 漂移了就意味着我们和上游的判断分叉。
func TestVCRedistOverrideDLLsMatchWinetricks(t *testing.T) {
	// 抄自 winetricks load_vcrun2022 的两次 w_override_dlls 声明
	// （第二次在 win64 分支里，只加 vcruntime140_1）。
	want := []string{
		"concrt140", "msvcp140", "msvcp140_1", "msvcp140_2",
		"msvcp140_atomic_wait", "msvcp140_codecvt_ids",
		"vcamp140", "vccorlib140", "vcomp140", "vcruntime140",
		"vcruntime140_1",
	}
	if len(vcRedistOverrideDLLs) != len(want) {
		t.Fatalf("模块数 %d != %d", len(vcRedistOverrideDLLs), len(want))
	}
	for i := range want {
		if vcRedistOverrideDLLs[i] != want[i] {
			t.Errorf("第 %d 项 = %q, want %q", i, vcRedistOverrideDLLs[i], want[i])
		}
	}
}

// --- 安装器退出码 -------------------------------------------------------------

func TestMSInstallerExitOK(t *testing.T) {
	// winetricks w_try_ms_installer 的非致命集合。注意这些是**截断到低 8 位之后**的
	// 值：Linux 的 wait(2) 只给 8 位，Windows 的 3010 在这里是 194。
	for _, code := range []int{0, 105, 194, 236} {
		if !msInstallerExitOK(code) {
			t.Errorf("退出码 %d 应判为非致命", code)
		}
	}
	for _, code := range []int{5, 1, -1, 1638, 3010, vcRedistExitNoDisplay} {
		if msInstallerExitOK(code) {
			t.Errorf("退出码 %d 不应判为非致命", code)
		}
	}
}

// 203 是真机上最常见的失败码，而它的成因（Wine 下连不上 X 显示）从码本身完全看不出来。
// 文案里必须带着解释，否则用户只能看到一个裸数字。
func TestNoDisplayExitNoteExplainsItself(t *testing.T) {
	note := msInstallerExitNote(vcRedistExitNoDisplay)
	for _, want := range []string{"203", "X 显示"} {
		if !strings.Contains(note, want) {
			t.Errorf("203 的文案里应当出现 %q，实际: %s", want, note)
		}
	}
}

func TestMSInstallerExitNote(t *testing.T) {
	// 每个已知码都要有专属文案，不能全落到 default
	seen := map[string]bool{}
	for _, code := range []int{0, 5, 105, 194, 236, -1, vcRedistExitNoDisplay} {
		note := msInstallerExitNote(code)
		if note == "" {
			t.Errorf("退出码 %d 没有文案", code)
		}
		if seen[note] {
			t.Errorf("退出码 %d 的文案与前面的重复: %q", code, note)
		}
		seen[note] = true
	}
	if got := msInstallerExitNote(42); !strings.Contains(got, "42") {
		t.Errorf("未知退出码的文案里应带上码值，got %q", got)
	}
}
