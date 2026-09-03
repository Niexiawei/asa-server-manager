package instanceapi

import (
	cfgpkg "asa-server/internal/config"
	instancepkg "asa-server/internal/instance"
	"asa-server/internal/mirror"
	procpkg "asa-server/internal/process"
	"asa-server/internal/runner"
	statepkg "asa-server/internal/state"
	"asa-server/internal/webapi/apiresp"
	"asa-server/pkg/fsutil"
	"asa-server/pkg/logger"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRouter(r *gin.Engine) {
	r.GET("/health", h.health)
	instances := r.Group("/api/instances")
	{
		instances.GET("", h.listInstances)
		instances.POST("", h.createInstance)
		instances.GET("/:name", h.getInstanceStatus)
		instances.DELETE("/:name", h.deleteInstance)
		instances.PUT("/:name", h.renameInstance)
		instances.GET("/:name/config", h.getInstanceConfig)
		instances.PATCH("/:name/config", h.updateInstanceConfig)
	}
	r.GET("/api/mod-info", h.getModInfo)
}

type InstanceInfo struct {
	Name          string                   `json:"name"`
	Running       bool                     `json:"running"`
	Config        interface{}              `json:"config,omitempty"`
	Status        string                   `json:"status"`
	StatusHistory []statepkg.InstanceState `json:"status_history"`
	AsaVersion    string                   `json:"asaVersion,omitempty"`
	Error         string                   `json:"error,omitempty"`
}

type ListResponse struct {
	Instances []InstanceInfo `json:"instances"`
	Count     int            `json:"count"`
}

type CreateInstanceRequest struct {
	Name string `json:"name" binding:"required"`
}

type RenameInstanceRequest struct {
	NewName string `json:"new_name" binding:"required"`
}

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "asa-server-manager-api",
	})
}

func (h *Handler) listInstances(c *gin.Context) {
	instances, err := cfgpkg.GetAvailableInstances()
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Message: "Failed to get instances",
			Error:   err.Error(),
		})
		return
	}

	var instanceInfos []InstanceInfo
	for _, instanceName := range instances {
		running, err := procpkg.IsServerRunning(instanceName)
		config, cfgErr := cfgpkg.LoadInstanceConfig(instanceName)
		_status := statepkg.GetInstanceStateOrDefault(instanceName)
		history, _ := statepkg.GetInstanceStateHistory(instanceName, 200)

		info := InstanceInfo{
			Name:          instanceName,
			Running:       running,
			Status:        string(_status.Status),
			StatusHistory: history,
		}

		if cfgErr == nil {
			info.Config = config
		}

		if version, versionErr := instancepkg.GetInstanceAsaVersion(instanceName); versionErr == nil {
			info.AsaVersion = version
		}

		if err != nil {
			info.Error = err.Error()
		}

		instanceInfos = append(instanceInfos, info)
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "Instances retrieved successfully",
		Data: ListResponse{
			Instances: instanceInfos,
			Count:     len(instanceInfos),
		},
	})
}

