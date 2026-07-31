package saveapi

import (
	"asa-server/parseserver"
	"asa-server/webapi/apiresp"
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

var errMissingInstance = errors.New("missing instance parameter")

type Handler struct {
	serverCtx context.Context
	saveMgr   *parseserver.SaveDataManager
}

func NewHandler(serverCtx context.Context, saveMgr *parseserver.SaveDataManager) *Handler {
	return &Handler{serverCtx: serverCtx, saveMgr: saveMgr}
}

func (h *Handler) RegisterRouter(r *gin.Engine) {
	save := r.Group("/api/save")
	{
		save.GET("/:instance/players", h.getSavePlayers)
		save.GET("/:instance/tribes", h.getSaveTribes)
	}
}

// getSavePlayers 返回玩家列表（§3.1）。
func (h *Handler) getSavePlayers(c *gin.Context) {
	data, err := h.resolveSaveData(c)
	if err != nil {
		return // 已在 resolveSaveData 内写响应
	}
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "玩家列表获取成功",
		Data:    data.Players,
	})
}

// getSaveTribes 返回富化后的部落列表（§3.2）。
func (h *Handler) getSaveTribes(c *gin.Context) {
	data, err := h.resolveSaveData(c)
	if err != nil {
		return
	}
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "部落列表获取成功",
		Data:    data.Tribes,
	})
}

// resolveSaveData 优先读监控器内存缓存，未命中回退一次性解析。
// 出错时已写好响应并返回 error。
func (h *Handler) resolveSaveData(c *gin.Context) (*parseserver.SaveData, error) {
	instanceName := c.Param("instance")
	if instanceName == "" {
		err := errMissingInstance
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Message: "缺少 instance 参数",
			Error:   err.Error(),
		})
		return nil, err
	}

	if h.saveMgr != nil {
		if data, ok := h.saveMgr.GetCurrent(instanceName); ok {
			return data, nil
		}
	}

	data, err := parseserver.ParseInstanceSave(c.Request.Context(), instanceName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Message: "解析存档失败",
			Error:   err.Error(),
		})
		return nil, err
	}
	return data, nil
}
