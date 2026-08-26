//go:build windows

package mirror

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"asa-server/pkg/logger"

	"golang.org/x/sys/windows"
)

// createJunction 在 linkPath 建一条指向 targetPath 的 NTFS junction（mount point）。
//
// 为什么不用 os.Symlink：它在 Windows 上对目录目标创建的是**目录符号链接**，
// 需要 SeCreateSymbolicLinkPrivilege（管理员，或开启开发者模式）。而真正的
// junction 走 FSCTL_SET_REPARSE_POINT，**普通用户即可创建**。
// 这个区别是整个程序过去必须提权自重启的唯一原因。
//
// 语义与 os.Symlink 保持一致：linkPath 已存在时报错而不是覆盖 ——
// 在一个非空目录上设置 reparse point 会让原有内容变得不可访问，
// 调用方（migrateExceptionJunctions / reconcileEntry）都依赖"先删再建"的顺序。
func createJunction(linkPath, targetPath string) error {
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %w", targetPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory for %s: %w", linkPath, err)
	}

	// 用 Mkdir 而非 MkdirAll：已存在时要报错，与 os.Symlink 的行为对齐
	if err := os.Mkdir(linkPath, 0755); err != nil {
		return fmt.Errorf("failed to create junction placeholder %s: %w", linkPath, err)
	}

	if err := setMountPoint(linkPath, absTarget); err != nil {
		// 回滚那个空目录，否则半成品会被后续同步误判成真实目录
		_ = os.Remove(linkPath)
		return fmt.Errorf("failed to create junction %s -> %s: %w", linkPath, absTarget, err)
	}

	logger.Debugf("Created junction: %s -> %s", linkPath, absTarget)
	return nil
}

// setMountPoint 往一个已存在的空目录上写 IO_REPARSE_TAG_MOUNT_POINT 重解析点。
//
// REPARSE_DATA_BUFFER 的 mount point 变体布局（全部小端）：
//
//	0   uint32  ReparseTag = IO_REPARSE_TAG_MOUNT_POINT
//	4   uint16  ReparseDataLength   —— 从偏移 8 起的字节数
//	6   uint16  Reserved
//	8   uint16  SubstituteNameOffset
//	10  uint16  SubstituteNameLength
//	12  uint16  PrintNameOffset
//	14  uint16  PrintNameLength
//	16  []uint16 PathBuffer
//
// 两个 Length 都**不含**各自的 NUL 结尾，但 PathBuffer 里必须带上，
// 且 PrintName 紧跟在 SubstituteName 的 NUL 之后。
func setMountPoint(linkPath, absTarget string) error {
	// SubstituteName 必须是 NT 路径形式（\??\C:\...），PrintName 是给人看的显示路径
	sub, err := windows.UTF16FromString(`\??\` + absTarget)
	if err != nil {
		return fmt.Errorf("invalid target path %q: %w", absTarget, err)
	}
	printName, err := windows.UTF16FromString(absTarget)
	if err != nil {
		return fmt.Errorf("invalid target path %q: %w", absTarget, err)
	}

	subLen := (len(sub) - 1) * 2 // 去掉 NUL
	printLen := (len(printName) - 1) * 2

	pathBuf := make([]uint16, 0, len(sub)+len(printName))
	pathBuf = append(pathBuf, sub...)       // 含 NUL
	pathBuf = append(pathBuf, printName...) // 含 NUL

	dataLen := 8 + len(pathBuf)*2 // 四个 uint16 + PathBuffer
	buf := make([]byte, 8+dataLen)
	binary.LittleEndian.PutUint32(buf[0:], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:], uint16(dataLen))
	binary.LittleEndian.PutUint16(buf[6:], 0)
	binary.LittleEndian.PutUint16(buf[8:], 0)
	binary.LittleEndian.PutUint16(buf[10:], uint16(subLen))
	binary.LittleEndian.PutUint16(buf[12:], uint16(subLen+2)) // 跳过 SubstituteName 的 NUL
	binary.LittleEndian.PutUint16(buf[14:], uint16(printLen))
	for i, v := range pathBuf {
		binary.LittleEndian.PutUint16(buf[16+i*2:], v)
	}

	p, err := windows.UTF16PtrFromString(linkPath)
	if err != nil {
		return fmt.Errorf("invalid link path %q: %w", linkPath, err)
	}
	// FILE_FLAG_BACKUP_SEMANTICS 才能拿到目录句柄；
	// FILE_FLAG_OPEN_REPARSE_POINT 保证操作的是链接本身而不是它指向的位置
	h, err := windows.CreateFile(
		p,
		windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return fmt.Errorf("open %s: %w", linkPath, err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck

	var returned uint32
	if err := windows.DeviceIoControl(
		h, windows.FSCTL_SET_REPARSE_POINT,
		&buf[0], uint32(len(buf)),
		nil, 0, &returned, nil,
	); err != nil {
		return fmt.Errorf("FSCTL_SET_REPARSE_POINT: %w", err)
	}
	return nil
}
