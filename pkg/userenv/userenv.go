// Package userenv 读写**持久化**的环境变量（下次登录 / 新启动的进程可见），
// 不是当前进程的 os.Getenv/os.Setenv。
//
// 只有 Windows 有实现：用户级存在 HKCU\Environment，系统级存在
// HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment，写完广播
// WM_SETTINGCHANGE 让资源管理器刷新环境块。其余平台一律返回 ErrUnsupported——
// Linux 上「持久化环境变量」要选 .bashrc / .zshrc / /etc/environment / systemd
// drop-in 中的哪一个，选哪个都是替用户做主，不该由程序代劳。
//
// 零领域依赖、无全局状态。
package userenv

import "errors"

// ErrUnsupported 表示当前平台没有持久化环境变量的实现。
var ErrUnsupported = errors.New("userenv: 当前平台不支持持久化环境变量")

// Scope 是环境变量的作用范围。
type Scope int

const (
	// User 是当前用户级（Windows：HKCU\Environment）。同名时覆盖 Machine（PATH 之外）。
	User Scope = iota
	// Machine 是系统级（Windows：HKLM\...\Session Manager\Environment），写入需要管理员。
	Machine
)

func (s Scope) String() string {
	if s == Machine {
		return "machine"
	}
	return "user"
}
