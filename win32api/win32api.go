package win32api

import (
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
)

// ShowWindow flags
const (
	SW_MINIMIZE = 6
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

// MinimizeWindowsByPID 最小化指定 pid 的所有顶层窗口
// onlyVisible=true 则只处理可见窗口
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
