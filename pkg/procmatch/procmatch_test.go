package procmatch

import (
	"asa-server/pkg/procx"
	"testing"
)

// 下面两组用例是 2026-08-30 真机（WSL2 + GE-Proton10-34 + umu 1.4.4）扫 /proc 得到的
// 快照，逐字照抄，只把路径缩短。它们是这条规则**唯一**的依据，所以宁可啰嗦也要留真值。
// 见 docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md §2.2。
const (
	cmdUmuRun = "python3 /opt/asa/umu-launcher/umu-run /opt/asa/…/Win64/AsaApiLoader.exe " +
		"TheIsland_WP?listen?AltSaveDirectoryName=pidprobe2 -Port=41778"
	cmdUmuExe = `c:\windows\system32\umu.exe /opt/asa/…/Win64/AsaApiLoader.exe ` +
		"TheIsland_WP?listen?AltSaveDirectoryName=pidprobe2 -Port=41778"
	cmdWineLoader = `Z:\opt\asa\…\Win64\AsaApiLoader.exe ` +
		"TheIsland_WP?listen?AltSaveDirectoryName=pidprobe2 -Port=41778"
	cmdWineArk = `Z:\opt\asa\…\Win64\ArkAscendedServer.exe ` +
		"TheIsland_WP?listen?AltSaveDirectoryName=pidprobe2 -Port=41778"
)

func testMatcher() *Matcher {
	return New([]string{"ArkAscendedServer.exe", "AsaApiLoader.exe"}, "GameThread")
}

func proc(pid uint32, cmdline string) procx.Win32Process {
	return procx.Win32Process{ProcessId: pid, CommandLine: cmdline}
}

// TestIsWineSideCmdline: 包装器全部排除，Wine 侧两个 exe 都保留。
//
// umu.exe 那条是最容易漏的：它的命令行里**有**反斜杠（c:\windows\system32\umu.exe），
// 只判「有没有反斜杠」会把它一起收进来。
func TestIsWineSideCmdline(t *testing.T) {
	m := testMatcher()
	tests := []struct {
		name    string
		cmdline string
		want    bool
	}{
		{"umu-run 包装器（Unix 路径）", cmdUmuRun, false},
		{"umu.exe（有反斜杠，但 exe 路径是 Unix 形式）", cmdUmuExe, false},
		{"Wine 侧的 AsaApiLoader", cmdWineLoader, true},
		{"Wine 侧的 ArkAscendedServer", cmdWineArk, true},
		{"空命令行", "", false},
	}
	for _, tt := range tests {
		if got := m.isWineSideCmdline(tt.cmdline); got != tt.want {
			t.Errorf("%s: isWineSideCmdline = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestPickArkApi 是本次修复的核心用例：启用 ArkApi 时加载器与游戏的命令行
// **逐字相同**，只有 comm 不同。挑错了不是「少一个 PID」，而是停止时杀不到游戏。
func TestPickArkApi(t *testing.T) {
	m := testMatcher()
	candidates := []candidate{
		{Proc: proc(2705, cmdWineLoader), Comm: "AsaApiLoader.ex"}, // comm 被内核截到 15 字节
		{Proc: proc(2722, cmdWineLoader), Comm: "GameThread"},
	}
	got, ok := m.pick(candidates)
	if !ok {
		t.Fatal("pick found nothing; want the GameThread process")
	}
	if got.ProcessId != 2722 {
		t.Errorf("picked pid %d, want 2722 (the GameThread one, which is what holds the game port)", got.ProcessId)
	}
}

// TestPickPlain 是回归用例：不启用 ArkApi 的形态今天是好的，不能改坏。
func TestPickPlain(t *testing.T) {
	m := testMatcher()
	candidates := []candidate{
		{Proc: proc(3228, cmdWineArk), Comm: "GameThread"},
	}
	got, ok := m.pick(candidates)
	if !ok || got.ProcessId != 3228 {
		t.Errorf("pick = (%d, %v), want (3228, true)", got.ProcessId, ok)
	}
}

// TestPickFallsBackWhenCommChanges: comm 依赖 UE 的线程命名，将来可能变。
// 那时第二层必须接住不启用 ArkApi 的实例 —— 这是这套规则的安全网，单独钉一条。
func TestPickFallsBackWhenCommChanges(t *testing.T) {
	m := testMatcher()
	candidates := []candidate{
		{Proc: proc(3228, cmdWineArk), Comm: "SomeFutureUEName"},
	}
	got, ok := m.pick(candidates)
	if !ok || got.ProcessId != 3228 {
		t.Errorf("pick = (%d, %v), want the ArkAscendedServer.exe fallback to pick 3228", got.ProcessId, ok)
	}
}

// TestPickRefusesTheLoader: 只有加载器、游戏还没出现时必须返回空。
//
// 这一条比看上去重要：返回加载器会让上层「成功」，实例被记成 started，然后停止时
// TerminateTree 从加载器出发**杀不到游戏**（两者的父进程都是 pv-adverb，不是父子
// 关系）。宁可判成启动失败 —— 那是一条清楚的错误消息。
func TestPickRefusesTheLoader(t *testing.T) {
	m := testMatcher()
	candidates := []candidate{
		{Proc: proc(2705, cmdWineLoader), Comm: "AsaApiLoader.ex"},
	}
	if got, ok := m.pick(candidates); ok {
		t.Errorf("pick returned pid %d for a loader-only snapshot; want no match", got.ProcessId)
	}
}

func TestPickEmpty(t *testing.T) {
	m := testMatcher()
	if _, ok := m.pick(nil); ok {
		t.Error("pick(nil) reported a match")
	}
}

// TestPickPrefersCommOverOrder: GameThread 优先级高于「命令行里有
// ArkAscendedServer.exe」，即使后者排在前面。
func TestPickPrefersCommOverOrder(t *testing.T) {
	m := testMatcher()
	candidates := []candidate{
		{Proc: proc(1, cmdWineArk), Comm: "not-the-game"},
		{Proc: proc(2, cmdWineLoader), Comm: "GameThread"},
	}
	got, ok := m.pick(candidates)
	if !ok || got.ProcessId != 2 {
		t.Errorf("pick = (%d, %v), want pid 2 (comm wins)", got.ProcessId, ok)
	}
}
