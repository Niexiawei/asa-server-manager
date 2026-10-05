// Package meshapi 暴露管理器互控的 HTTP 接口。P1 只有排障与验收用的两个：
// 本机状态与对指定节点发一次 Hello（docs/REMOTE_MANAGER_MESH_PLAN.md §12 P1-7）。
// 完整的 /api/mesh/* 在 P3/P4。两个接口都要求管理员。
package meshapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"asa-server/internal/mesh"
	"asa-server/internal/webapi/apiresp"
	"asa-server/internal/webapi/authapi"
	"asa-server/pkg/meshid"
)

const helloTimeout = 15 * time.Second

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRouter(r *gin.Engine) {
	g := r.Group("/api/mesh", authapi.RequireAdmin())
	g.GET("/status", h.status)
	g.POST("/hello/:node", h.hello)
}

func manager(c *gin.Context) *mesh.Manager {
	m := mesh.GetGlobalManager()
	if m == nil {
		fail(c, http.StatusInternalServerError, errors.New("管理器互控模块未初始化"))
	}
	return m
}

func fail(c *gin.Context, code int, err error) {
	c.JSON(code, apiresp.StatusResponse{Success: false, Message: err.Error(), Error: err.Error()})
}

func (h *Handler) status(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Data: m.Status()})
}

func (h *Handler) hello(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	id, err := meshid.ParseID(c.Param("node"))
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), helloTimeout)
	defer cancel()
	res, err := m.Hello(ctx, id)
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, mesh.ErrNotRunning) {
			code = http.StatusServiceUnavailable
		}
		fail(c, code, err)
		return
	}
	c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Data: res})
}