func (h *Handler) createInstance(c *gin.Context) {
	var req CreateInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if err := apiresp.ValidateInstanceName(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Create instance directory
	instanceDir := filepath.Join(cfgpkg.InstancesDir, req.Name, "Config")
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Copy base server configuration files to instance Config directory
	baseConfigDir := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")
	if _, err := os.Stat(baseConfigDir); err == nil {
		if err := fsutil.CopyDir(baseConfigDir, instanceDir); err != nil {
			// Log warning but continue as this is not critical
			logger.Warnf("Failed to copy base server configuration: %v", err)
		}
	} else {
		// Create empty Game.ini if it doesn't exist
		if err := cfgpkg.SaveGameIniContent(req.Name, ""); err != nil {
			logger.Warnf("Failed to create Game.ini: %v", err)
		}
		// Create empty GameUserSettings.ini if it doesn't exist
		if err := cfgpkg.SaveGameUserSettingsContent(req.Name, ""); err != nil {
			logger.Warnf("Failed to create GameUserSettings.ini: %v", err)
		}
	}

	// Check and create missing config files if directory exists but files don't
	gameIniPath := filepath.Join(instanceDir, "Game.ini")
	if _, err := os.Stat(gameIniPath); os.IsNotExist(err) {
		if err := cfgpkg.SaveGameIniContent(req.Name, ""); err != nil {
			logger.Warnf("Failed to create Game.ini: %v", err)
		}
	}

	gameUserSettingsPath := filepath.Join(instanceDir, "GameUserSettings.ini")
	if _, err := os.Stat(gameUserSettingsPath); os.IsNotExist(err) {
		if err := cfgpkg.SaveGameUserSettingsContent(req.Name, ""); err != nil {
			logger.Warnf("Failed to create GameUserSettings.ini: %v", err)
		}
	}

	// Create default configuration
	config := cfgpkg.CreateDefaultInstanceConfig(req.Name)

	if err := cfgpkg.SaveInstanceConfig(req.Name, config); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance '%s' created successfully", req.Name),
	})
}

func (h *Handler) getInstanceStatus(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   "Instance not found",
		})
		return
	}
	running, err := procpkg.IsServerRunning(name)
	config, cfgErr := cfgpkg.LoadInstanceConfig(name)
	_status := statepkg.GetInstanceStateOrDefault(name)
	history, _ := statepkg.GetInstanceStateHistory(name, 200)
	info := InstanceInfo{
		Name:          name,
		Running:       running,
		Status:        string(_status.Status),
		StatusHistory: history,
	}

	if cfgErr == nil {
		info.Config = config
	}

	if version, versionErr := instancepkg.GetInstanceAsaVersion(name); versionErr == nil {
		info.AsaVersion = version
	}

	if err != nil && cfgErr != nil {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   "Instance not found",
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "Instance status retrieved",
		Data:    info,
	})
}

func (h *Handler) deleteInstance(c *gin.Context) {
	name := c.Param("name")
	if err := apiresp.ValidateInstanceName(name); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}

	// Stop instance if running
	if running, _ := procpkg.IsServerRunning(name); running {
		if err := instancepkg.StopServer(name); err != nil {
			c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
				Success: false,
				Error:   fmt.Sprintf("Failed to stop server: %v", err),
			})
			return
		}
	}

	// 镜像目录（server-files-tmp-<name>）不归 StopServer 管：正常停止**故意保留**它
	// 以便下次秒起，只有 ForceStopServer 才清。所以删除实例时必须自己清一次，否则
	// 盘上会永远留着一个几百 MB 的真实文件拷贝，外加一堆指向已删实例目录的链接 ——
	// 而且没有任何东西会报告或回收它（`prefix gc` 只管 Wine 前缀）。
	//
	// 必须在删除实例目录**之前**：CleanupInstanceMirror 内部会把镜像里的插件数据
	// 抢救回 instances/<name>/，删完再调只会把那个目录重新建出来。
	if err := mirror.CleanupInstanceMirror(name); err != nil {
		logger.Warnf("清理实例 %s 的镜像目录失败: %v", name, err)
	}

	// Delete instance directory
	instanceDir := filepath.Join(cfgpkg.InstancesDir, name)
	if err := os.RemoveAll(instanceDir); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Delete save directories (v2: save is under instances/<name>/Save)
	savePath := filepath.Join(cfgpkg.InstancesDir, name, "Save")
	os.RemoveAll(savePath)

	// 该实例在 prefix_mode: per-instance 下拥有的独立 Wine 前缀（Windows 与
	// 从未用过该模式时都是 no-op）。只告警不失败：实例本体已经删掉了，为一个
	// 残留的运行时目录把整个删除报成失败没有意义，`asa-server prefix gc` 会兜底。
	if err := runner.RemoveInstancePrefix(name); err != nil {
		logger.Warnf("删除实例 %s 的独立 Wine 前缀失败（可稍后执行 asa-server prefix gc 清理）: %v", name, err)
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance '%s' deleted successfully", name),
	})
}

