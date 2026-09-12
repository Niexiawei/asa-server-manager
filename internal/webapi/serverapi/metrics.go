package serverapi

import (
	procpkg "asa-server/internal/process"
	"asa-server/pkg/serverinfo"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// buildAllInstancesPayload 组装 /api/server/all-info 的一帧。
//
// 采样全部来自 serverinfo 的单例采样器，handler 只做 JSON 组装。以前这里每个 SSE
// 连接每轮都要自己 cpu.Percent(200ms) 阻塞一次、各自 NewProcess 取「创建至今的平均
// CPU」，多客户端下是重复且不准的。
//
// 「哪些实例在跑」由 procpkg.RunningInstances 回答，采样器的目标源
// （internal/webapi/actions.go）问的是**同一个函数**：各写一份判据的话，会出现
// 「载荷里显示在跑、曲线却是空洞」这种只能靠对时间轴才发现的错位。
func buildAllInstancesPayload() (map[string]any, error) {
	running := procpkg.RunningInstances()

	snap := serverinfo.Snapshot()
	if snap == nil {
		return nil, fmt.Errorf("资源采样器尚未就绪")
	}

	cores := snap.Host.CoreCount
	instancesData := make([]any, 0, len(running))
	for _, ri := range running {
		rates, ok := snap.ByName[ri.Name]
		if !ok {
			// 实例刚启动，采样器还没赶上这一轮（它下一个周期才会发现这个目标）。
			// 直接补一次一次性采样，保证载荷结构永远完整——前端的
			// formatInstanceData 会对 cpu_percent 调 toFixed，给 null 会当场抛异常。
			info, err := serverinfo.GetProcessInfo(int32(ri.PID))
			if err != nil {
				continue
			}
			rates = serverinfo.ProcRates{
				PID:           info.PID,
				Name:          info.Name,
				CPUPercent:    info.CPUPercent,
				MemoryUsed:    info.MemoryUsed,
				MemoryPercent: info.MemoryPercent,
			}
		}

		cpuTotal := 0.0
		if cores > 0 {
			cpuTotal = rates.CPUPercent / float64(cores)
		}

		instanceData := map[string]any{
			"instance":          ri.Name,
			"running":           true,
			"pid":               ri.PID,
			"cpu_percent":       rates.CPUPercent,
			"cpu_total_percent": cpuTotal,
			"memory_used":       rates.MemoryUsed,
			"memory_percent":    rates.MemoryPercent,
			"process_name":      rates.Name,
			"memory_used_mb":    float64(rates.MemoryUsed) / (1024 * 1024),
			"memory_used_gb":    float64(rates.MemoryUsed) / (1024 * 1024 * 1024),
			"disk_io":           procDiskIO(rates),
			"net_io":            procNetIO(rates),
		}
		instancesData = append(instancesData, instanceData)
	}

	return map[string]any{
		"timestamp":     snap.Timestamp.Unix(),
		"cpu_cores":     cores,
		"running_count": len(instancesData),
		"host": map[string]any{
			"cpu": map[string]any{
				"used_percent": snap.Host.CPUUsedPercent,
				"core_count":   cores,
			},
			"memory": map[string]any{
				"used":         snap.Host.MemUsed,
				"total":        snap.Host.MemTotal,
				"used_percent": snap.Host.MemUsedPercent,
			},
			"disk_io": map[string]any{
				"read_bytes_per_sec":  snap.Host.DiskReadBytesPS,
				"write_bytes_per_sec": snap.Host.DiskWriteBytesPS,
				"read_iops":           snap.Host.DiskReadIOPS,
				"write_iops":          snap.Host.DiskWriteIOPS,
			},
			"net_io": map[string]any{
				"recv_bytes_per_sec": snap.Host.NetRecvBytesPS,
				"sent_bytes_per_sec": snap.Host.NetSentBytesPS,
			},
		},
		// 保留旧字段：前端的进度条组件还在读它，改动纯增量
		"memory": map[string]any{
			"total":    snap.Host.MemTotal,
			"total_gb": float64(snap.Host.MemTotal) / (1024 * 1024 * 1024),
		},
		"instances": instancesData,
	}, nil
}

// procDiskIO 采不到时整块返回 nil（JSON null），与「速率真的是 0」区分开。
func procDiskIO(r serverinfo.ProcRates) any {
	if r.IOReadBytesPS == nil || r.IOWriteBytesPS == nil {
		return nil
	}
	return map[string]any{
		"read_bytes_per_sec":  *r.IOReadBytesPS,
		"write_bytes_per_sec": *r.IOWriteBytesPS,
		"read_iops":           r.IOReadIOPS,
		"write_iops":          r.IOWriteIOPS,
	}
}

// procNetIO 在按进程网络计量可用时才非 nil（Linux 走 eBPF、Windows 走 ETW，
// 都要前置条件：权限、内核支持）。采不到时整块为 null，与「速率真的是 0」区分开。
func procNetIO(r serverinfo.ProcRates) any {
	if r.NetRxBytesPS == nil || r.NetTxBytesPS == nil {
		return nil
	}
	return map[string]any{
		"recv_bytes_per_sec": *r.NetRxBytesPS,
		"sent_bytes_per_sec": *r.NetTxBytesPS,
	}
}

// getMetricsHistory 回填接口：面板挂载时先拉这个把缓冲灌满，再订阅实时流。
//
// 为什么不能让 SSE 首帧带 backfill：SharedWorker 全浏览器只有一条 all-info 长连接，
// 中途挂载的面板收不到首帧（详见 docs/RESOURCE_RATE_CHART_PLAN.md §3.2.1）。
func (h *Handler) getMetricsHistory(c *gin.Context) {
	window := 15 * time.Minute
	if raw := c.Query("window"); raw != "" {
		if sec, err := strconv.Atoi(raw); err == nil && sec > 0 {
			window = time.Duration(sec) * time.Second
		}
	}
	if window > serverinfo.HistoryWindow {
		window = serverinfo.HistoryWindow
	}

	c.JSON(http.StatusOK, serverinfo.GetHistory(window, c.Query("instance")))
}
