package serverinfo

import (
	"path/filepath"
	"strings"
)

// includeDisk 只统计**顶层 block device**，判据是 `/sys/block/<name>` 存在，
// 且它不是虚拟设备。
//
// 不靠「设备名含不含数字」判断：`nvme0n1` / `mmcblk0` 是合法的整盘名，本身就带数字。
//   - 纳入：sda/vda/xvda/nvme0n1/mmcblk0 等顶层 disk
//   - 排除：分区（sda1、nvme0n1p1——它们在 /sys/block/<parent>/ 下，不在 /sys/block/ 里）、
//     dm-*/loop*/ram*（realpath 落在 /sys/devices/virtual/block/ 下）
//
// 等价于 `lsblk -d -n -o NAME,TYPE` 里 TYPE == disk 的那批，但不 shell out。
func includeDisk(name string) bool {
	real, err := filepath.EvalSymlinks(filepath.Join("/sys/block", name))
	if err != nil {
		return false
	}
	return !strings.HasPrefix(filepath.ToSlash(real), "/sys/devices/virtual/block/")
}

// includeNIC 排除回环与虚拟网卡（docker0 / veth* / br-* / tun* 都在 virtual 下）。
// 拿不到 realpath 时保守纳入——除了明确的 lo。
func includeNIC(name string) bool {
	if name == "lo" {
		return false
	}
	real, err := filepath.EvalSymlinks(filepath.Join("/sys/class/net", name))
	if err != nil {
		return true
	}
	return !strings.HasPrefix(filepath.ToSlash(real), "/sys/devices/virtual/net/")
}
