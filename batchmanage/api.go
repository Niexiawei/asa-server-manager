package batchmanage

import (
	"encoding/json"
	"fmt"
	"net/http"

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

	op, err := mgr.StartOperation(opType, req.Instances, req.DelaySeconds)
	if err != nil {
		status := http.StatusConflict
		if err.Error() == "no instances to operate on" {
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

	// 计算进度
	done := 0
	for _, r := range op.InstanceResults {
		if r.Status != InstancePending && r.Status != InstanceRunning {
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
	})
}

func streamBatchLogs(c *gin.Context) {
	mgr := GetGlobalManager()
	if mgr == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "No active batch operation",
		})
		return
	}

	op := mgr.GetCurrent()
	if op == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "No active batch operation",
		})
		return
	}

	// 设置 SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.Flush()

	// 先回放历史日志
	history := op.GetLogHistory()
	for _, entry := range history {
		data, _ := json.Marshal(entry)
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		c.Writer.Flush()
	}

	// 订阅新日志
	subscriber, unsubscribe := op.GetLogBroadcaster().Subscribe()
	defer unsubscribe()

	ctx := c.Request.Context()
	for {
		select {
		case entry, ok := <-subscriber:
			if !ok {
				return
			}
			data, _ := json.Marshal(entry)
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			c.Writer.Flush()
		case <-ctx.Done():
			return
		}
	}
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

	if mgr.SkipInstance(req.InstanceName) {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": fmt.Sprintf("Instance '%s' will be skipped", req.InstanceName),
		})
	} else {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Instance '%s' not found or already processed", req.InstanceName),
		})
	}
}
