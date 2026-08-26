// Package systemapi exposes host-level diagnostics that aren't tied to any
// particular ARK instance. Today that's just the Linux runtime dependency
// self-check (docs/LINUX_COMPATIBILITY_PLAN.md §4.2) — a no-op returning an
// empty, healthy result on Windows.
package systemapi

import (
	"net/http"

	"asa-server/internal/runner"
	"asa-server/internal/webapi/apiresp"

	"github.com/gin-gonic/gin"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRouter(r *gin.Engine) {
	r.GET("/api/system/preflight", h.preflight)
}

// preflight reports host dependency problems that would stop the Linux
// Wine/Proton runtime from working (missing 32-bit glibc, python3, etc. —
// see runner.Preflight). Always empty/healthy on Windows.
func (h *Handler) preflight(c *gin.Context) {
	problems := runner.Preflight()
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Data: gin.H{
			"healthy":  len(problems) == 0,
			"problems": problems,
		},
	})
}
