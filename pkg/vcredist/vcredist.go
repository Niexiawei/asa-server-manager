// Package vcredist holds the pure logic for installing Microsoft's VC++
// runtime into a Wine prefix: download-URL/checksum resolution, the
// installed-vs-Wine-builtin judgement, DLL-override .reg generation, and
// installer exit-code translation. It does not know about umu, prefixes, or
// how to actually execute a Windows exe — that mechanism (running something
// via umu-run, resolving a display, dropping to a runtime user) is supplied
// by the caller, since this package would otherwise have to depend on
// pkg/umu/pkg/wineprefix/pkg/xvfb just to describe "run this .exe".
//
// Every judgement here is checked against winetricks' vcrun2022 verb
// (https://github.com/Winetricks/winetricks src/winetricks) rather than
// derived from scratch — that is the most-battle-tested path for "install a
// Microsoft runtime into Wine" there is. See docs/ARKAPI_LINUX_VCREDIST_PLAN.md.
//
// Deliberately free of a //go:build tag — like pkg/steamrt, this is all
// string/byte parsing with no platform-specific API, so it can be unit
// tested on any host.
package vcredist

import (
	"bufio"
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// DefaultURL is Microsoft's VS 17 (2022 channel) x64 runtime short link.
//
// VC++ 2015/2017/2019/2022 are the same 14.x family, the same installer
// package — the "2019" ArkApi's docs ask for is covered by it. x64 only:
// both the ASA dedicated server and AsaApiLoader.exe are 64-bit.
const DefaultURL = "https://aka.ms/vs/17/release/vc_redist.x64.exe"

// MarkerFileName is recorded **inside** the prefix, so a prefix-generation
// bump that moves the whole prefix away invalidates it automatically — no
// extra invalidation logic needed.
const MarkerFileName = ".asa-vcredist"

// InstallerName is the installer's on-disk file name under the caller's
// download directory.
const InstallerName = "vc_redist.x64.exe"

// OverrideRegName is the generated DLL-override registry script's file name.
const OverrideRegName = "dll-overrides.reg"

// ExitNoDisplay is the installer's exit code when it can't connect to an X
// display (203 — Windows' ERROR_ENVVAR_NOT_FOUND truncated to 8 bits happens
// to be the same value).
//
// Measured on real hardware (WSL2 + GE-Proton10-34, 2026-08-30): the same
// command, with DISPLAY=:0 (WSLg's real X server) → exits 0, every DLL
// replaced with native; without DISPLAY → 203; DISPLAY=:99 (set but nobody
// listening) → 203; winex11.drv disabled → 203; even /layout → 203. In other
// words: a genuinely connectable X display is required — this is not an
// environment-variable formality.
const ExitNoDisplay = 203

// --- 下载地址与校验 ---------------------------------------------------------

// msDownloadSHA256Re 匹配微软下载 URL 里那段文件哈希：
//
//	https://download.visualstudio.microsoft.com/download/pr/{guid}/{SHA256}/VC_redist.x64.exe
//
// 实测这一段与文件的 sha256 完全一致（docs/ARKAPI_LINUX_VCREDIST_PLAN.md §1.2，
// winetricks 手工维护的哈希表也是同一个值），所以跟随一次 302 就能白捡一个校验值。
// 这比在代码里钉死哈希好：微软的服务化更新几个月就换一版，钉死等于每次更新都把
// setup 弄挂；也比不校验好。
var msDownloadSHA256Re = regexp.MustCompile(`/([0-9A-Fa-f]{64})/[^/]+$`)

// SHA256FromDownloadURL 从最终下载地址里取出文件的 sha256（小写）。
// 自建镜像等地址里没有这一段时返回 false，调用方据此降级为不校验并告警。
func SHA256FromDownloadURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	m := msDownloadSHA256Re.FindStringSubmatch(u.Path)
	if m == nil {
		return "", false
	}
	return strings.ToLower(m[1]), true
}

// --- 安装成功的判据 ---------------------------------------------------------

// ProbeDLL 是判据实际检查的文件：system32 下的它是不是微软原生 DLL。
//
// ⚠️ 不能用 msvcp140.dll：Wine 内建版本号比微软发的还高，安装器据此判定「已有更新
// 版本」直接跳过它（winehq #57518），所以即便安装完全成功它也仍是 Wine 的。
const ProbeDLL = "vcruntime140.dll"

