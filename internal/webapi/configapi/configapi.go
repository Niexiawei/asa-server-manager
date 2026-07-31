package configapi

import (
	cfgpkg "asa-server/config"
	"asa-server/logger"
	"asa-server/webapi/apiresp"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"strings"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRouter(r *gin.Engine) {
	config := r.Group("/api/config")
	{
		config.GET("/server/configs", h.getServerConfigs)
		config.GET("/:name/configs", h.getInstanceConfigs)
		config.GET("/:name/game-ini", h.getGameIni)
		config.GET("/:name/game-user-settings", h.getGameUserSettings)
		config.POST("/:name/game-ini", h.uploadGameIni)
		config.POST("/:name/game-user-settings", h.uploadGameUserSettings)
		config.PUT("/:name/game-ini", h.updateGameIni)
		config.PUT("/:name/game-user-settings", h.updateGameUserSettings)
		config.POST("/sync-instance", h.syncInstanceConfig)
	}
}

type ConfigFileRequest struct {
	Content string `json:"content" binding:"required"`
}

type SyncInstanceConfigRequest struct {
	SourceInstance              string   `json:"source_instance" binding:"required"`
	TargetInstances             []string `json:"target_instances" binding:"required,min=1"`
	SyncCustomStartParameters   *bool    `json:"sync_custom_start_parameters,omitempty"`
	SyncEnableAsaPlugin         *bool    `json:"sync_enable_asa_plugin,omitempty"`
	OnlySyncServerGameINIConfig *bool    `json:"only_sync_server_game_ini_config,omitempty"`
}

func (h *Handler) getServerConfigs(c *gin.Context) {
	gameIniContent, gameIniErr := cfgpkg.GetServerGameIniContent()
	gameUserSettingsContent, gameUserSettingsErr := cfgpkg.GetServerGameUserSettingsContent()

	if gameIniErr != nil && gameUserSettingsErr != nil {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   "Both configuration files not found in server base directory",
		})
		return
	}

	// Build response data with error handling
	gameIniData := gin.H{
		"filename": "Game.ini",
		"content":  gameIniContent,
	}
	if gameIniErr != nil {
		gameIniData["error"] = gameIniErr.Error()
	}

	gameUserSettingsData := gin.H{
		"filename": "GameUserSettings.ini",
		"content":  gameUserSettingsContent,
	}
	if gameUserSettingsErr != nil {
		gameUserSettingsData["error"] = gameUserSettingsErr.Error()
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "Configuration files retrieved from server base directory",
		Data: gin.H{
			"game_ini":           gameIniData,
			"game_user_settings": gameUserSettingsData,
		},
	})
}

func (h *Handler) getInstanceConfigs(c *gin.Context) {
	instanceName := c.Param("name")

	gameIniContent, gameIniErr := cfgpkg.GetGameIniContent(instanceName)
	gameUserSettingsContent, gameUserSettingsErr := cfgpkg.GetGameUserSettingsContent(instanceName)

	if gameIniErr != nil && gameUserSettingsErr != nil {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   "Both configuration files not found",
		})
		return
	}

	// Build response data with error handling
	gameIniData := gin.H{
		"filename": "Game.ini",
		"content":  gameIniContent,
	}
	if gameIniErr != nil {
		gameIniData["error"] = gameIniErr.Error()
	}

	gameUserSettingsData := gin.H{
		"filename": "GameUserSettings.ini",
		"content":  gameUserSettingsContent,
	}
	if gameUserSettingsErr != nil {
		gameUserSettingsData["error"] = gameUserSettingsErr.Error()
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Configuration files retrieved for instance '%s'", instanceName),
		Data: gin.H{
			"game_ini":           gameIniData,
			"game_user_settings": gameUserSettingsData,
		},
	})
}

func (h *Handler) getGameIni(c *gin.Context) {
	instanceName := c.Param("name")

	content, err := cfgpkg.GetGameIniContent(instanceName)
	if err != nil {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Game.ini retrieved for instance '%s'", instanceName),
		Data: gin.H{
			"filename": "Game.ini",
			"content":  content,
		},
	})
}

func (h *Handler) getGameUserSettings(c *gin.Context) {
	instanceName := c.Param("name")

	content, err := cfgpkg.GetGameUserSettingsContent(instanceName)
	if err != nil {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("GameUserSettings.ini retrieved for instance '%s'", instanceName),
		Data: gin.H{
			"filename": "GameUserSettings.ini",
			"content":  content,
		},
	})
}

