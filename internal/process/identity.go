package process

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/procx"
)

// 「这个进程属于哪个实例」的唯一判定。
//
// 在此之前代码里有四种互不一致的判据：端口、PID 文件、isExpectedProcess 的 exe 名、
// `AltSaveDirectoryName=` 子串。没有一个能同时回答「是不是游戏进程」与「是不是
// **这个实例的**」，于是：
//
//   - 实例名互为前缀（srv / srv2，SaveDir 默认就是实例名）时，子串匹配把 srv2 的
//     进程当成 srv 的；
//   - PID 文件从不清理，强停拿着早已被系统复用的 PID 去杀；
//   - 按端口找进程会拿到同样持有该 socket 的共享 wineserver，停一个实例带走一片。
//
// 所有「要对某个 PID 动手」与「要判断某实例活没活着」的路径都经过这里。
// 端口只用于「是否在对外服务」这一只读判断（IsServerRunning）。
// 见 docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §8.6。

// SaveDirMarker 是启动命令行里标识实例的那一段（instance.StartServer 把它拼在
// 地图 URL 参数串的最后）。它只适合作**预筛**的子串；判定用 CmdlineHasSaveDir。
func SaveDirMarker(saveDir string) string {
	return "AltSaveDirectoryName=" + saveDir
}

// CmdlineHasSaveDir 判断命令行是否带着这个 SaveDir——**带边界**：
// `AltSaveDirectoryName=<saveDir>` 之后必须紧跟串尾、空白、NUL、引号或 '?'。
// 否则 "AltSaveDirectoryName=srv" 会命中 srv2 的命令行。
func CmdlineHasSaveDir(cmdline, saveDir string) bool {
	if saveDir == "" {
		return false
	}
	marker := SaveDirMarker(saveDir)
	for from := 0; ; {
		i := strings.Index(cmdline[from:], marker)
		if i < 0 {
			return false
		}
		end := from + i + len(marker)
		if end == len(cmdline) || isSaveDirTerminator(cmdline[end]) {
			return true
		}
		from += i + 1
	}
}

func isSaveDirTerminator(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', 0, '"', '?':
		return true
	}
	return false
}

// PIDKind 选择实例的哪一个 PID 文件。
type PIDKind int

const (
	PIDGame     PIDKind = iota // pid：游戏进程
	PIDLauncher                // launcher_pid：Linux 上是 umu-run，Windows 上同游戏进程
	PIDAsaApi                  // asa_api_pid：AsaApiLoader
)

func (k PIDKind) fileName() string {
	switch k {
	case PIDLauncher:
		return "launcher_pid"
	case PIDAsaApi:
		return "asa_api_pid"
	}
	return "pid"
}

func getPID(instanceName string, kind PIDKind) (int, error) {
	switch kind {
	case PIDLauncher:
		return GetLauncherPID(instanceName)
	case PIDAsaApi:
		return GetAsaServerApiPID(instanceName)
	}
	return GetInstancePID(instanceName)
}

// VerifiedPID 返回实例保存的某个 PID，前提是它**此刻仍属于这个实例**：
// 进程存活、是本项目会启动的可执行文件（isExpectedProcess）、命令行带着该实例的
// SaveDir（CmdlineHasSaveDir）。任何一条不满足、或查不到判据，都返回 false——
// 宁可漏判，也不能对一个被复用的 PID 动手。
//
// Linux 上三个 PID 的命令行都带着那串参数：游戏进程由 Wine 原样转写，loader 与
// umu-run 的命令行本来就是这串参数。
func VerifiedPID(instanceName string, kind PIDKind) (int, bool) {
	pid, err := getPID(instanceName, kind)
	if err != nil || pid <= 1 {
		return 0, false
	}
	cfg, err := cfgpkg.LoadInstanceConfig(instanceName)
	if err != nil || cfg == nil {
		return 0, false
	}
	if !PIDBelongsTo(pid, cfg.SaveDir) {
		return 0, false
	}
	return pid, true
}

// PIDBelongsTo 是 VerifiedPID 去掉「读 PID 文件」之后的那部分：pid 存活、是本项目
// 的可执行文件、命令行带着 saveDir。供手里已有 PID（例如刚从进程表查出来的）的
// 调用方核对归属。
func PIDBelongsTo(pid int, saveDir string) bool {
	if pid <= 1 {
		return false
	}
	exited, err := procx.IsProcessExited(uint32(pid))
	if err != nil || exited || !isExpectedProcess(uint32(pid)) {
		return false
	}
	cmdline, err := procx.ProcessCmdline(uint32(pid))
	if err != nil {
		return false
	}
	return CmdlineHasSaveDir(cmdline, saveDir)
}

// ClearInstancePIDs 删除实例的三个 PID 文件。实例确认停止后调用，从源头消除陈旧
// PID——它们留着，下一次强停或存活判断就会拿一个早已被复用的号码去做决定。
func ClearInstancePIDs(instanceName string) error {
	dir := filepath.Join(cfgpkg.InstancesDir, instanceName)
	var errs []error
	for _, kind := range []PIDKind{PIDGame, PIDLauncher, PIDAsaApi} {
		if err := os.Remove(filepath.Join(dir, kind.fileName())); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
