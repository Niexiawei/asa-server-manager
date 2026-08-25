// Package process handles per-instance PID persistence and process-liveness checks.
// It sits below both state and instance packages to break the state<->instance
// import cycle: both depend on process, process depends only on config + winproc.
package process

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/winproc"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SaveInstancePID persists the PID of a running instance to its directory.
func SaveInstancePID(instanceName string, pid int) error {
	instanceDir := filepath.Join(cfgpkg.InstancesDir, instanceName)
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		return fmt.Errorf("failed to create instance directory: %w", err)
	}

	pidFile := filepath.Join(instanceDir, "pid")
	return os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644)
}

// GetInstancePID retrieves the PID of a running instance from its directory.
func GetInstancePID(instanceName string) (int, error) {
	instanceDir := filepath.Join(cfgpkg.InstancesDir, instanceName)
	pidFile := filepath.Join(instanceDir, "pid")

	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID: %w", err)
	}

	return pid, nil
}

// SaveAsaServerApiPID persists the AsaApiLoader (asaServerApi) PID of an instance
// to its directory. This is the loader process launched when EnableAsaPlugin is on;
// it is distinct from the game process PID stored by SaveInstancePID.
func SaveAsaServerApiPID(instanceName string, pid int) error {
	instanceDir := filepath.Join(cfgpkg.InstancesDir, instanceName)
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		return fmt.Errorf("failed to create instance directory: %w", err)
	}

	pidFile := filepath.Join(instanceDir, "asa_api_pid")
	return os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644)
}

// GetAsaServerApiPID retrieves the saved AsaApiLoader (asaServerApi) PID of an instance.
func GetAsaServerApiPID(instanceName string) (int, error) {
	pidFile := filepath.Join(cfgpkg.InstancesDir, instanceName, "asa_api_pid")

	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID: %w", err)
	}

	return pid, nil
}

// expectedExecutables 是本项目会启动、且会写 PID 文件的可执行文件名（小写）。
// 见 instance/server.go 的 ArkAscendedServer.exe / AsaApiLoader.exe。
var expectedExecutables = map[string]bool{
	"arkascendedserver.exe": true,
	"asaapiloader.exe":      true,
}

// isExpectedProcess 核对 pid 当前对应的可执行文件是否是本项目会启动的那几个。
//
// PID 文件一旦写入就不会再被清理（进程正常停止或崩溃都不会删它），而 Windows
// 会把退出进程的 PID 号码回收复用给完全无关的新进程。只看"这个 PID 存在"会把
// 崩溃后凑巧复用了旧 PID 的无关进程误判成"实例还活着"。查不到镜像名（进程已
// 退出、或权限不足）一律当作"不是"——宁可漏判存活，也不能误判存活。
func isExpectedProcess(pid uint32) bool {
	imagePath, err := winproc.ProcessImageName(pid)
	if err != nil {
		return false
	}
	return expectedExecutables[strings.ToLower(filepath.Base(imagePath))]
}

// IsServerRunningByPID checks if a server instance is running by verifying the
// saved PID process still exists and is one of the expected game executables.
func IsServerRunningByPID(instanceName string) (bool, error) {
	pid, err := GetInstancePID(instanceName)
	if err != nil {
		return false, fmt.Errorf("failed to get instance PID: %w", err)
	}

	exited, err := winproc.IsProcessExited(uint32(pid))
	if err != nil {
		return false, fmt.Errorf("failed to check process status: %w", err)
	}

	if exited || !isExpectedProcess(uint32(pid)) {
		return false, nil
	}

	return true, nil
}

// IsInstanceProcessAlive reports whether an instance is alive, by port listening
// check first, then by saved-PID process existence.
func IsInstanceProcessAlive(instanceName string) bool {
	// 方法 1：检查端口是否被监听
	running, err := IsServerRunning(instanceName)
	if err == nil && running {
		return true
	}

	// 方法 2：检查保存的 PID 对应的进程是否还在，且确实是本项目会启动的可执行文件。
	// 端口没绑上不代表进程真的还在跑，但也不能只凭 PID 号码存在就判定存活——
	// 见 isExpectedProcess 的说明。
	pid, err := GetInstancePID(instanceName)
	if err != nil || pid <= 0 {
		return false
	}

	exited, err := winproc.IsProcessExited(uint32(pid))
	if err != nil || exited {
		return false
	}
	return isExpectedProcess(uint32(pid))
}

// ListAliveInstances returns the names of all instances currently judged alive.
//
// 判据用 IsInstanceProcessAlive 而非 IsServerRunning：后者只看端口是否在监听，
// 会漏掉进程已起但端口尚未绑定的 starting 阶段实例。
// 读取实例列表失败时返回 nil，调用方据此不阻断。
func ListAliveInstances() []string {
	instances, err := cfgpkg.GetAvailableInstances()
	if err != nil {
		return nil
	}

	var alive []string
	for _, name := range instances {
		if IsInstanceProcessAlive(name) {
			alive = append(alive, name)
		}
	}
	return alive
}