func (h *Handler) uploadGameIni(c *gin.Context) {
	instanceName := c.Param("name")

	// Get the uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   "No file provided",
		})
		return
	}

	// Validate file size (max 10MB)
	if file.Size > 10*1024*1024 {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   "File size exceeds 10MB limit",
		})
		return
	}

	// Read the file content
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to open file: %v", err),
		})
		return
	}
	defer src.Close()

	// Read all content
	content, err := io.ReadAll(src)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to read file: %v", err),
		})
		return
	}

	// Save the file content to the instance config directory
	if err := cfgpkg.SaveGameIniContent(instanceName, string(content)); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Game.ini uploaded and saved for instance '%s'", instanceName),
		Data: gin.H{
			"filename": file.Filename,
			"size":     file.Size,
		},
	})
}

func (h *Handler) uploadGameUserSettings(c *gin.Context) {
	instanceName := c.Param("name")

	// Get the uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   "No file provided",
		})
		return
	}

	// Validate file size (max 10MB)
	if file.Size > 10*1024*1024 {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   "File size exceeds 10MB limit",
		})
		return
	}

	// Read the file content
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to open file: %v", err),
		})
		return
	}
	defer src.Close()

	// Read all content
	content, err := io.ReadAll(src)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to read file: %v", err),
		})
		return
	}

	// Save the file content to the instance config directory
	if err := cfgpkg.SaveGameUserSettingsContent(instanceName, string(content)); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("GameUserSettings.ini uploaded and saved for instance '%s'", instanceName),
		Data: gin.H{
			"filename": file.Filename,
			"size":     file.Size,
		},
	})
}

func (h *Handler) updateGameIni(c *gin.Context) {
	instanceName := c.Param("name")

	var req ConfigFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Validate content is not empty
	if strings.TrimSpace(req.Content) == "" {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   "Content cannot be empty",
		})
		return
	}

	// Save the content to Game.ini
	if err := cfgpkg.SaveGameIniContent(instanceName, req.Content); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Game.ini updated successfully for instance '%s'", instanceName),
		Data: gin.H{
			"filename": "Game.ini",
			"size":     len(req.Content),
		},
	})
}

func (h *Handler) updateGameUserSettings(c *gin.Context) {
	instanceName := c.Param("name")

	var req ConfigFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Validate content is not empty
	if strings.TrimSpace(req.Content) == "" {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   "Content cannot be empty",
		})
		return
	}

	// Save the content to GameUserSettings.ini
	if err := cfgpkg.SaveGameUserSettingsContent(instanceName, req.Content); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("GameUserSettings.ini updated successfully for instance '%s'", instanceName),
		Data: gin.H{
			"filename": "GameUserSettings.ini",
			"size":     len(req.Content),
		},
	})
}

func (h *Handler) syncInstanceConfig(c *gin.Context) {
	var req SyncInstanceConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if len(req.TargetInstances) == 0 {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   "At least one target instance name is required",
		})
		return
	}

	var (
		opts []cfgpkg.SetSyncInstanceConfig
	)

	if req.OnlySyncServerGameINIConfig != nil {
		opts = append(opts, cfgpkg.WithOnlySyncServerGameINIConfig(*req.OnlySyncServerGameINIConfig))
	}

	if req.SyncCustomStartParameters != nil {
		opts = append(opts, cfgpkg.WithSyncCustomStartParameters(*req.SyncCustomStartParameters))
	}

	if req.SyncEnableAsaPlugin != nil {
		opts = append(opts, cfgpkg.WithSyncEnableAsaPlugin(*req.SyncEnableAsaPlugin))
	}

	// Sync config from source to each target instance
	results := cfgpkg.SyncInstanceConfigToMultiple(req.SourceInstance, req.TargetInstances,
		opts...,
	)

	// Separate successful and failed instances
	var successInstances []string
	var failedInstances []map[string]interface{}

	for instanceName, err := range results {
		if err != nil {
			failedInstances = append(failedInstances, map[string]interface{}{
				"instance": instanceName,
				"error":    err.Error(),
			})
			logger.GetLogger().Errorf("Failed to sync config for instance '%s': %v", instanceName, err)
		} else {
			successInstances = append(successInstances, instanceName)
			logger.GetLogger().Infof("Successfully synced config from '%s' to instance '%s'", req.SourceInstance, instanceName)
		}
	}

	// Return results
	if len(failedInstances) > 0 {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Message: fmt.Sprintf("Synced %d of %d target instances", len(successInstances), len(req.TargetInstances)),
			Error:   fmt.Sprintf("Failed to sync %d instances", len(failedInstances)),
			Data: gin.H{
				"synced_instances": successInstances,
				"failed_instances": failedInstances,
				"success_count":    len(successInstances),
				"failure_count":    len(failedInstances),
			},
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance configuration synced successfully from '%s' to %d target instances", req.SourceInstance, len(successInstances)),
		Data: gin.H{
			"synced_instances": successInstances,
			"count":            len(successInstances),
		},
	})
}
