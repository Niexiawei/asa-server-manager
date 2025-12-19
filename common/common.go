package common

import (
	"asa-server/win32api"
	"context"
	"fmt"
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

func QueryProcess(name, commandLine string) ([]Win32Process, error) {
	im, err := instance.GetWmiInstanceManager("", `root\cimv2`, "", "", "")
	if err != nil {
		return nil, err
	}
	args := []any{
		name,
	}

	// WQL：查询进程名和 PID、命令行
	query := `SELECT Name, ProcessId, CommandLine FROM Win32_Process where Name like '%%%s%%'`
	if commandLine != "" {
		query += ` and CommandLine like '%%%s%%'`
		args = append(args, commandLine)
	}
	query = fmt.Sprintf(query, args...)
	res, err := im.QueryInstances(query)
	if err != nil {
		return nil, err
	}
	result := make([]Win32Process, 0, len(res))
	// res 是 *[]Instance（接口），逐个读取属性
	for _, inst := range res {
		name, _ := inst.GetProperty("Name")
		pid, _ := inst.GetProperty("ProcessId")
		cmd, _ := inst.GetProperty("CommandLine")
		result = append(result, Win32Process{
			Name:        name.(string),
			ProcessId:   uint32(pid.(int32)),
			CommandLine: cmd.(string),
		})
	}
	return result, nil
}
