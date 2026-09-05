//go:build linux

package procmatch

import (
	"asa-server/pkg/procx"
	"fmt"
	"os"
	"strings"
)

// Find finds the running game process by command line only — image-name
// matching cannot work here.
//
// Under Proton the game's /proc/<pid>/exe points at the Wine preloader and
// /proc/<pid>/comm is the thread name, so a Win32_Process.Name-style filter
// never matches and would return an empty set every time. Dropping the name
// filter alone is not enough either: every wrapper in the umu/proton chain
// also carries the target exe's path on its own command line — what
// separates the Wine side from those wrappers is the path *form* (only the
// Wine side sees "Z:\...\<exe>"), which is what isWineSideCmdline checks.
// pick then resolves the loader/game ambiguity via comm.
func (m *Matcher) Find(cmdlineMarker string) (procx.Win32Process, bool, error) {
	procs, err := procx.QueryProcess("", cmdlineMarker)
	if err != nil {
		return procx.Win32Process{}, false, err
	}

	candidates := make([]candidate, 0, len(procs))
	for _, p := range procs {
		if !m.isWineSideCmdline(p.CommandLine) {
			continue
		}
		candidates = append(candidates, candidate{Proc: p, Comm: processComm(p.ProcessId)})
	}

	proc, ok := m.pick(candidates)
	return proc, ok, nil
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
