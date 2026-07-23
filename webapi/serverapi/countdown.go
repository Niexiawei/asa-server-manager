package serverapi

import (
	instancepkg "asa-server/instance"
	"asa-server/webapi/apiresp"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// parseCountdownQuery 从 query string 解析倒计时配置。
//
// stop/restart 目前是 GET 路由，为不破坏既有前端调用，参数走 query 而非 body。
// 全部可选：不传 countdown 就是立即执行，与改动前行为一致。
//
//	?countdown=600&notify_points=600,300,60,30,10&notify_message=...&notify_command=ServerChat
func parseCountdownQuery(c *gin.Context) (*instancepkg.CountdownConfig, error) {
	raw := strings.TrimSpace(c.Query("countdown"))
	if raw == "" {
		return nil, nil
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("countdown 必须是整数秒: %s", raw)
	}
	if seconds == 0 {
		return nil, nil
	}
	if seconds < 0 {
		return nil, fmt.Errorf("countdown 不能为负数")
	}

	cfg := &instancepkg.CountdownConfig{
		Total:    time.Duration(seconds) * time.Second,
		Template: c.Query("notify_message"),
		Command:  c.Query("notify_command"),
	}

	points, err := parsePointsCSV(c.Query("notify_points"))
	if err != nil {
		return nil, err
	}
	cfg.Points = points

	// 在这里就校验，而不是等倒计时跑起来才发现点位永远触发不到
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// parsePointsCSV 解析 "600,300,60" 形式的播报点位。
func parsePointsCSV(raw string) ([]time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var points []time.Duration
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("notify_points 必须是逗号分隔的整数秒: %s", part)
		}
		points = append(points, time.Duration(n)*time.Second)
	}

	return points, nil
}

// getCountdown 查询实例当前的倒计时状态。
// WS 之外的兜底：页面首次加载时用它补上当前进度。
func (h *Handler) getCountdown(c *gin.Context) {
	name := c.Param("name")

	status, ok := instancepkg.GetCountdown(name)
	if !ok {
		c.JSON(http.StatusOK, apiresp.StatusResponse{
			Success: true,
			Data:    gin.H{"active": false},
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Data: gin.H{
			"active":    true,
			"action":    status.Action,
			"phase":     status.Phase,
			"remaining": status.Remaining,
		},
	})
}

// cancelCountdown 取消倒计时。实例状态由 instance 包回滚到 started。
func (h *Handler) cancelCountdown(c *gin.Context) {
	name := c.Param("name")

	if !instancepkg.CancelCountdown(name) {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("实例 '%s' 当前没有可取消的倒计时", name),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("实例 '%s' 的倒计时已取消", name),
	})
}
