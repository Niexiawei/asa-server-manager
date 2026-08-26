//go:build windows

package procx

import (
	"fmt"
	"strings"

	"github.com/yusufpapurcu/wmi"
)

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

// processCmdline returns the command line of the process with the given
// pid via a direct ProcessId lookup (no name filter).
//
// Selects every Win32Process field, not just CommandLine: the wmi library
// maps the query's selected columns onto the destination struct by name,
// and errors if a struct field has no matching column in the result set.
func processCmdline(pid uint32) (string, error) {
	query := fmt.Sprintf(`SELECT Name, ProcessId, CommandLine FROM Win32_Process WHERE ProcessId=%d`, pid)
	var result []Win32Process
	if err := wmi.Query(query, &result); err != nil {
		return "", err
	}
	if len(result) == 0 {
		return "", fmt.Errorf("process %d not found", pid)
	}
	return result[0].CommandLine, nil
}
