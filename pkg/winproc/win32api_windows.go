//go:build windows

package winproc

import (
	"fmt"
	"os"
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

	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
	procGetExitCodeProcess         = kernel32.NewProc("GetExitCodeProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")

	shell32           = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

// ShowWindow flags
const (
	SW_MINIMIZE   = 6
	SW_SHOWNORMAL = 1
)

const (
	STILL_ACTIVE = 259

	// processQueryLimitedInformation 只够查镜像名，不需要 PROCESS_QUERY_INFORMATION
	// 那么高的权限，对着系统进程/权限更高的进程也能查。
	processQueryLimitedInformation = 0x1000
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

// ProcessImageName 返回指定 PID 对应可执行文件的完整路径。
// 进程不存在或没有查询权限时返回 error。
//
// 用途：PID 会被系统回收复用给完全无关的新进程，光凭"这个 PID 存在"判断
// 存活会有误判风险；调用方应该把这里返回的文件名和期望的可执行文件核对一遍。
func ProcessImageName(pid uint32) (string, error) {
	handle, _, _ := procOpenProcess.Call(
		processQueryLimitedInformation,
		0,
		uintptr(pid),
	)
	if handle == 0 {
		return "", fmt.Errorf("process %d not found or inaccessible", pid)
	}
	defer procCloseHandle.Call(handle)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	ret, _, err := procQueryFullProcessImageNameW.Call(
		handle,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret == 0 {
		return "", fmt.Errorf("QueryFullProcessImageName failed: %w", err)
	}
	return windows.UTF16ToString(buf[:size]), nil
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

// RunAsAdmin 使用 ShellExecuteW + "runas" 以管理员权限重新启动当前程序
// args: 传递给新进程的参数字符串（不含程序名本身），例如 "--elevated api --api-port 19193"
// 返回值: 成功返回 nil，失败返回包含错误码的 error（1223 表示用户取消了 UAC 弹窗）
func RunAsAdmin(args string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	argPtr, _ := syscall.UTF16PtrFromString(args)

	ret, _, callErr := procShellExecuteW.Call(
		0,                               // hwnd: 无父窗口
		uintptr(unsafe.Pointer(verb)),   // operation: "runas"
		uintptr(unsafe.Pointer(file)),   // file: 当前可执行文件
		uintptr(unsafe.Pointer(argPtr)), // parameters: 参数
		0,                               // directory: nil（继承当前目录）
		SW_SHOWNORMAL,                   // showcmd
	)

	// ShellExecuteW 返回值 <= 32 表示失败
	if ret <= 32 {
		if ret == 1223 {
			return fmt.Errorf("用户取消 UAC 提权弹窗")
		}
		return fmt.Errorf("ShellExecuteW 失败, ret=%d, err=%v", ret, callErr)
	}

	return nil
}