func (h *Handler) renameInstance(c *gin.Context) {
	oldName := c.Param("name")
	if err := apiresp.ValidateInstanceName(oldName); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}

	var req RenameInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if err := apiresp.ValidateInstanceName(req.NewName); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Stop instance if running
	if running, _ := procpkg.IsServerRunning(oldName); running {
		if err := instancepkg.StopServer(oldName); err != nil {
			c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
				Success: false,
				Error:   fmt.Sprintf("Failed to stop server: %v", err),
			})
			return
		}
	}

	// 旧名字的镜像目录同样得清，理由同 deleteInstance；改名后它再也不会被任何
	// 实例使用。也必须在 os.Rename **之前**：抢救插件数据的目的地是
	// instances/<oldName>/，改完名再调会凭空造出一个旧名字的实例目录。
	// 镜像本身没有独有数据，新名字下次启动会重新同步出来。
	if err := mirror.CleanupInstanceMirror(oldName); err != nil {
		logger.Warnf("清理实例 %s 的旧镜像目录失败: %v", oldName, err)
	}

	// Rename instance directory
	oldPath := filepath.Join(cfgpkg.InstancesDir, oldName)
	newPath := filepath.Join(cfgpkg.InstancesDir, req.NewName)

	if err := os.Rename(oldPath, newPath); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Rename save directories (v2: save is under instances/<name>/Save)
	oldSavePath := filepath.Join(cfgpkg.InstancesDir, oldName, "Save")
	newSavePath := filepath.Join(cfgpkg.InstancesDir, req.NewName, "Save")
	if err := os.Rename(oldSavePath, newSavePath); err != nil {
		logger.Warnf("Failed to rename save directory for instance %s: %v", oldName, err)
	}

	// 独立 Wine 前缀跟着旧名字走，这里**删除而不是改名**：Wine 前缀内部多处
	// 记录着自己的绝对路径，mv 不保证安全；而前缀里没有任何用户数据（存档在
	// instances/<name>/Save），删掉后新名字下次启动会自动重建，代价约一分钟。
	// 见 docs/UMU_PREFIX_PER_INSTANCE_PLAN.md §9.2。
	if err := runner.RemoveInstancePrefix(oldName); err != nil {
		logger.Warnf("清理实例 %s 的旧 Wine 前缀失败（可稍后执行 asa-server prefix gc 清理）: %v", oldName, err)
	}

	// Update SaveDir in configuration
	config, err := cfgpkg.LoadInstanceConfig(req.NewName)
	if err == nil {
		config.SaveDir = req.NewName
		cfgpkg.SaveInstanceConfig(req.NewName, config)
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance renamed from '%s' to '%s'", oldName, req.NewName),
	})
}

func (h *Handler) getInstanceConfig(c *gin.Context) {
	instanceName := c.Param("name")

	config, err := cfgpkg.LoadInstanceConfig(instanceName)
	if err != nil {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to load instance config: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance config retrieved for '%s'", instanceName),
		Data:    config,
	})
}

func (h *Handler) updateInstanceConfig(c *gin.Context) {
	instanceName := c.Param("name")

	var req cfgpkg.UpdateInstanceConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	if err := cfgpkg.UpdateInstanceConfig(instanceName, req); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: fmt.Sprintf("Instance config updated successfully for '%s'", instanceName),
	})
}

func (h *Handler) getModInfo(c *gin.Context) {
	// Construct the path to mod_info.json
	modInfoPath := filepath.Join(cfgpkg.BaseDir, "mod_info.json")

	// Check if the file exists
	if _, err := os.Stat(modInfoPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{
			Success: false,
			Error:   "mod_info.json file not found",
		})
		return
	}

	// Read the file
	file, err := os.Open(modInfoPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to open mod_info.json: %v", err),
		})
		return
	}
	defer file.Close()

	// Decode JSON content
	var modInfo []instancepkg.ModInfo
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&modInfo); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to decode mod_info.json: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "Mod info retrieved successfully",
		Data:    modInfo,
	})
}
