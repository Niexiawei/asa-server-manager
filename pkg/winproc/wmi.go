package winproc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yusufpapurcu/wmi"
)

// WaitProcessExit blocks until the process with the given pid has exited or ctx is done.
// It polls process liveness every interval. Returns true once the process has exited,
// false if ctx was cancelled first.
//
// NOTE: previously this existed as two同名 functions with different poll intervals
// (asaserver 500ms for the ARK game process, common 2s for syncthing). They are unified
// here with an explicit interval parameter so callers keep their original cadence.
func WaitProcessExit(ctx context.Context, pid int, interval time.Duration) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(interval):
			if exited, _ := IsProcessExited(uint32(pid)); exited {
				return true
			}
		}
	}
}

// Win32Process is a subset of Win32_Process WMI properties.
// 字段名必须与 WMI 属性名一致：wmi.Query 靠反射按名字回填。
type Win32Process struct {
	Name      string
	ProcessId uint32
	// CommandLine 在 WMI 里可能是 NULL（权限不足或系统进程），
	// 此时 wmi 库跳过该字段，保持零值空串。
	CommandLine string
}

// escapeWQL 转义 WQL 字符串字面量与 LIKE 通配符。
//
// WQL 的 LIKE 不认反斜杠转义，通配符只能用方括号集合字面化：
// `%` -> `[%]`、`_` -> `[_]`、`[` -> `[[]`；`]` 在集合外本就是字面量。
// 单引号按 SQL 惯例翻倍。`[` 必须最先替换，否则会把后面新插入的括号再转一遍。
func escapeWQL(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	s = strings.ReplaceAll(s, "[", "[[]")
	s = strings.ReplaceAll(s, "%", "[%]")
	s = strings.ReplaceAll(s, "_", "[_]")
	return s
}

// QueryProcess queries Windows processes by name and optional command line.
func QueryProcess(name, commandLine string) ([]Win32Process, error) {
	query := fmt.Sprintf(
		`SELECT Name, ProcessId, CommandLine FROM Win32_Process WHERE Name LIKE '%%%s%%'`,
		escapeWQL(name),
	)
	if commandLine != "" {
		query += fmt.Sprintf(` AND CommandLine LIKE '%%%s%%'`, escapeWQL(commandLine))
	}

	// wmi.Query 内部自己做 COM 初始化，并用全局锁 + LockOSThread 串行化，可并发调用。
	var result []Win32Process
	if err := wmi.Query(query, &result); err != nil {
		return nil, err
	}
	return result, nil
}
