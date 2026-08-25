// Package pluginapi 暴露 ArkApi 插件的**每实例**配置与运行期数据。
//
// 所有写操作落到实例目录（{BaseDir}/instances/{name}/plugins/{Plugin}/），
// 而不是镜像目录 —— 镜像随时会被重建，写进去等于白写。
// 实例目录里的这一份在下次启动时由 plugindata.Inject 注入镜像。
//
// 设计背景见 docs/ARKAPI_PLUGIN_DATA_PLAN.md。
package pluginapi

import (
	"net/http"

	"asa-server/internal/plugindata"
	"asa-server/internal/webapi/apiresp"

	"github.com/gin-gonic/gin"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRouter(r *gin.Engine) {
	plugins := r.Group("/api/plugins")
	{
		plugins.GET("/:name", h.listPlugins)
		plugins.GET("/:name/:plugin/config", h.getPluginConfig)
		plugins.PUT("/:name/:plugin/config", h.updatePluginConfig)
	}
}

type PluginConfigRequest struct {
	Content string `json:"content" binding:"required"`
}

func (h *Handler) listPlugins(c *gin.Context) {
	name := c.Param("name")
	if err := apiresp.ValidateInstanceName(name); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}

	plugins, err := plugindata.ListInstancePlugins(name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"plugins": plugins, "count": len(plugins)})
}

func (h *Handler) getPluginConfig(c *gin.Context) {
	name := c.Param("name")
	if err := apiresp.ValidateInstanceName(name); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}

	content, seeded, err := plugindata.ReadPluginConfig(name, c.Param("plugin"))
	if err != nil {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}
	// seeded=false 表示实例侧还没有独立配置，展示的是源服务端自带的默认值。
	// 前端应当据此提示「保存后才会成为本实例的配置」。
	c.JSON(http.StatusOK, gin.H{"content": content, "seeded": seeded})
}

func (h *Handler) updatePluginConfig(c *gin.Context) {
	name := c.Param("name")
	if err := apiresp.ValidateInstanceName(name); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}

	var req PluginConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}

	if err := plugindata.WritePluginConfig(name, c.Param("plugin"), req.Content); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "插件配置已保存，将在下次启动该实例时生效",
	})
}