// HeaderScanBytes 是读头部多少字节去找 Wine 的 DOS stub 标记 —— DOS stub 在 PE
// 最前面。
const HeaderScanBytes = 1 << 10

// wineDLLMarkers 是 Wine 写在自己生成的 PE 的 DOS stub 里的明文标记。
// 真机实测（WSL2 + GE-Proton10-34）：全新 prefix 的 system32 下这几个 DLL 全部命中
// "Wine builtin DLL"。
//
// **不用文件体积做判据**，实测数据直接判了死刑：Wine 内建的 msvcp140.dll 是
// 1,843,959 字节，比微软原生的 553,552 字节还大得多；concrt140 更是两边都在 320KB
// 上下。PE 化之后的 Wine 内建 DLL 是真代码，任何阈值都划不出界。
var wineDLLMarkers = [][]byte{
	[]byte("Wine placeholder DLL"), // wineboot 铺进 prefix 的占位
	[]byte("Wine builtin DLL"),     // 真正的内建模块
}

// isWineOwnDLL 判断一段 PE 头部是不是 Wine 自己的产物（而非微软原生 DLL）。
func isWineOwnDLL(header []byte) bool {
	for _, marker := range wineDLLMarkers {
		if bytes.Contains(header, marker) {
			return true
		}
	}
	return false
}

// DLLOrigin says where a DLL in a Wine prefix came from.
type DLLOrigin string

const (
	DLLMissing DLLOrigin = "missing"
	DLLWine    DLLOrigin = "wine"   // Wine 自己的占位/内建 PE
	DLLNative  DLLOrigin = "native" // 微软原生
)

// ClassifyHeader judges a DLL's origin from its first HeaderScanBytes bytes
// (or fewer, for a short file). The caller does the actual file I/O — this
// package only classifies bytes already in hand.
func ClassifyHeader(header []byte) DLLOrigin {
	if len(header) == 0 {
		return DLLMissing
	}
	if isWineOwnDLL(header) {
		return DLLWine
	}
	return DLLNative
}

// --- 注册表：只能当诊断，不能当判据 ---------------------------------------------

// vcRuntimeRegSection 是 VC++ 2015-2022 x64 运行时在 Windows 上的标准检测键
// （HKLM\SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64）。Wine 把它明文写进
// system.reg，段名里的反斜杠是转义过的，所以这里是两个。
//
// ⚠️ **它不能用来判断我们装没装。** 真机实测：GE-Proton10-34 建出来的**全新** prefix
// 里这一节就已经存在，且 "Installed"=dword:00000001、"Version"="14.42.34433.0" ——
// Wine/Proton 主动伪造这个键，好让游戏自带的安装器别去装 VC++。拿它当判据的结果是
// 「永远认为已装好，于是永远不装」。这里只留下读版本号的能力，给诊断输出用。
const vcRuntimeRegSection = `VisualStudio\\14.0\\VC\\Runtimes\\x64]`

var vcRuntimeVersionRe = regexp.MustCompile(`(?i)^"Version"\s*=\s*"([^"]*)"$`)

