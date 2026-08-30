//go:build linux

package instance

import (
	"asa-server/pkg/procx"
	"fmt"
	"os"
	"strings"
)

// queryGameProcesses finds the running game process by command line only —
// image-name matching cannot work here.
//
// Under Proton the game's /proc/<pid>/exe points at
// ".../GE-Proton10-34/files/bin/wine64-preloader" and /proc/<pid>/comm is
// "GameThread", so procx.QueryProcess's name filter ("ArkAscendedServer.exe")
// never matches and the query returns an empty set every single time. That
// silently broke waitForGamePID into a guaranteed 30s timeout, which reported
// a failed start while the game process was in fact up — leaving an orphaned
// process tree behind on every attempt. See
// docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §2.5.
//
// Dropping the name filter alone isn't enough: umu-run, pv-adverb, proton and
// umu.exe all carry the exe's path on their own command lines too. What
// separates the Wine side from those wrappers is the path *form* — only the
// Wine side sees "Z:\...\<exe>", every wrapper carries the Unix
// "/.../<exe>" — so the backslash is the discriminator (isWineSideGameCmdline).
//
// ⚠️ 上面那条规则**不足以**应付 ArkApi：启用它之后带 savedir 标记、且是 Windows 路径
// 形式的进程有两个（加载器与游戏），命令行逐字相同。挑出游戏的规则见 pickGameProcess，
// 真机快照与推导见 docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md §2。
func queryGameProcesses(cmdlineMarker string) ([]procx.Win32Process, error) {
	procs, err := procx.QueryProcess("", cmdlineMarker)
	if err != nil {
		return nil, err
	}

	candidates := make([]gameCandidate, 0, len(procs))
	for _, p := range procs {
		if !isWineSideGameCmdline(p.CommandLine) {
			continue
		}
		candidates = append(candidates, gameCandidate{Proc: p, Comm: processComm(p.ProcessId)})
	}

	if game, ok := pickGameProcess(candidates); ok {
		return []procx.Win32Process{game}, nil
	}
	return nil, nil
}

// processComm 读 /proc/<pid>/comm。
//
// 没有走 pkg/procx 是刻意的：comm 是 Linux 独有的概念，procx 的导出面是按 Windows 的
// WMI 形状定的、两个平台对称，为一个平台的细节在那里加一个只有 Linux 有的函数不划算。
// procx.Win32Process.Name 也顶不上：游戏进程的 /proc/<pid>/exe 是**读得到**的
// （指向 wine64-preloader），所以 Name 永远是 "wine64-preloader"，不会回落到 comm。
func processComm(pid uint32) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
