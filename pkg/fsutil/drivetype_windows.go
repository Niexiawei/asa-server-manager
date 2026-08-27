//go:build windows

package fsutil

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// IsNetworkDrive 判断 path 是否落在映射网络盘或 UNC 路径上（DRIVE_REMOTE）。
//
// filepath.VolumeName 对映射盘符返回 "C:"，对 UNC 路径返回 "\\server\share" 本身——
// GetDriveType 接受这两种形式的卷根路径（末尾带反斜杠），因此两种情况用同一次调用
// 就能覆盖，不需要额外的 UNC 前缀特判。
//
// 已知缺口：subst 出来的虚拟盘不会被这里识别——GetDriveType 对 subst 盘返回的是
// 目标盘的类型（本地盘一般仍是 DRIVE_FIXED），要识别 subst 需要额外调用
// QueryDosDevice 比对目标路径，本次未实现，与文档 §10.4 保留的已知限制一致。
func IsNetworkDrive(path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	vol := filepath.VolumeName(abs)
	if vol == "" {
		return false, nil
	}
	root := vol + `\`
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return false, err
	}
	return windows.GetDriveType(p) == windows.DRIVE_REMOTE, nil
}
