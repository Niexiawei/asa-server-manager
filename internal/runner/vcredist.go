package runner

// ArkApi（AsaApiLoader.exe）依赖微软 VC++ 运行时，而 Wine/GE-Proton 的 prefix 里只有
// Wine 自己的同名实现。这个文件是「把它装进 prefix」的纯逻辑部分：下载地址解析、
// 安装成功的判据、DLL override 的 .reg 生成、安装器退出码翻译。
// 落盘/联网/跑 umu-run 在 vcredist_linux.go。
//
// 不加 //go:build linux —— 与 steamrt.go 同理由：全是字符串与字节解析，没有平台专属
// API，不加约束才能在 Windows 上跑单测。
//
// 本文件的每一条做法都对照 winetricks 的 vcrun2022 动词
// （https://github.com/Winetricks/winetricks 的 src/winetricks），而不是自己推导 ——
// 那是这十几年里「在 Wine 里装微软运行时」被踩得最平的一条路。
// 详见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md。

import (
	"bufio"
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// defaultVCRedistURL 是微软的 VS 17（2022 通道）x64 运行时短链。
//
// VC++ 2015/2017/2019/2022 是同一个 14.x 家族、同一个安装包，ArkApi 官方要求的
// 「2019」由它覆盖。只需要 x64：ASA 服务端与 AsaApiLoader.exe 都是 64 位。
const defaultVCRedistURL = "https://aka.ms/vs/17/release/vc_redist.x64.exe"

// vcRedistMarker 记在 prefix **内部**，所以 reconcilePrefixVersion 因换代把整个
// prefix 移走时它自动失效，新 prefix 会重装 —— 不需要额外的失效逻辑。
const vcRedistMarker = ".asa-vcredist"

// vcRedistInstallerName 是安装包在 {BaseDir}/vcredist/ 下的落盘文件名。
const vcRedistInstallerName = "vc_redist.x64.exe"

// vcRedistOverrideRegName 是我们生成的 DLL override 注册表脚本。
const vcRedistOverrideRegName = "dll-overrides.reg"

// vcRedistExitNoDisplay 是「连不上 X 显示」时安装器的退出码（203，
// Windows 的 ERROR_ENVVAR_NOT_FOUND 截断到 8 位后正好也是这个值）。
//
// 真机实测（WSL2 + GE-Proton10-34，2026-08-30）：同一条命令，
// 带 DISPLAY=:0（WSLg 的真实 X 服务）→ 退出 0、DLL 全部换成原生；
// 不带 DISPLAY → 203；DISPLAY=:99（有变量、无人监听）→ 203；
// 禁用 winex11.drv → 203；连 /layout 都是 203。
// 也就是说：**必须有一个真实可连的 X 显示**，这不是环境变量的形式问题。
const vcRedistExitNoDisplay = 203

// --- 下载地址与校验 ---------------------------------------------------------

// msDownloadSHA256Re 匹配微软下载 URL 里那段文件哈希：
//
//	https://download.visualstudio.microsoft.com/download/pr/{guid}/{SHA256}/VC_redist.x64.exe
//
// 实测这一段与文件的 sha256 完全一致（docs/ARKAPI_LINUX_VCREDIST_PLAN.md §1.2，
// winetricks 手工维护的哈希表也是同一个值），所以跟随一次 302 就能白捡一个校验值。
// 这比在代码里钉死哈希好：微软的服务化更新几个月就换一版，钉死等于每次更新都把
// setup 弄挂；也比不校验好 —— 见 LINUX_COMPATIBILITY_PLAN.md §6 风险 17。
var msDownloadSHA256Re = regexp.MustCompile(`/([0-9A-Fa-f]{64})/[^/]+$`)

// sha256FromMSDownloadURL 从最终下载地址里取出文件的 sha256（小写）。
// 自建镜像等地址里没有这一段时返回 false，调用方据此降级为不校验并告警。
func sha256FromMSDownloadURL(rawURL string) (string, bool) {
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

// nativeProbeDLL 是判据实际检查的文件：system32 下的它是不是微软原生 DLL。
//
// ⚠️ 不能用 msvcp140.dll：Wine 内建版本号比微软发的还高，安装器据此判定「已有更新
// 版本」直接跳过它（winehq #57518），所以即便安装完全成功它也仍是 Wine 的。
// 见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §2.5。
const nativeProbeDLL = "vcruntime140.dll"

// wineDLLHeaderScan 是读头部多少字节去找下面那两个标记 —— DOS stub 在 PE 最前面。
const wineDLLHeaderScan = 1 << 10

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

// vcRuntimeRegistryVersion 读出 prefix 注册表里报告的 VC++ 版本，读不到返回 ""。
// 仅供人看：Proton 预置的假值与真实安装的版本长得一模一样，程序无法据此判断。
func vcRuntimeRegistryVersion(systemReg []byte) string {
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

// vcRedistOverrideDLLs 抄自 winetricks 的 load_vcrun2022，含 x64 独有的
// vcruntime140_1（上游在 win64 分支里单独声明）。顺序保持与上游一致便于对拍。
var vcRedistOverrideDLLs = []string{
	"concrt140", "msvcp140", "msvcp140_1", "msvcp140_2",
	"msvcp140_atomic_wait", "msvcp140_codecvt_ids",
	"vcamp140", "vccorlib140", "vcomp140", "vcruntime140",
	"vcruntime140_1",
}

// vcRedistOverrideMode：原生优先、内建兜底。用 native,builtin 而不是硬 native，
// 是因为 msvcp140/msvcp140_2 已知装不进去（§2.5）—— 有 builtin 兜着，它们退化成
// 「用 Wine 的 C++ 标准库」，而不是「找不到 DLL」。
const vcRedistOverrideMode = "native,builtin"

// buildVCRedistOverrideReg 生成与 winetricks w_override_dlls 等价的注册表脚本。
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
func buildVCRedistOverrideReg() string {
	var b strings.Builder
	b.WriteString("REGEDIT4\r\n\r\n")
	b.WriteString(`[HKEY_CURRENT_USER\Software\Wine\DllOverrides]`)
	b.WriteString("\r\n")
	for _, dll := range vcRedistOverrideDLLs {
		fmt.Fprintf(&b, "\"*%s\"=\"%s\"\r\n", dll, vcRedistOverrideMode)
	}
	return b.String()
}

// --- 安装器退出码 -------------------------------------------------------------

// msInstallerExitNote 把安装器退出码翻成人话，**只用于给判决加注脚**，判决本身由
// nativeVCRuntimeInRegistry / isWineOwnDLL 下 —— 与 warmPrefix 对 wineboot 的
// exitNote 是同一个模式。
//
// 用的是 winetricks w_try_ms_installer 那张表，也就是**被截断到低 8 位之后**的值：
// Linux 的 wait(2) 只给 8 位，Windows 那套 3010/1638 在这里永远不会原样出现
// （194 == 3010 & 0xFF）。
func msInstallerExitNote(code int) string {
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
	case vcRedistExitNoDisplay:
		return "（安装器退出码 203：几乎可以肯定是没有可用的 X 显示 —— " +
			"vc_redist 的 WiX Burn 引导器即使带 /quiet 也要初始化 UI 子系统，" +
			"连不上 X 就以这个码退出。见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §2.6）"
	case -1:
		return "（安装器没有正常结束，可能已超时被杀）"
	default:
		return fmt.Sprintf("（安装器退出码 %d）", code)
	}
}

// msInstallerExitOK 报告退出码是否属于 winetricks 认可的非致命集合。
func msInstallerExitOK(code int) bool {
	switch code {
	case 0, 105, 194, 236:
		return true
	}
	return false
}
