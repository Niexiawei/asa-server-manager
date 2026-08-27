//go:build linux

package fsutil

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// networkFsTypes 是 /proc/mounts 里视为网络文件系统的 fstype 集合。
var networkFsTypes = []string{"nfs", "nfs4", "cifs", "smb3", "smbfs", "fuse.sshfs"}

// IsNetworkDrive 判断 path 所在挂载点的文件系统类型是否属于网络文件系统
// （nfs/cifs/smbfs/sshfs 等），查 /proc/mounts 里前缀匹配最长的挂载点。
func IsNetworkDrive(path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	fstype, err := mountFstype(abs)
	if err != nil {
		return false, err
	}
	return slices.Contains(networkFsTypes, fstype), nil
}

// mountFstype 返回 path 所属挂载点的 fstype：按 /proc/mounts 里挂载点字符串长度
// 从长到短匹配前缀，最长匹配即为 path 实际所在的挂载点（标准做法，等价于
// 内核解析路径时逐级查 mount 表)。
func mountFstype(path string) (string, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return "", err
	}
	defer f.Close()

	bestMountLen := -1
	bestFstype := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		mountPoint := unescapeOctal(fields[1])
		if !isPathUnderMount(path, mountPoint) {
			continue
		}
		if len(mountPoint) > bestMountLen {
			bestMountLen = len(mountPoint)
			bestFstype = fields[2]
		}
	}
	return bestFstype, scanner.Err()
}

// isPathUnderMount 判断 path 是否落在 mountPoint 之下（含相等），按路径分段比较，
// 不能用裸字符串 HasPrefix——"/data2" 不该被误判为挂载点 "/data" 之下。
func isPathUnderMount(path, mountPoint string) bool {
	if mountPoint == "/" {
		return true
	}
	if path == mountPoint {
		return true
	}
	return strings.HasPrefix(path, mountPoint+"/")
}

// unescapeOctal 还原 /proc/mounts 里对空格等特殊字符的八进制转义（如 \040 表示空格）。
func unescapeOctal(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
