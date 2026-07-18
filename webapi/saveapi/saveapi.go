package saveapi

import (
	cfgpkg "asa-server/config"
	"asa-server/parseserver"
	"asa-server/webapi/apiresp"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"os"
	"path/filepath"
)

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
		save.GET("/:instance/all", h.getSaveAll)
		save.GET("/:instance/stream", h.streamSaveData)
	}
}

func (h *Handler) getSavePlayers(c *gin.Context) {
	h.handleSaveParse(c, parseserver.ParseTypePlayers)
}

func (h *Handler) getSaveTribes(c *gin.Context) {
	h.handleSaveParse(c, parseserver.ParseTypeTribes)
}

func (h *Handler) getSaveAll(c *gin.Context) {
	instanceName := c.Param("instance")
	if instanceName == "" {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Message: "instance parameter is required",
			Error:   "missing instance parameter",
		})
		return
	}

	// Try cache first
	if h.saveMgr != nil {
		cached, err := h.saveMgr.GetCached(instanceName)
		if err == nil && cached != nil {
			c.JSON(http.StatusOK, apiresp.StatusResponse{
				Success: true,
				Message: "Save data retrieved from cache",
				Data:    cached,
			})
			return
		}
	}

	// Fallback to synchronous parse
	h.handleSaveParse(c, parseserver.ParseTypeAll)
}

func (h *Handler) handleSaveParse(c *gin.Context, parseType parseserver.ParseType) {
	if !parseserver.IsParserAvailable() {
		c.JSON(http.StatusServiceUnavailable, apiresp.StatusResponse{
			Success: false,
			Message: "Save parser not available",
			Error:   "save parser not available",
		})
		return
	}

	instanceName := c.Param("instance")
	if instanceName == "" {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Message: "instance parameter is required",
			Error:   "missing instance parameter",
		})
		return
	}

	savePath, err := findSaveFileByInstance(instanceName)
	if err != nil {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Message: "Save file not found",
			Error:   err.Error(),
		})
		return
	}

	result, err := parseserver.ParseSave(c.Request.Context(), savePath, parseType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to parse save file",
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "Save file parsed successfully",
		Data:    result.Data,
	})
}

func (h *Handler) streamSaveData(c *gin.Context) {
	instanceName := c.Param("instance")
	if instanceName == "" {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Message: "instance parameter is required",
		})
		return
	}

	if h.saveMgr == nil {
		c.JSON(http.StatusServiceUnavailable, apiresp.StatusResponse{
			Success: false,
			Message: "Save data manager not initialized",
		})
		return
	}

	// Set SSE headers once
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	// Send cached data immediately if available
	cached, err := h.saveMgr.GetCached(instanceName)
	if err == nil && cached != nil {
		data, _ := json.Marshal(map[string]any{
			"type":     "cached",
			"instance": instanceName,
			"data":     cached,
		})
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		c.Writer.Flush()
	}

	broadcaster := h.saveMgr.GetBroadcaster(instanceName)
	ch, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	c.Writer.Flush()

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.serverCtx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", msg)
			c.Writer.Flush()
		}
	}
}

func findSaveFileByInstance(instanceName string) (string, error) {
	config, err := cfgpkg.LoadInstanceConfig(instanceName)
	if err != nil {
		return "", fmt.Errorf("failed to load instance config for %s: %w", instanceName, err)
	}

	saveDir := filepath.Join(cfgpkg.InstancesDir, instanceName, "Save", config.MapName)
	savePath := filepath.Join(saveDir, config.MapName+".ark")
	if _, err := os.Stat(savePath); err != nil {
		return "", fmt.Errorf("save file not found for instance %s: %s", instanceName, savePath)
	}

	return savePath, nil
}