// RegistryVersion 读出 prefix 注册表里报告的 VC++ 版本，读不到返回 ""。
// 仅供人看：Proton 预置的假值与真实安装的版本长得一模一样，程序无法据此判断。
func RegistryVersion(systemReg []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(systemReg))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	inSection := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			// Wine 的段头形如 `[Software\\...\\Runtimes\\x64] 1774238072`
			// —— ']' 后面还跟着时间戳，所以只能用 Contains 而非 HasSuffix。
			// 同时要排掉 Wow6432Node 那一份（32 位视图，不是我们关心的）。
			inSection = strings.Contains(line, vcRuntimeRegSection) &&
				!strings.Contains(line, `Wow6432Node`)
			continue
		}
		if inSection {
			if m := vcRuntimeVersionRe.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// --- DLL override ------------------------------------------------------------

// OverrideDLLs 抄自 winetricks 的 load_vcrun2022，含 x64 独有的
// vcruntime140_1（上游在 win64 分支里单独声明）。顺序保持与上游一致便于对拍。
var OverrideDLLs = []string{
	"concrt140", "msvcp140", "msvcp140_1", "msvcp140_2",
	"msvcp140_atomic_wait", "msvcp140_codecvt_ids",
	"vcamp140", "vccorlib140", "vcomp140", "vcruntime140",
	"vcruntime140_1",
}

// OverrideMode：原生优先、内建兜底。用 native,builtin 而不是硬 native，
// 是因为 msvcp140/msvcp140_2 已知装不进去 —— 有 builtin 兜着，它们退化成
// 「用 Wine 的 C++ 标准库」，而不是「找不到 DLL」。
const OverrideMode = "native,builtin"

// BuildOverrideReg 生成与 winetricks w_override_dlls 等价的注册表脚本。
//
// 值名的 '*' 前缀**不能省**。winetricks 的原注释：
//
//	# Note: if you want to override even DLLs loaded with an absolute
//	# path, you need to add an asterisk:
//
// ArkApi 的注入代码很可能正是用绝对路径 LoadLibrary，漏了星号则 override 不生效，
// 而且从现象几乎反推不出原因。
//
// 用 CRLF 与 REGEDIT4 头：regedit.exe 认的是 Windows 那套。
func BuildOverrideReg() string {
	var b strings.Builder
	b.WriteString("REGEDIT4\r\n\r\n")
	b.WriteString(`[HKEY_CURRENT_USER\Software\Wine\DllOverrides]`)
	b.WriteString("\r\n")
	for _, dll := range OverrideDLLs {
		fmt.Fprintf(&b, "\"*%s\"=\"%s\"\r\n", dll, OverrideMode)
	}
	return b.String()
}

// CountOverrides 数 user.reg 的 DllOverrides 段里有几条是我们写的
// "*<dll>"="native,builtin"。
func CountOverrides(userReg []byte) int {
	text := string(userReg)
	n := 0
	for _, dll := range OverrideDLLs {
		if strings.Contains(text, fmt.Sprintf("\"*%s\"=\"%s\"", dll, OverrideMode)) {
			n++
		}
	}
	return n
}

// --- 安装器退出码 -------------------------------------------------------------

// ExitNote 把安装器退出码翻成人话，**只用于给判决加注脚**，判决本身由
// ClassifyHeader 下——与 warmPrefix 对 wineboot 的 exitNote 是同一个模式。
//
// 用的是 winetricks w_try_ms_installer 那张表，也就是**被截断到低 8 位之后**的值：
// Linux 的 wait(2) 只给 8 位，Windows 那套 3010/1638 在这里永远不会原样出现
// （194 == 3010 & 0xFF）。
func ExitNote(code int) string {
	switch code {
	case 0:
		return "（安装器正常退出）"
	case 105:
		return "（安装器退出码 105：正常，选择了立即重启）"
	case 194:
		return "（安装器退出码 194：正常，选择了稍后重启；即 Windows 的 3010 截断到 8 位）"
	case 236:
		return "（安装器退出码 236：检测到已安装更新的版本）"
	case 5:
		return "（安装器退出码 5：安装被取消）"
	case ExitNoDisplay:
		return "（安装器退出码 203：几乎可以肯定是没有可用的 X 显示 —— " +
			"vc_redist 的 WiX Burn 引导器即使带 /quiet 也要初始化 UI 子系统，" +
			"连不上 X 就以这个码退出。见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §2.6）"
	case -1:
		return "（安装器没有正常结束，可能已超时被杀）"
	default:
		return fmt.Sprintf("（安装器退出码 %d）", code)
	}
}

// ExitOK 报告退出码是否属于 winetricks 认可的非致命集合。
func ExitOK(code int) bool {
	switch code {
	case 0, 105, 194, 236:
		return true
	}
	return false
}

// DLLInfo is one override DLL's classification, in system32 and (optionally)
// the game's own directory.
type DLLInfo struct {
	Name       string
	InSystem32 DLLOrigin
	InGameDir  DLLOrigin // empty when no game dir was supplied
}

// Info summarises a prefix's VC++ runtime state, for a `verify-arkapi`-style
// diagnostic view.
type Info struct {
	Managed          bool
	Prefix           string
	Installed        bool
	ProbeDLL         string
	RegistryVersion  string
	OverridesSet     int
	WantOverrides    int
	InstallerDisplay string
	InstallerBlocked string
	DLLs             []DLLInfo
}
