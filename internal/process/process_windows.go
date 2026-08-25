//go:build windows

package process

import (
	cfgpkg "asa-server/internal/config"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

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
