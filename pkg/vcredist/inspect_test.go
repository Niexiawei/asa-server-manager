package vcredist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDLL 在 prefix 的 system32 下造一个内容为 body 的假 DLL。
func writeDLL(t *testing.T, prefix, name, body string) {
	t.Helper()
	path := System32(prefix, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestClassifyFileMissing: 读不到的文件一律 DLLMissing —— 判据不能因为「没权限读」
// 就把一个 Wine 内建 DLL 当成原生的，那会让安装流程以为已经装好了。
func TestClassifyFileMissing(t *testing.T) {
	if got := ClassifyFile(filepath.Join(t.TempDir(), "nope.dll")); got != DLLMissing {
		t.Errorf("ClassifyFile(不存在) = %q, want %q", got, DLLMissing)
	}
}

// TestClassifyFileShorterThanHeaderScan: 比 HeaderScanBytes 短的文件仍要判得出来。
// io.ReadFull 在这种情况下返回 ErrUnexpectedEOF，但读到的 n 字节是有效的 ——
// 早期版本在这里直接返回 DLLMissing，于是一个 200 字节的占位文件会被当成「没装」，
// 每次启动都重跑一遍安装。
func TestClassifyFileShorterThanHeaderScan(t *testing.T) {
	dir := t.TempDir()
	short := filepath.Join(dir, "short.dll")
	if err := os.WriteFile(short, []byte("MZ...Wine builtin DLL..."), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ClassifyFile(short); got != DLLWine {
		t.Errorf("ClassifyFile(短文件) = %q, want %q", got, DLLWine)
	}
}

// TestInstalledInJudgesTheProbeDLL: 判据是 system32 下探针 DLL 的出身，
// 而**不是**注册表 —— GE-Proton 在全新 prefix 里就把那个「标准检测键」伪造好了
// （见 TestFreshProtonPrefixIsNotConsideredInstalled）。
func TestInstalledInJudgesTheProbeDLL(t *testing.T) {
	prefix := t.TempDir()
	if InstalledIn(prefix) {
		t.Error("空 prefix 被判成已装")
	}

	writeDLL(t, prefix, ProbeDLL, "MZ\x90\x00 this is a Wine builtin DLL placeholder")
	if InstalledIn(prefix) {
		t.Error("Wine 内建的探针 DLL 被判成微软原生")
	}

	writeDLL(t, prefix, ProbeDLL, "MZ\x90\x00 Microsoft Corporation, no wine marker here")
	if !InstalledIn(prefix) {
		t.Error("原生探针 DLL 没被认出来")
	}
}

// TestOverridesAppliedCountsTheWholeSet: override 是承重的那一环，判据必须是
// **全都齐了**。少一条就当没写，否则一次中途失败的导入会被记成成功。
func TestOverridesAppliedCountsTheWholeSet(t *testing.T) {
	prefix := t.TempDir()
	if OverridesApplied(prefix) {
		t.Error("没有 user.reg 却报 override 已齐")
	}

	// 只写一半。
	var half strings.Builder
	half.WriteString("[Software\\\\Wine\\\\DllOverrides]\n")
	for _, name := range OverrideDLLs[:len(OverrideDLLs)/2] {
		half.WriteString("\"" + name + "\"=\"" + OverrideMode + "\"\n")
	}
	if err := os.WriteFile(filepath.Join(prefix, "user.reg"), []byte(half.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if OverridesApplied(prefix) {
		t.Errorf("只写了 %d/%d 条 override 却报已齐", len(OverrideDLLs)/2, len(OverrideDLLs))
	}

	// 写全（直接用生产代码生成的那份，顺带钉住两者格式一致）。
	if err := os.WriteFile(filepath.Join(prefix, "user.reg"), []byte(BuildOverrideReg()), 0o644); err != nil {
		t.Fatal(err)
	}
	if !OverridesApplied(prefix) {
		t.Error("BuildOverrideReg 生成的 .reg 自己都过不了 OverridesApplied")
	}
}

// TestInspectLeavesCallerFieldsAlone: Managed 与两个 Installer* 字段归调用方 ——
// 前者是运行时选型，后两者要问显示候选链，而「诊断视图只问计划、绝不拉起 X 服务端」
// 是调用方那一侧的规则。Inspect 擅自填了就等于替调用方做了那个决定。
func TestInspectLeavesCallerFieldsAlone(t *testing.T) {
	info := Inspect(t.TempDir(), "")
	if info.Managed || info.InstallerDisplay != "" || info.InstallerBlocked != "" {
		t.Errorf("Inspect 填了本该归调用方的字段：%+v", info)
	}
}

// TestInspectReportsEveryOverrideDLL: 诊断视图要把 11 个 DLL 全列出来，
// 每个都带 system32 那一列；gameDir 为空时不去猜游戏目录那一列。
func TestInspectReportsEveryOverrideDLL(t *testing.T) {
	prefix := t.TempDir()
	writeDLL(t, prefix, ProbeDLL, "MZ\x90\x00 Wine builtin DLL")

	info := Inspect(prefix, "")
	if len(info.DLLs) != len(OverrideDLLs) {
		t.Fatalf("Inspect 报了 %d 个 DLL，want %d", len(info.DLLs), len(OverrideDLLs))
	}
	if info.WantOverrides != len(OverrideDLLs) {
		t.Errorf("WantOverrides = %d, want %d", info.WantOverrides, len(OverrideDLLs))
	}
	if info.Prefix != prefix || info.ProbeDLL != ProbeDLL {
		t.Errorf("Inspect 没如实回填 Prefix/ProbeDLL：%+v", info)
	}
	for _, d := range info.DLLs {
		if d.InGameDir != "" {
			t.Errorf("gameDir 为空却报了游戏目录那一列：%+v", d)
		}
		if d.Name == ProbeDLL && d.InSystem32 != DLLWine {
			t.Errorf("探针 DLL 的 system32 一列 = %q, want %q", d.InSystem32, DLLWine)
		}
	}
}

// TestInspectReadsGameDirWhenGiven: ARK 服务端自己在 exe 同目录带了 11 个里的 9 个
// 原生 DLL —— 那一列正是「为什么 override 才是承重项」的证据，不能漏读。
func TestInspectReportsGameDirWhenGiven(t *testing.T) {
	prefix := t.TempDir()
	gameDir := t.TempDir()
	native := ProbeDLL
	if err := os.WriteFile(filepath.Join(gameDir, native),
		[]byte("MZ\x90\x00 Microsoft Corporation"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, d := range Inspect(prefix, gameDir).DLLs {
		if d.Name != native {
			continue
		}
		if d.InGameDir != DLLNative {
			t.Errorf("游戏目录里的原生 %s 被判成 %q", native, d.InGameDir)
		}
		if d.InSystem32 != DLLMissing {
			t.Errorf("prefix 里没有 %s，却报 %q", native, d.InSystem32)
		}
		return
	}
	t.Fatalf("Inspect 的清单里没有探针 DLL %s", native)
}
