package serverapi

import (
	"asa-server/internal/countdown"
	"asa-server/internal/webapi/apiresp"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// getCountdown 查询实例当前的倒计时状态。
// WS 之外的兜底：页面首次加载时用它补上当前进度。
func (h *Handler) getCountdown(c *gin.Context) {
	name := c.Param("name")

	status, ok := countdown.Get(name)
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

// cancelCountdown 取消倒计时。状态由 countdown 包回滚到 started。
//
// 批量操作中的实例也走这个入口：只放过这一台，其余实例照常执行。
// 要终止整批请用 batchmanage 的取消接口。
func (h *Handler) cancelCountdown(c *gin.Context) {
	name := c.Param("name")

	if !countdown.Cancel(name) {
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
