// Package folderpicker 调系统原生的「选择文件夹」对话框。
//
// 只有 Windows 有实现（IFileOpenDialog + FOS_PICKFOLDERS，Vista 起的标准对话框：
// 地址栏可粘贴、有快速访问与网络位置）。其余平台返回 ErrUnsupported，调用方
// 回退到自己的实现（GUI 里是 Fyne 自绘的 dialog.NewFolderOpen）。
//
// 零领域依赖、无全局状态。
package folderpicker

import "errors"

// ErrUnsupported 表示当前平台没有原生目录选择器。
var ErrUnsupported = errors.New("folderpicker: 当前平台不支持原生目录选择器")
