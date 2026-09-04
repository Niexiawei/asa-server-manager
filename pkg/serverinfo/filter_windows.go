package serverinfo

import "strings"

// includeDisk 在 Windows 上全收。
//
// gopsutil 的 disk.IOCounters 返回的 key 是**盘符**（"C:"/"D:"），不是 PhysicalDriveN
// （disk/disk_windows.go:297 走 GetLogicalDriveStrings + 逐卷 IOCTL_DISK_PERFORMANCE），
// 而且它已经只保留 DRIVE_FIXED —— 光驱、可移动盘、网络盘都被挡掉了，这里无需再筛。
// 同一物理盘的多个卷各返回一份，求和 ≈ 整盘吞吐，够用但不是精确的物理盘计数。
func includeDisk(string) bool { return true }

// includeNIC 排除回环伪接口。Windows 的适配器名是显示名（"以太网"、
// "Loopback Pseudo-Interface 1"），只能按名字认回环。
func includeNIC(name string) bool {
	return !strings.Contains(strings.ToLower(name), "loopback")
}
