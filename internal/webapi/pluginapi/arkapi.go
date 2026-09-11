package pluginapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"asa-server/internal/arkapimanage"
	"asa-server/internal/webapi/apiresp"
	"asa-server/internal/webapi/authapi"

	"github.com/gin-gonic/gin"
)

// ArkApi 包的上传安装与插件的跨实例操作（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §8.2）。
//
// 写操作一律要求管理员：往服务器上放 dll 等同于在服务器上执行代码。

func (h *Handler) registerArkApiRoutes(r *gin.Engine) {
	g := r.Group("/api/arkapi")
	{
		g.POST("/packages", authapi.RequireAdmin(), h.uploadPackage)
		g.POST("/packages/:token/apply", authapi.RequireAdmin(), h.applyPackage)
		g.DELETE("/packages/:token", authapi.RequireAdmin(), h.discardPackage)
		g.GET("/plugins/:plugin/instances", h.pluginInstances)
		g.POST("/plugins/:plugin/uninstall", authapi.RequireAdmin(), h.uninstallPlugin)
	}
}

// multipartOverhead 是 multipart 封装本身（边界、part 头）的余量
const multipartOverhead = 1 << 20

func (h *Handler) uploadPackage(c *gin.Context) {
	if kind := c.DefaultQuery("kind", "plugin"); kind != "plugin" {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: "暂不支持上传该类型的包: " + kind})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, arkapimanage.MaxUploadBytes+multipartOverhead)
	mr, err := c.Request.MultipartReader()
	if err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: "请求不是 multipart/form-data: " + err.Error()})
		return
	}
	// 直接把 file 字段流进暂存区，不经 gin 的 FormFile（那会先在系统临时目录落一份）
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: "请求里没有 file 字段"})
			return
		}
		if err != nil {
			respondUploadError(c, err)
			return
		}
		if part.FormName() != "file" {
			continue
		}
		stage, err := arkapimanage.StagePlugin(part, part.FileName(), c.Query("expect"))
		if err != nil {
			respondUploadError(c, err)
			return
		}
		if len(stage.Errors) > 0 {
			// 422 且 data 形状与成功时相同（token 为空），前端据此展示逐条错误
			c.JSON(http.StatusUnprocessableEntity, apiresp.StatusResponse{Success: false, Error: stage.Errors[0], Data: stage})
			return
		}
		c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Data: stage})
		return
	}
}

func respondUploadError(c *gin.Context, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		c.JSON(http.StatusRequestEntityTooLarge, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("上传的文件超过 %d MiB 上限", arkapimanage.MaxUploadBytes>>20),
		})
		return
	}
	c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{Success: false, Error: "接收上传失败: " + err.Error()})
}

type ApplyPackageRequest struct {
	Targets           []string `json:"targets"`
	RestoreFromBackup bool     `json:"restore_from_backup"`
}

func (h *Handler) applyPackage(c *gin.Context) {
	var req ApplyPackageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}
	if err := validateTargets(req.Targets); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}
	results, err := arkapimanage.ApplyPlugin(c.Param("token"), req.Targets, req.RestoreFromBackup)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}
	respondResults(c, results)
}

func (h *Handler) discardPackage(c *gin.Context) {
	arkapimanage.Discard(c.Param("token"))
	c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true})
}

func (h *Handler) pluginInstances(c *gin.Context) {
	plugin := c.Param("plugin")
	targets, err := arkapimanage.PluginInstances(plugin)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Data:    gin.H{"plugin": plugin, "instances": targets},
	})
}

type UninstallPluginRequest struct {
	Targets []string `json:"targets"`
}

func (h *Handler) uninstallPlugin(c *gin.Context) {
	var req UninstallPluginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}
	if err := validateTargets(req.Targets); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}
	results, err := arkapimanage.UninstallPlugin(c.Param("plugin"), req.Targets)
	if err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}
	respondResults(c, results)
}

// validateTargets 挡住路径穿越，并拒绝空列表——接口里不存在「默认全部」（方案 §1.1）。
func validateTargets(targets []string) error {
	if len(targets) == 0 {
		return errors.New("没有选择目标实例")
	}
	for _, t := range targets {
		if err := apiresp.ValidateInstanceName(t); err != nil {
			return fmt.Errorf("实例名 %q 无效: %w", t, err)
		}
	}
	return nil
}

// respondResults 按实例报告结果。部分失败也返回 200：每个实例的成败都在 results 里。
func respondResults(c *gin.Context, results []arkapimanage.Result) {
	failed := 0
	for _, r := range results {
		if !r.OK {
			failed++
		}
	}
	msg := fmt.Sprintf("已在 %d 个实例上完成", len(results)-failed)
	if failed > 0 {
		msg += fmt.Sprintf("，%d 个失败", failed)
	}
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: failed == 0,
		Message: msg,
		Data:    gin.H{"results": results},
	})
}
