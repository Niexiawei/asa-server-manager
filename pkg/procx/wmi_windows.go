//go:build windows

package procx

import (
	"fmt"
	"strings"

	"github.com/yusufpapurcu/wmi"
)

// escapeWQL 转义 WQL 字符串字面量与 LIKE 通配符，两层各管各的：
//
//   - 字符串字面量这一层用**反斜杠**转义：`\` -> `\\`、`'` -> `\'`。不是 SQL 的
//     单引号翻倍——翻倍和裸反斜杠都会让 WMI 直接报「无效查询」（实测，
//     TestQueryProcess_EscapedQueryIsAccepted）。命令行里满是 Windows 路径，
//     反斜杠不转义的话，凡是带路径的片段都查不了。`\` 必须先于 `'` 替换，否则会
//     把刚插入的转义符再转一遍。
//   - LIKE 这一层不认反斜杠转义，通配符只能用方括号集合字面化：
//     `%` -> `[%]`、`_` -> `[_]`、`[` -> `[[]`；`]` 在集合外本就是字面量。
//     `[` 必须先于 `%`、`_` 替换，否则会把后面新插入的括号再转一遍。
func escapeWQL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "'", `\'`)
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
