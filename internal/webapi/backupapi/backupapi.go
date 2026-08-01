package backupapi

import (
	"asa-server/internal/backup"
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/logger"
	"asa-server/internal/webapi/apiresp"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"os"
	"path/filepath"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRouter(r *gin.Engine) {
	worldBackup := r.Group("/api/backup/world")
	{
		worldBackup.POST("/:name", h.backupInstanceWorld)
		worldBackup.GET("/:name", h.listWorldBackups)
		worldBackup.POST("/:name/restore", h.restoreWorldBackup)
		worldBackup.DELETE("/:name/:file", h.deleteWorldBackup)
	}
}

type RestoreWorldBackupRequest struct {
	BackupFile string `json:"backup_file" binding:"required"`
}

func (h *Handler) backupInstanceWorld(c *gin.Context) {
	name := c.Param("name")

	if err := backup.BackupInstanceWorld(name); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance '%s' backed up successfully", name),
	})
}

func (h *Handler) listWorldBackups(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   "instance name is required",
		})
		return
	}

	backups, err := backup.GetAvailableWorldBackups(name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "Backups retrieved successfully",
		Data: gin.H{
			"backups": backups,
			"count":   len(backups),
		},
	})
}

func (h *Handler) restoreWorldBackup(c *gin.Context) {
	name := c.Param("name")

	var req RestoreWorldBackupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if name == "" {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   "instance name is required (provide in URL path)",
		})
		return
	}

	// Resolve filename to full path within the instance's backup directory
	backupPath := filepath.Join(cfgpkg.InstancesDir, name, "backup", req.BackupFile)
	if _, err := os.Stat(backupPath); err != nil {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("backup file not found: %s", req.BackupFile),
		})
		return
	}

	// Save current state as latest snapshot before restoring (best-effort)
	if err := backup.BackupLatestWorldSnapshot(name); err != nil {
		logger.GetLogger().Warnf("Failed to create latest snapshot before restore for '%s': %v", name, err)
	}

	if err := backup.RestoreInstanceWorld(name, backupPath); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance '%s' restored successfully", name),
	})
}

func (h *Handler) deleteWorldBackup(c *gin.Context) {
	name := c.Param("name")
	file := c.Param("file")

	if name == "" || file == "" {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   "instance name and file name are required",
		})
		return
	}

	if err := backup.DeleteWorldBackup(name, file); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Backup '%s' deleted successfully", file),
	})
}
