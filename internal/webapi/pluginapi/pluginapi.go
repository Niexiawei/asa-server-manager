// Package pluginapi 暴露 ArkApi 插件的**每实例**状态与配置。
//
// 插件整个放在实例目录（{BaseDir}/instances/{name}/ArkApi/Plugins/）下，镜像里的
// ArkApi/Plugins 是指向它的 junction，所以配置直接读写实例目录即可。尚未迁移的实例
// （升级时正在运行）仍按旧布局读写 instances/{name}/plugins/，下次启动迁移时带过去。
// 两种情况都不写镜像：镜像随时会被重建。
//
// 设计背景见 docs/ARKAPI_PLUGIN_INSTALL_PLAN.md。
package pluginapi

import (
	"net/http"

	"asa-server/internal/installer"
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
	// arkapi_installed 让前端区分「没装主程序」与「装了但还没有插件」——
	// 两者的插件列表都是空的，而用户该做的事完全不同。
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Data: gin.H{
			"plugins":          plugins,
			"count":            len(plugins),
			"arkapi_installed": installer.ArkApiInstalled(),
			// layout=legacy：实例升级时正在运行、尚未迁移，列出的是 server-files 里的全局插件
			"layout":      plugindata.LayoutOf(name),
			"plugins_dir": plugindata.InstancePluginsDir(name),
		},
	})
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
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Data:    gin.H{"content": content, "seeded": seeded},
	})
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
