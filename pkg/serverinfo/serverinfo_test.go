package serverinfo

import (
	"os"
	"testing"
)

func TestGetMemoryInfo(t *testing.T) {
	info, err := GetMemoryInfo()
	if err != nil {
		t.Fatalf("获取内存信息失败: %v", err)
	}

	t.Logf("内存总量: %s (%d 字节)", FormatBytes(info.Total), info.Total)
	t.Logf("已使用内存: %s (%d 字节)", FormatBytes(info.Used), info.Used)
	t.Logf("可用内存: %s (%d 字节)", FormatBytes(info.Available), info.Available)
	t.Logf("内存使用率: %.2f%%", info.UsedPercent)

	if info.Total == 0 {
		t.Error("内存总量不应为0")
	}
	if info.UsedPercent < 0 || info.UsedPercent > 100 {
		t.Errorf("内存使用率应在0-100之间，实际值: %.2f", info.UsedPercent)
	}
}

func TestGetCPUInfo(t *testing.T) {
	info, err := GetCPUInfo()
	if err != nil {
		t.Fatalf("获取CPU信息失败: %v", err)
	}

	t.Logf("CPU核心数: %d", info.CoreCount)
	t.Logf("CPU使用率: %.2f%%", info.UsedPercent)

	if info.CoreCount <= 0 {
		t.Error("CPU核心数应大于0")
	}
	if info.UsedPercent < 0 || info.UsedPercent > 100 {
		t.Errorf("CPU使用率应在0-100之间，实际值: %.2f", info.UsedPercent)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    uint64
		expected string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
		{17179869184, "16.00 GB"},
	}

	for _, tt := range tests {
		result := FormatBytes(tt.bytes)
		if result != tt.expected {
			t.Errorf("FormatBytes(%d) = %s; 期望 %s", tt.bytes, result, tt.expected)
		}
	}
}

func TestGetProcessInfo(t *testing.T) {
	// 使用当前进程PID进行测试（原先这里硬编码了作者机器上的某个 PID，
	// 换台机器必然失败，与注释本身也对不上）
	currentPID := int32(os.Getpid())

	info, err := GetProcessInfo(currentPID)
	if err != nil {
		t.Fatalf("获取进程信息失败: %v", err)
	}

	t.Logf("进程PID: %d", info.PID)
	t.Logf("进程名称: %s", info.Name)
	t.Logf("CPU使用率: %.2f%%", info.CPUPercent)
	t.Logf("内存使用量: %s (%d 字节)", FormatBytes(info.MemoryUsed), info.MemoryUsed)
	t.Logf("内存使用率: %.2f%%", info.MemoryPercent)

	if info.PID != currentPID {
		t.Errorf("进程PID不匹配，期望: %d, 实际: %d", currentPID, info.PID)
	}
	if info.Name == "" {
		t.Error("进程名称不应为空")
	}
	if info.CPUPercent < 0 {
		t.Errorf("CPU使用率不应为负数，实际值: %.2f", info.CPUPercent)
	}
	if info.MemoryUsed == 0 {
		t.Error("内存使用量不应为0")
	}
	if info.MemoryPercent < 0 || info.MemoryPercent > 100 {
		t.Errorf("内存使用率应在0-100之间，实际值: %.2f", info.MemoryPercent)
	}
}

func TestGetProcessInfo_NonExistentPID(t *testing.T) {
	// 测试不存在的进程PID
	nonExistentPID := int32(999999)

	_, err := GetProcessInfo(nonExistentPID)
	if err == nil {
		t.Error("查询不存在的进程应返回错误")
	}
	t.Logf("预期的错误信息: %v", err)
}
