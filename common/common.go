package common

import (
	"asa-server/win32api"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/microsoft/wmi/pkg/base/instance"
)

func WaitGamePidExit(ctx context.Context, pid int) bool {
	for {
		select {
		case <-ctx.Done():
			return false
			// Startup completed successfully via log detection
		case <-time.After(2 * time.Second):
			// Check if process is still running before timing out
			if exited, _ := win32api.IsProcessExited(uint32(pid)); exited {
				// Process is still running, consider it a success
				return true
			}
		}
	}
}

type Win32Process struct {
	Name        string
	ProcessId   uint32
	CommandLine string // 可能为 nil
}

// escapeWQL escapes special characters in WQL LIKE patterns
func escapeWQL(s string) string {
	// Escape single quotes and WQL LIKE wildcards
	s = strings.ReplaceAll(s, "'", "''")
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// QueryProcess queries Windows processes by name and optional command line
func QueryProcess(name, commandLine string) ([]Win32Process, error) {
	im, err := instance.GetWmiInstanceManager("", `root\cimv2`, "", "", "")
	if err != nil {
		return nil, err
	}
	args := []any{
		escapeWQL(name), // H10 fix: escape WQL injection
	}

	// WQL：查询进程名和 PID、命令行
	query := `SELECT Name, ProcessId, CommandLine FROM Win32_Process where Name like '%%%s%%'`
	if commandLine != "" {
		query += ` and CommandLine like '%%%s%%'`
		args = append(args, escapeWQL(commandLine)) // H10/M15 fix: escape WQL injection
	}
	query = fmt.Sprintf(query, args...)
	res, err := im.QueryInstances(query)
	if err != nil {
		return nil, err
	}
	result := make([]Win32Process, 0, len(res))
	// res 是 *[]Instance（接口），逐个读取属性
	for _, inst := range res {
		nameVal, _ := inst.GetProperty("Name")
		pidVal, _ := inst.GetProperty("ProcessId")
		cmdVal, _ := inst.GetProperty("CommandLine")

		// H11 fix: Use comma-ok pattern to prevent nil type assertion panic
		nameStr, _ := nameVal.(string)
		pidInt, _ := pidVal.(int32)
		cmdStr, _ := cmdVal.(string)

		result = append(result, Win32Process{
			Name:        nameStr,
			ProcessId:   uint32(pidInt),
			CommandLine: cmdStr,
		})
	}
	return result, nil
}
