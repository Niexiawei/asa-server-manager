package serverinfo

import (
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

// MemoryInfo 内存信息结构体
type MemoryInfo struct {
	Total       uint64  // 总内存（字节）
	Used        uint64  // 已使用内存（字节）
	Available   uint64  // 可用内存（字节）
	UsedPercent float64 // 使用率（百分比）
}

// CPUInfo CPU信息结构体
type CPUInfo struct {
	CoreCount   int     // CPU核心数
	UsedPercent float64 // CPU使用率（百分比）
}

// ProcessInfo 进程信息结构体
type ProcessInfo struct {
	PID           int32   // 进程ID
	Name          string  // 进程名称
	CPUPercent    float64 // CPU使用率（百分比）
	MemoryUsed    uint64  // 内存使用量（字节）
	MemoryPercent float64 // 内存使用率（百分比）
}

// GetMemoryInfo 获取内存信息
// 返回内存总量、已使用量、可用量和使用率
func GetMemoryInfo() (*MemoryInfo, error) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("获取内存信息失败: %w", err)
	}

	return &MemoryInfo{
		Total:       v.Total,
		Used:        v.Used,
		Available:   v.Available,
		UsedPercent: v.UsedPercent,
	}, nil
}

// GetCPUInfo 获取CPU信息
// 返回CPU核心数和使用率
func GetCPUInfo() (*CPUInfo, error) {
	// 获取CPU核心数
	counts, err := cpu.Counts(true)
	if err != nil {
		return nil, fmt.Errorf("获取CPU核心数失败: %w", err)
	}

	// 获取CPU使用率（采样时间200毫秒）
	percentages, err := cpu.Percent(200*time.Millisecond, false)
	if err != nil {
		return nil, fmt.Errorf("获取CPU使用率失败: %w", err)
	}

	var usedPercent float64
	if len(percentages) > 0 {
		usedPercent = percentages[0]
	}

	return &CPUInfo{
		CoreCount:   counts,
		UsedPercent: usedPercent,
	}, nil
}

// GetProcessInfo 根据进程PID获取进程信息
// 返回进程的CPU使用率、内存使用量和内存使用率
func GetProcessInfo(pid int32) (*ProcessInfo, error) {
	// 检查进程是否存在
	exists, err := process.PidExists(pid)
	if err != nil {
		return nil, fmt.Errorf("检查进程是否存在失败: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("进程 PID %d 不存在", pid)
	}

	// 创建进程对象
	p, err := process.NewProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("创建进程对象失败: %w", err)
	}

	// 获取进程名称
	name, err := p.Name()
	if err != nil {
		name = "Unknown"
	}

	// 获取CPU使用率（采样时间200ms）
	cpuPercent, err := p.CPUPercent()
	if err != nil {
		return nil, fmt.Errorf("获取进程CPU使用率失败: %w", err)
	}

	// 获取内存信息
	memInfo, err := p.MemoryInfo()
	if err != nil {
		return nil, fmt.Errorf("获取进程内存信息失败: %w", err)
	}

	// 获取内存使用百分比
	memPercent, err := p.MemoryPercent()
	if err != nil {
		return nil, fmt.Errorf("获取进程内存使用率失败: %w", err)
	}

	return &ProcessInfo{
		PID:           pid,
		Name:          name,
		CPUPercent:    cpuPercent,
		MemoryUsed:    memInfo.RSS, // 使用RSS（常驻内存集）
		MemoryPercent: float64(memPercent),
	}, nil
}

// FormatBytes 格式化字节大小为易读格式
func FormatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
