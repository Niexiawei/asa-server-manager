package win32api

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procShowWindow               = user32.NewProc("ShowWindow")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsWindow                 = user32.NewProc("IsWindow")

	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procOpenProcess        = kernel32.NewProc("OpenProcess")
	procCloseHandle        = kernel32.NewProc("CloseHandle")
	procGetExitCodeProcess = kernel32.NewProc("GetExitCodeProcess")
)

// ShowWindow flags
const (
	SW_MINIMIZE = 6
)

const (
	STILL_ACTIVE = 259
)

// enumWindows collects all HWNDs for which match(hwnd) == true
func enumWindows(match func(hwnd windows.Handle) bool) ([]windows.Handle, error) {
	var results []windows.Handle
	cb := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		h := windows.Handle(hwnd)
		if match(h) {
			results = append(results, h)
		}
		return 1 // continue enumeration
	})
	ret, _, err := procEnumWindows.Call(cb, 0)
	if ret == 0 {
		return results, err
	}
	return results, nil
}

func getWindowProcessID(hwnd windows.Handle) (uint32, error) {
	var pid uint32
	ret, _, err := procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pid)))
	if ret == 0 {
		// although return value is thread id, 0 indicates failure
		return 0, err
	}
	return pid, nil
}

func isWindowVisible(hwnd windows.Handle) bool {
	ret, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
	return ret != 0
}

// isWindow checks if the specified window handle is valid
func isWindow(hwnd windows.Handle) bool {
	ret, _, _ := procIsWindow.Call(uintptr(hwnd))
	return ret != 0
}

// WindowsExistByPID 检测指定 pid 的窗口是否存在
// 返回 true 表示存在至少一个属于该 PID 的窗口，false 表示不存在
func WindowsExistByPID(pid uint32) (bool, error) {
	hwnds, err := enumWindows(func(hwnd windows.Handle) bool {
		// Check if the window is valid
		if !isWindow(hwnd) {
			return false
		}
		wpid, err := getWindowProcessID(hwnd)
		if err != nil {
			return false
		}
		return wpid == pid
	})
	if err != nil {
		return false, err
	}

	return len(hwnds) > 0, nil
}

// IsProcessExited 检测指定 PID 的进程是否已退出
// 返回 true 表示进程已退出，false 表示进程仍在运行或无法确定
func IsProcessExited(pid uint32) (bool, error) {
	// Open the process with PROCESS_QUERY_INFORMATION access
	handle, _, _ := procOpenProcess.Call(
		windows.PROCESS_QUERY_INFORMATION,
		0,
		uintptr(pid),
	)
	if handle == 0 {
		// If we can't open the process, it likely doesn't exist (has exited)
		return true, nil
	}
	defer procCloseHandle.Call(handle)

	// Get the exit code of the process
	var exitCode uint32
	ret, _, _ := procGetExitCodeProcess.Call(handle, uintptr(unsafe.Pointer(&exitCode)))
	if ret == 0 {
		// If we can't get the exit code, assume the process has exited
		return true, nil
	}

	// If the exit code is STILL_ACTIVE, the process is still running
	return exitCode != STILL_ACTIVE, nil
}
func MinimizeWindowsByPID(pid uint32, onlyVisible bool) ([]uintptr, error) {
	hwnds, err := enumWindows(func(hwnd windows.Handle) bool {
		if onlyVisible && !isWindowVisible(hwnd) {
			return false
		}
		wpid, err := getWindowProcessID(hwnd)
		if err != nil {
			return false
		}
		return wpid == pid
	})
	if err != nil {
		return nil, err
	}

	var minimized []uintptr
	for _, h := range hwnds {
		// 调用 ShowWindow(hwnd, SW_MINIMIZE)
		procShowWindow.Call(uintptr(h), uintptr(SW_MINIMIZE))
		minimized = append(minimized, uintptr(h))
	}
	return minimized, nil
}

// GetPIDByPort 根据占用的端口号查询进程ID
// port: 应用占用的端口号
// 返回: 进程ID、错误信息
func GetPIDByPort(port int) (int, error) {
	cmd := exec.Command("netstat", "-ano")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to execute netstat: %w", err)
	}

	netstatOutput := string(output)
	portStr := fmt.Sprintf(":%d", port)

	// Split output into lines and search for the port
	lines := strings.Split(netstatOutput, "\n")
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
					return pid, nil
				}
			}
		}
	}

	return 0, fmt.Errorf("no process found listening on port %d", port)
}

// HideWindowByPID 根据进程ID隐藏应用窗口
// pid: 应用的进程ID
// onlyVisible: 是否仅隐藏可见窗口（true 仅隐藏可见窗口，false 隐藏所有窗口）
// 返回: 被隐藏的窗口句柄列表、错误信息
func HideWindowByPID(pid uint32, onlyVisible bool) ([]uintptr, error) {
	hwnds, err := enumWindows(func(hwnd windows.Handle) bool {
		if onlyVisible && !isWindowVisible(hwnd) {
			return false
		}
		wpid, err := getWindowProcessID(hwnd)
		if err != nil {
			return false
		}
		return wpid == pid
	})
	if err != nil {
		return nil, err
	}

	if len(hwnds) == 0 {
		return nil, fmt.Errorf("no windows found for PID %d", pid)
	}

	const SW_HIDE = 0 // 隐藏窗口
	var hidden []uintptr
	for _, h := range hwnds {
		procShowWindow.Call(uintptr(h), uintptr(SW_HIDE))
		hidden = append(hidden, uintptr(h))
	}
	return hidden, nil
}
