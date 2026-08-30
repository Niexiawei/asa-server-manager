package instance

// 「哪个进程才是游戏进程」的判定规则。Linux 专用逻辑，但**不加 build tag** ——
// 与 runner/steamrt.go、runner/vcredist.go 同理由：全是字符串比较，没有平台专属
// API，不加约束才能在 Windows 上跑单测，而这条规则的用例正是真机进程快照。
// 接线在 gameproc_linux.go；Windows 走 gameproc_windows.go 的镜像名匹配，用不到这里。
//
// 见 docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md §2。

import (
	"asa-server/pkg/procx"
	"strings"
)

// gameProcessComm 是游戏进程在 /proc/<pid>/comm 里的名字：Unreal Engine 给主线程起的
// 名字，而进程的 comm 就是主线程的 comm。真机实测（2026-08-30）在**两种**启动形态下
// 都是它，且所有包装器进程都不是。
//
// 内核把 comm 截到 15 字节（同一份快照里 AsaApiLoader.exe 的 comm 就是被截过的
// "AsaApiLoader.ex"），"GameThread" 只有 10 字节，不受影响 —— 但正因为存在截断，
// 比较必须是精确相等，不能用前缀匹配。
const gameProcessComm = "GameThread"

// arkGameExeNames 是可能出现在**游戏进程**命令行上的 exe 名。两个都要认：启用 ArkApi
// 时命令行上是加载器的名字，见 pickGameProcess。
var arkGameExeNames = []string{arkExeName, asaApiLoaderExeName}

// gameCandidate 是一个「命令行看起来像游戏进程」的进程，外加它的 comm。
type gameCandidate struct {
	Proc procx.Win32Process
	Comm string
}

// isWineSideGameCmdline 判断一条命令行是不是 Wine 侧的游戏/加载器进程：
// 必须带 Windows 路径形式（反斜杠）的已知 exe 名。
//
// 只看反斜杠是不够的：umu.exe 的命令行是 `c:\windows\system32\umu.exe /unix/path/...`，
// 反斜杠有、但 exe 路径是 Unix 形式，所以必须是 `\<exe名>` 连在一起。
func isWineSideGameCmdline(cmdline string) bool {
	for _, exe := range arkGameExeNames {
		if strings.Contains(cmdline, `\`+exe) {
			return true
		}
	}
	return false
}

// pickGameProcess 从候选里挑出真正的游戏进程。
//
// 为什么需要「挑」：启用 ArkApi 时，`AsaApiLoader.exe` 创建游戏进程时把自己的整条
// 命令行原样传了过去，于是同一次启动里有**两个**进程带着**逐字相同**的命令行 ——
// 加载器和游戏（真机快照见 docs/ARKAPI_LINUX_LOGGING_AND_PID_PLAN.md §2.2）。
// 父进程区分不了（Wine 把两者都重挂到 pv-adverb 名下），线程数与启动进度相关，
// 剩下唯一可用的信号是 comm。
//
// 两层，顺序有意义：
//
//  1. comm == "GameThread" —— 两种启动形态下都成立。
//  2. 没有任何候选叫 GameThread 时，退回「命令行里写着 ArkAscendedServer.exe 的那个」，
//     也就是本函数存在之前的规则。这一层是给「将来 UE 改了主线程名」留的后路：
//     即便第 1 条失效，不启用 ArkApi 的实例也照常工作。
//
// 两层都没命中就返回 false —— **宁可判成启动失败，也不能把加载器当成游戏**：
// 加载器不是游戏的父进程，拿它的 PID 去 TerminateTree 杀不到游戏，那会把一条清楚的
// 「启动失败」换成难查得多的「实例停不掉」。
func pickGameProcess(candidates []gameCandidate) (procx.Win32Process, bool) {
	for _, c := range candidates {
		if c.Comm == gameProcessComm {
			return c.Proc, true
		}
	}
	for _, c := range candidates {
		if strings.Contains(c.Proc.CommandLine, `\`+arkExeName) {
			return c.Proc, true
		}
	}
	return procx.Win32Process{}, false
}
