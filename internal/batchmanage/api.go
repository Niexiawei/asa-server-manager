package batchmanage

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"asa-server/internal/realtime"

	"github.com/gin-gonic/gin"
)

// RegisterBatchRoutes 注册批量操作相关路由
func RegisterBatchRoutes(router *gin.Engine) {
	batch := router.Group("/api/server/batch")
	{
		batch.POST("/start", batchStartServers)
		batch.POST("/stop", batchStopServers)
		batch.POST("/restart", batchRestartServers)
		batch.GET("/status", getBatchStatus)
		batch.GET("/logs", streamBatchLogs)
		batch.POST("/cancel", cancelBatch)
		batch.POST("/skip", skipBatchInstance)
	}
}

func batchStartServers(c *gin.Context) {
	handleBatchOperation(c, BatchStart)
}

func batchStopServers(c *gin.Context) {
	handleBatchOperation(c, BatchStop)
}

func batchRestartServers(c *gin.Context) {
	handleBatchOperation(c, BatchRestart)
}

func handleBatchOperation(c *gin.Context, opType BatchOperationType) {
	var req BatchOperationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
			"error":   err.Error(),
		})
		return
	}

	mgr := GetGlobalManager()
	if mgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Batch manager not initialized",
		})
		return
	}

	op, err := mgr.StartOperation(opType, req.Instances, req.DelaySeconds, req.CountdownConfig(), UserOrigin())
	if err != nil {
		status := http.StatusConflict
		if err.Error() == "no instances to operate on" {
			status = http.StatusBadRequest
		}
		// 倒计时参数配错属于客户端错误，不该报 409
		if strings.Contains(err.Error(), "倒计时") || strings.Contains(err.Error(), "播报时间点") {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": "Failed to start batch operation",
			"error":   err.Error(),
		})
		return
	}

	// 构建预检结果
	type skippedInfo struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	}
	var skipped []skippedInfo
	eligible := 0
	for _, r := range op.InstanceResults {
		if r.Status == InstanceSkipped {
			skipped = append(skipped, skippedInfo{Name: r.InstanceName, Reason: r.Error})
		} else {
			eligible++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Batch %s operation started", string(opType)),
		"data": gin.H{
			"total":    len(op.Instances),
			"eligible": eligible,
			"skipped":  skipped,
		},
	})
}

func getBatchStatus(c *gin.Context) {
	mgr := GetGlobalManager()
	if mgr == nil {
		c.JSON(http.StatusOK, gin.H{"active": false})
		return
	}

	op := mgr.GetCurrent()
	if op == nil || op.Status != "running" {
		c.JSON(http.StatusOK, gin.H{"active": false})
		return
	}

	op.mu.RLock()
	defer op.mu.RUnlock()

	// 计算进度：skip_requested 尚未真正处理，不能提前计入
	done := 0
	for _, r := range op.InstanceResults {
		if r.Status != InstancePending && r.Status != InstanceRunning &&
			r.Status != InstanceSkipRequested {
			done++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"active": true,
		"type":   string(op.Type),
		"status": op.Status,
		"progress": gin.H{
			"done":  done,
			"total": len(op.Instances),
		},
		"instances": op.InstanceResults,
		// origin 说明这一轮批量是谁发起的（用户手动/定时任务/恢复启动等）。
		// 弹窗打开时可能批量早已在跑，光靠 WS 的 batch_started 事件拿不到——
		// 那条消息在弹窗打开之前就已经发过了
		"origin_kind":  string(op.Origin.Kind),
		"origin_label": op.Origin.Label,
	})
}

// streamBatchLogs 是一条**长连接**：连上就回放历史，之后新日志——包括连接期间
// 新发起的那几轮批量——继续在同一条流里推，直到客户端断开或进程退出。
//
// 千万别再因为「当前没有批量在跑」就提前 return。以前那样做的后果是：
// 200 已经提交，流却在亚毫秒内结束，而 EventSource 分不清「服务端正常收尾」和
// 「连接抖断」，于是按默认 3 秒无限重连，日志里全是 0ms 的 200。
// 连接的建立与断开由前端弹窗的开启状态决定，这里只管一直推。
func streamBatchLogs(c *gin.Context) {
	mgr := GetGlobalManager()
	if mgr == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Batch manager not initialized",
		})
		return
	}

	// 先订阅再回放，避免两者之间的日志漏帧
	subscriber, unsubscribe, ok := mgr.GetLogBroadcaster().Subscribe()
	defer unsubscribe()

	// 设置 SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.Flush()

	// 回放历史。用 GetCurrentOrLast：没有进行中的操作时回放最近一轮的日志；
	// 一次都没跑过时它是 nil，跳过回放照样把连接挂着等新日志
	if op := mgr.GetCurrentOrLast(); op != nil {
		for _, entry := range op.GetLogHistory() {
			data, _ := json.Marshal(entry)
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		}
		c.Writer.Flush()
	}

	// 广播器已停止（进程正在退出）：回放完就收工
	if !ok {
		sendStreamEnd(c)
		return
	}

	// 长时间没有批量时这条流是零流量的，定期发个注释帧免得被反向代理判空闲掐掉
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	ctx := c.Request.Context()
	for {
		select {
		case entry, chOpen := <-subscriber:
			if !chOpen {
				// 广播器被 Stop，即进程正在退出
				sendStreamEnd(c)
				return
			}
			data, _ := json.Marshal(entry)
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			c.Writer.Flush()
		case <-keepalive.C:
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()
		case <-ctx.Done():
			// 客户端主动断开（弹窗关了）：不用发 end，对面已经不听了
			return
		}
	}
}

// sendStreamEnd 告诉客户端这条流是**服务端主动收尾**的，别再重连。
//
// EventSource 分不清正常收尾和连接抖断，两者都是 error + readyState CONNECTING，
// 不显式说一声，浏览器就会按默认 3 秒一直重连。
func sendStreamEnd(c *gin.Context) {
	fmt.Fprint(c.Writer, "event: end\ndata: {}\n\n")
	c.Writer.Flush()
}

func cancelBatch(c *gin.Context) {
	mgr := GetGlobalManager()
	if mgr == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "No active batch operation",
		})
		return
	}

	if mgr.CancelCurrent() {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "Batch operation cancelled",
		})
	} else {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "No active batch operation to cancel",
		})
	}
}

func skipBatchInstance(c *gin.Context) {
	var req struct {
		InstanceName string `json:"instance_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "instance_name is required",
		})
		return
	}

	mgr := GetGlobalManager()
	if mgr == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "No active batch operation",
		})
		return
	}

	ok, reason := mgr.SkipInstance(req.InstanceName)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   reason,
		})
		return
	}

	// 广播放在锁外，使其它客户端立即同步到该实例的待跳过状态
	opType := ""
	if op := mgr.GetCurrent(); op != nil {
		opType = string(op.Type)
	}
	realtime.BroadcastBatchInstanceSkipped(opType, req.InstanceName)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Instance '%s' will be skipped", req.InstanceName),
	})
}
