package serverinfo

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// includeDisk 决定某个 /proc/diskstats 行是否计入「整机磁盘吞吐」。
//
// 目标是**不重复、不遗漏地拿到物理层总量**。难点在于 diskstats 在每一层都独立
// 计数、且层层重叠：LVM 下同一次写会出现在 dm-1(LV) + sda2(PV 分区) + sda(整盘)，
// 叠 LUKS 还要再加一层 dm-0(crypt)，md 镜像则是 md0 + 两个成员盘。把这些都求和会
// 让带 LVM/LUKS/md 的机器（现代发行版默认装机的常态）整机吞吐虚高 2~4 倍。
//
// 判据不是设备名前缀，而是「这个块设备下面还有没有更底层的块设备」——即
// /sys/block/<name>/slaves/ 是否为空：
//   - sda / nvme0n1 / vda：slaves 为空 → 计入（栈底，最贴硬件，彼此不重叠）
//   - dm-*（LVM/LUKS/multipath）、md*：slaves 非空 → 跳过，其 IO 已被成员盘统计
//   - 分区 sda1 / nvme0n1p1：不在 /sys/block/ 顶层（在 /sys/block/<parent>/ 下）→ 跳过
//   - WSL2 的虚拟主盘、VM 里孤立的 dm/vd 设备：slaves 为空 → 计入（否则整机恒为 0）
//
// 仍显式排除 loop/ram/zram/sr/fd/nbd：它们是「无 slaves 的叶子」但不算真实磁盘吞吐
//（loop 背后的真实盘会自己被计入；zram 是内存）。
func includeDisk(name string) bool {
	// 1. 明确不计入的虚拟/非磁盘前缀
	for _, prefix := range []string{"loop", "ram", "zram", "fd", "sr", "nbd"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}

	// 2. 必须是 /sys/block 下的直接目录（排除分区，如 sda1 —— 它在 /sys/block/sda/ 下）
	sysPath := filepath.Join("/sys/block", name)
	if info, err := os.Stat(sysPath); err != nil || !info.IsDir() {
		return false
	}

	// 3. 容量 > 0（排除空读卡器等）
	if b, err := os.ReadFile(filepath.Join(sysPath, "size")); err == nil {
		if blocks, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil && blocks == 0 {
			return false
		}
	}

	// 4. slaves/ 非空 → 该设备叠在更底层的块设备之上（dm-*/md*），其 IO 已被底层统计，
	//    跳过以免重复计数。读不到 slaves/（老内核无此目录）时按空处理 → 计入。
	if entries, err := os.ReadDir(filepath.Join(sysPath, "slaves")); err == nil && len(entries) > 0 {
		return false
	}

	return true
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
