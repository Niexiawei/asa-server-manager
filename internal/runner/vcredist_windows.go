//go:build windows

package runner

import (
	"context"
	"io"
)

// Windows 上没有 Wine prefix 这个概念，微软 VC++ 运行时是系统级组件（ArkApi 的用户
// 按官方要求自行安装）。这两个函数在这里恒为空操作，好让 internal/instance 那段
// ArkApi 检查不必带构建约束。见 docs/ARKAPI_LINUX_VCREDIST_PLAN.md §3.6。

func ensurePrefixVCRedist(context.Context, string, io.Writer) error { return nil }

func prefixHasVCRedist(string) bool { return true }

func vcRedistStatus(string, string) VCRedistInfo { return VCRedistInfo{} }
