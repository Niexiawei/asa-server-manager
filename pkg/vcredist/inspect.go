package vcredist

import (
	"io"
	"os"
	"path/filepath"
)

// 本文件是「给一个 Wine 前缀的路径，读几个文件，交给 vcredist.go 的纯逻辑判断」
// 那一层。判据本身（PE 头部的 Wine 标记、user.reg 里的 override 计数、system.reg
// 里的版本号）都在 vcredist.go，这里只负责把字节读进来。
//
// 与 vcredist.go 一样**不带 build tag**：全是普通文件读取，可以在任何平台上单测。
// 它认识的「前缀」只是一个 Wine 目录布局，不认识实例、不认识 ASA ——
// 前缀路径由调用方（internal/runner，经 pkg/wineprefix 解析）传进来。

// System32 拼出 prefix 里 64 位 system32 下某个文件的路径。
// （win64 prefix 下 system32 是 64 位、syswow64 才是 32 位，别记反。）
func System32(prefix, name string) string {
	return filepath.Join(prefix, "drive_c", "windows", "system32", name)
}

// ClassifyFile 判定一个 DLL 文件的出身：读头部字节，交给 ClassifyHeader 分类。
// 读不到（不存在、没权限）一律算 DLLMissing。
func ClassifyFile(path string) DLLOrigin {
	f, err := os.Open(path)
	if err != nil {
		return DLLMissing
	}
	defer f.Close()

	buf := make([]byte, HeaderScanBytes)
	n, err := io.ReadFull(f, buf)
	// 文件比 HeaderScanBytes 短时 ReadFull 返回 ErrUnexpectedEOF，但 n 是有效的。
	if n == 0 && err != nil {
		return DLLMissing
	}
	return ClassifyHeader(buf[:n])
}

// InstalledIn 只读判断某个 prefix 里有没有微软原生 VC++ 运行时。
// 不联网、不改动，可以放心在实例启动这种热路径上调。
//
// 判据只有一条：system32 下的探针 DLL 的 DOS stub 里没有 Wine 标记。
// 注册表那个「标准检测键」**不能用** —— GE-Proton 在全新 prefix 里就把它伪造好了，
// 见 vcRuntimeRegSection 的注释。
func InstalledIn(prefix string) bool {
	return ClassifyFile(System32(prefix, ProbeDLL)) == DLLNative
}

// OverridesApplied 报告某个 prefix 的 user.reg 里那批 DLL override 是否已经齐了。
// **只读一个文件**，所以可以放在实例启动这种热路径上。
//
// 它与 InstalledIn 判的不是同一件事，别混用：后者看 system32 里有没有微软原生 DLL
// （= vc_redist 安装器跑成功过），而安装器在没有图形显示的机器上**永远装不上**
// （退出码 203，见 ExitNoDisplay）。拿它当「要不要再试一次」的判据，会让无头机每次
// 启动都重跑一遍安装流程 —— 那里面有一次 regedit 容器启动，好几秒。
// override 才是承重的那一环，也是无头可用的那一环，所以由它当判据。
func OverridesApplied(prefix string) bool {
	data, err := os.ReadFile(filepath.Join(prefix, "user.reg"))
	if err != nil {
		return false
	}
	return CountOverrides(data) >= len(OverrideDLLs)
}

// Inspect 汇总一个 prefix 的 VC++ 运行时现状。只读，不联网。
// gameDir 传游戏 exe 所在目录（为空则跳过 InGameDir 那一列）。
//
// 不填 Managed / InstallerDisplay / InstallerBlocked 三个字段：前者是调用方的运行时
// 选型（本包不认识「custom 运行时」这回事），后两者要问显示候选链 —— 而「诊断视图
// 只问计划、绝不动手拉起 X 服务端」是调用方那一侧的业务规则，不该由本包代劳。
func Inspect(prefix, gameDir string) Info {
	info := Info{
		Prefix:        prefix,
		Installed:     InstalledIn(prefix),
		ProbeDLL:      ProbeDLL,
		WantOverrides: len(OverrideDLLs),
	}

	if data, err := os.ReadFile(filepath.Join(prefix, "system.reg")); err == nil {
		info.RegistryVersion = RegistryVersion(data)
	}
	if data, err := os.ReadFile(filepath.Join(prefix, "user.reg")); err == nil {
		info.OverridesSet = CountOverrides(data)
	}

	for _, name := range OverrideDLLs {
		d := DLLInfo{Name: name + ".dll"}
		d.InSystem32 = ClassifyFile(System32(prefix, d.Name))
		if gameDir != "" {
			d.InGameDir = ClassifyFile(filepath.Join(gameDir, d.Name))
		}
		info.DLLs = append(info.DLLs, d)
	}
	return info
}
