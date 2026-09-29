//go:build windows

package userenv

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const machineEnvKey = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`

func openKey(scope Scope, access uint32) (registry.Key, error) {
	if scope == Machine {
		return registry.OpenKey(registry.LOCAL_MACHINE, machineEnvKey, access)
	}
	return registry.OpenKey(registry.CURRENT_USER, "Environment", access)
}

// Get 读取 scope 下持久化的 name。不存在时 ok=false、err=nil。
// REG_EXPAND_SZ 返回未展开的原文（%USERPROFILE% 之类保持原样）。
func Get(scope Scope, name string) (value string, ok bool, err error) {
	k, err := openKey(scope, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("打开 %s 环境变量注册表项失败: %w", scope, err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("读取 %s 环境变量 %s 失败: %w", scope, name, err)
	}
	return v, true, nil
}

// SetUser 把 name=value 写进当前用户的持久化环境变量（REG_SZ），并广播
// WM_SETTINGCHANGE。不需要管理员权限。不改当前进程的环境，调用方按需 os.Setenv。
func SetUser(name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, "Environment", registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开用户环境变量注册表项失败: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(name, value); err != nil {
		return fmt.Errorf("写入用户环境变量 %s 失败: %w", name, err)
	}
	Broadcast()
	return nil
}

// UnsetUser 删除当前用户的持久化环境变量 name（不存在不算错误），并广播。
func UnsetUser(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("打开用户环境变量注册表项失败: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("删除用户环境变量 %s 失败: %w", name, err)
	}
	Broadcast()
	return nil
}

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
)

const (
	hwndBroadcast    = 0xffff
	wmSettingChange  = 0x001A
	smtoAbortIfHung  = 0x0002
	broadcastTimeout = 5000 // ms，每个顶层窗口
)

// Broadcast 通知所有顶层窗口「环境变量变了」。资源管理器收到后刷新自己的环境块，
// 此后从它启动的新进程就能看到新值，不用注销。已经开着的终端窗口不会刷新——那是
// Windows 的通用行为。失败只意味着要注销后才生效，所以不返回错误。
func Broadcast() {
	env, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	_, _, _ = procSendMessageTimeoutW.Call(
		hwndBroadcast, wmSettingChange, 0,
		uintptr(unsafe.Pointer(env)),
		smtoAbortIfHung, broadcastTimeout,
		uintptr(unsafe.Pointer(&result)),
	)
}
