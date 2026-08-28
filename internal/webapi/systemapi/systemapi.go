// Package systemapi exposes host-level diagnostics that aren't tied to any
// particular ARK instance: the Linux runtime dependency self-check
// (docs/LINUX_COMPATIBILITY_PLAN.md §4.2) plus the base-environment
// readiness bits (docs/SETUP_FLOW_OPTIMIZATION_PLAN.md §3.6). On Windows the
// preflight is empty and healthy; the readiness bits still reflect whether
// SteamCMD / the ARK server files are installed.
package systemapi

import (
	"net/http"

	"asa-server/internal/installer"
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
// Wine/Proton runtime from working (missing 32-bit glibc, python3, etc.) plus
// per-component readiness so the frontend can steer the user to
// `asa-server setup` when the environment isn't initialised.
func (h *Handler) preflight(c *gin.Context) {
	problems := runner.Preflight()
	runtimeErr := runner.CheckRuntime()
	st := installer.CheckInstalled()

	runtimeMsg := ""
	if runtimeErr != nil {
		runtimeMsg = runtimeErr.Error()
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Data: gin.H{
			"healthy":           len(problems) == 0,
			"problems":          problems,
			"runtimeReady":      runtimeErr == nil,
			"runtimeMessage":    runtimeMsg,
			"steamCmdReady":     st.SteamCmdReady,
			"serverBinaryReady": st.ServerBinaryReady,
			"serverConfigReady": st.ServerConfigReady,
			"environmentReady":  runtimeErr == nil && st.Ready(),
			// Drop-privileges runtime-user state (Linux; always {ready:true} on
			// Windows). See docs/UMU_RUNTIME_USER_PLAN.md §4.3.
			"umuRuntimeUser":         runner.RuntimeUserStatus(),
			"umuRuntimeUserProblems": runner.RuntimeUserProblems(),
			// Which Python interpreter umu-run runs under (Linux; empty on
			// Windows). See docs/UMU_PYTHON_DISCOVERY_PLAN.md.
			"umuPython": runner.RuntimePython(),
		},
	})
}
