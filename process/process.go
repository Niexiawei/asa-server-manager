// Package process handles per-instance PID persistence and process-liveness checks.
// It sits below both state and instance packages to break the state<->instance
// import cycle: both depend on process, process depends only on config + winproc.
package process

import (
	cfgpkg "asa-server/config"
	"asa-server/pkg/winproc"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

// IsServerRunning checks if a server instance is running by verifying its game
// port is listening (uniquely identifies the specific server instance).
func IsServerRunning(instanceName string) (bool, error) {
	config, err := cfgpkg.LoadInstanceConfig(instanceName)
	if err != nil {
		return false, err
	}

	cmd := exec.Command("netstat", "-ano")
	// Hide the cmd window on Windows
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to execute netstat: %w", err)
	}

	netstatOutput := string(output)
	lines := strings.Split(netstatOutput, "\n")
	portStr := fmt.Sprintf(":%d", config.Port)

	for _, line := range lines {
		if strings.Contains(line, portStr) {
			// The last field in the line is the PID
			fields := strings.Fields(line)
			if len(fields) > 2 {
				if !strings.Contains(fields[1], portStr) {
					continue
				}
				pid, err := strconv.Atoi(fields[len(fields)-1])
				if err == nil && pid > 0 {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// IsServerRunningByPID checks if a server instance is running by verifying the
// saved PID process still exists.
func IsServerRunningByPID(instanceName string) (bool, error) {
	pid, err := GetInstancePID(instanceName)
	if err != nil {
		return false, fmt.Errorf("failed to get instance PID: %w", err)
	}

	exited, err := winproc.IsProcessExited(uint32(pid))
	if err != nil {
		return false, fmt.Errorf("failed to check process status: %w", err)
	}

	if exited {
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

	// 方法 2：检查进程是否存在
	pid, err := GetInstancePID(instanceName)
	if err != nil || pid <= 0 {
		return false
	}

	exited, err := winproc.IsProcessExited(uint32(pid))
	if err != nil {
		return false
	}
	return !exited
}
