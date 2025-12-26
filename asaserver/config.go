package asaserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	BaseDir        string
	InstancesDir   string
	ServerFilesDir string
	SteamCmdDir    string
	BackupsDir     string
	ConfigFile     string
	LogMappingFile string
)

const (
	SteamCmdURL = "https://steamcdn-a.akamaihd.net/client/installer/steamcmd.zip"
)

// InstanceConfig represents an instance configuration
type InstanceConfig struct {
	ServerName            string
	ServerPassword        string
	ServerAdminPassword   string
	MaxPlayers            int
	MapName               string
	RCONPort              int
	QueryPort             int
	Port                  int
	ModIDs                string
	SaveDir               string
	ClusterID             string
	CustomStartParameters string
	EnableAsaPlugin       bool
	BindDomain            string
}

// EnsureDirectories Initialize directories based on executable location
func EnsureDirectories() error {
	// Check if ASA_BASEDIR environment variable is set
	baseDirEnv := os.Getenv("ASA_BASEDIR")
	if baseDirEnv != "" {
		BaseDir = baseDirEnv
	} else {
		// Get the directory where the executable is located
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}
		BaseDir = filepath.Dir(exe)
	}
	InstancesDir = filepath.Join(BaseDir, "instances")
	ServerFilesDir = filepath.Join(BaseDir, "server-files")
	SteamCmdDir = filepath.Join(BaseDir, "steamcmd")
	BackupsDir = filepath.Join(BaseDir, "backups")
	// Initialize log mapping file path
	LogMappingFile = filepath.Join(BaseDir, ".instance_log_mapping.json")
	dirs := []string{InstancesDir, ServerFilesDir, SteamCmdDir, BackupsDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// LoadInstanceConfig loads the configuration for an instance
func LoadInstanceConfig(instanceName string) (*InstanceConfig, error) {
	configFile := filepath.Join(InstancesDir, instanceName, "instance_config.ini")

	file, err := os.Open(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file for instance %s: %w", instanceName, err)
	}
	defer file.Close()

	config := &InstanceConfig{
		MaxPlayers:      70,
		MapName:         "TheIsland_WP",
		RCONPort:        27020,
		QueryPort:       27015,
		Port:            7777,
		EnableAsaPlugin: false,
		BindDomain:      "",
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "ServerName":
			config.ServerName = value
		case "ServerPassword":
			config.ServerPassword = value
		case "ServerAdminPassword":
			config.ServerAdminPassword = value
		case "MaxPlayers":
			if val, err := strconv.Atoi(value); err == nil {
				config.MaxPlayers = val
			}
		case "MapName":
			config.MapName = value
		case "RCONPort":
			if val, err := strconv.Atoi(value); err == nil {
				config.RCONPort = val
			}
		case "QueryPort":
			if val, err := strconv.Atoi(value); err == nil {
				config.QueryPort = val
			}
		case "Port":
			if val, err := strconv.Atoi(value); err == nil {
				config.Port = val
			}
		case "ModIDs":
			config.ModIDs = value
		case "SaveDir":
			config.SaveDir = value
		case "ClusterID":
			config.ClusterID = value
		case "CustomStartParameters":
			config.CustomStartParameters = value
		case "EnableMods":
			config.EnableAsaPlugin = strings.ToLower(value) == "true"
		case "EnableAsaPlugin":
			config.EnableAsaPlugin = strings.ToLower(value) == "true"
		case "BindDomain":
			config.BindDomain = value
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	return config, nil
}

// SaveInstanceConfig saves the configuration for an instance
func SaveInstanceConfig(instanceName string, config *InstanceConfig) error {
	configDir := filepath.Join(InstancesDir, instanceName)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create instance directory: %w", err)
	}

	configFile := filepath.Join(configDir, "instance_config.ini")

	content := fmt.Sprintf(`[ServerSettings]
ServerName=%s
ServerPassword=%s
ServerAdminPassword=%s
MaxPlayers=%d
MapName=%s
RCONPort=%d
QueryPort=%d
Port=%d
ModIDs=%s
CustomStartParameters=%s
SaveDir=%s
ClusterID=%s
EnableAsaPlugin=%v
BindDomain=%s
`,
		config.ServerName,
		config.ServerPassword,
		config.ServerAdminPassword,
		config.MaxPlayers,
		config.MapName,
		config.RCONPort,
		config.QueryPort,
		config.Port,
		config.ModIDs,
		config.CustomStartParameters,
		config.SaveDir,
		config.ClusterID,
		config.EnableAsaPlugin,
		config.BindDomain,
	)

	if err := os.WriteFile(configFile, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// UpdateInstanceConfig updates the configuration for an instance with partial updates
func UpdateInstanceConfig(instanceName string, updates map[string]interface{}) error {
	// Load current config
	currentConfig, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return fmt.Errorf("failed to load current config: %w", err)
	}

	// Apply updates
	if val, ok := updates["ServerName"]; ok {
		if str, ok := val.(string); ok {
			currentConfig.ServerName = str
		}
	}
	if val, ok := updates["ServerPassword"]; ok {
		if str, ok := val.(string); ok {
			currentConfig.ServerPassword = str
		}
	}
	if val, ok := updates["ServerAdminPassword"]; ok {
		if str, ok := val.(string); ok {
			currentConfig.ServerAdminPassword = str
		}
	}
	if val, ok := updates["MaxPlayers"]; ok {
		switch v := val.(type) {
		case float64:
			currentConfig.MaxPlayers = int(v)
		case int:
			currentConfig.MaxPlayers = v
		}
	}
	if val, ok := updates["MapName"]; ok {
		if str, ok := val.(string); ok {
			currentConfig.MapName = str
		}
	}
	if val, ok := updates["RCONPort"]; ok {
		switch v := val.(type) {
		case float64:
			currentConfig.RCONPort = int(v)
		case int:
			currentConfig.RCONPort = v
		}
	}
	if val, ok := updates["QueryPort"]; ok {
		switch v := val.(type) {
		case float64:
			currentConfig.QueryPort = int(v)
		case int:
			currentConfig.QueryPort = v
		}
	}
	if val, ok := updates["Port"]; ok {
		switch v := val.(type) {
		case float64:
			currentConfig.Port = int(v)
		case int:
			currentConfig.Port = v
		}
	}
	if val, ok := updates["ModIDs"]; ok {
		if str, ok := val.(string); ok {
			currentConfig.ModIDs = str
		}
	}
	if val, ok := updates["SaveDir"]; ok {
		if str, ok := val.(string); ok {
			currentConfig.SaveDir = str
		}
	}
	if val, ok := updates["ClusterID"]; ok {
		if str, ok := val.(string); ok {
			currentConfig.ClusterID = str
		}
	}
	if val, ok := updates["CustomStartParameters"]; ok {
		if str, ok := val.(string); ok {
			currentConfig.CustomStartParameters = str
		}
	}
	if val, ok := updates["EnableMods"]; ok {
		switch v := val.(type) {
		case bool:
			currentConfig.EnableAsaPlugin = v
		case float64:
			currentConfig.EnableAsaPlugin = v != 0
		}
	}
	if val, ok := updates["EnableAsaPlugin"]; ok {
		switch v := val.(type) {
		case bool:
			currentConfig.EnableAsaPlugin = v
		case float64:
			currentConfig.EnableAsaPlugin = v != 0
		}
	}

	// Add BindDomain update
	if val, ok := updates["BindDomain"]; ok {
		if str, ok := val.(string); ok {
			currentConfig.BindDomain = str
		}
	}

	// Save updated config
	return SaveInstanceConfig(instanceName, currentConfig)
}

// LogMapping represents the mapping of instance names to log file paths
type LogMapping struct {
	Mappings map[string]string `json:"mappings"`
}

// LoadLogMappingFromFile loads the instance to log file mapping from persistent storage
func LoadLogMappingFromFile() (map[string]string, error) {
	mappings := make(map[string]string)

	// If file doesn't exist, create an empty mapping file
	if _, err := os.Stat(LogMappingFile); os.IsNotExist(err) {
		if err := SaveLogMappingToFile(mappings); err != nil {
			return nil, err
		}
		return mappings, nil
	}

	data, err := os.ReadFile(LogMappingFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read log mapping file: %w", err)
	}

	var logMapping LogMapping
	if err := json.Unmarshal(data, &logMapping); err != nil {
		return nil, fmt.Errorf("failed to parse log mapping file: %w", err)
	}

	return logMapping.Mappings, nil
}

// SaveLogMappingToFile persists the instance to log file mapping to storage
func SaveLogMappingToFile(mappings map[string]string) error {
	logMapping := LogMapping{
		Mappings: mappings,
	}

	data, err := json.MarshalIndent(logMapping, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal log mapping: %w", err)
	}

	if err := os.WriteFile(LogMappingFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write log mapping file: %w", err)
	}

	return nil
}

// GetAvailableInstances returns a list of all available instances
func GetAvailableInstances() ([]string, error) {
	entries, err := os.ReadDir(InstancesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read instances directory: %w", err)
	}

	// 收集实例目录及其创建时间
	type instanceInfo struct {
		name       string
		createTime time.Time
	}
	var instanceList []instanceInfo

	for _, entry := range entries {
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			instanceList = append(instanceList, instanceInfo{
				name:       entry.Name(),
				createTime: info.ModTime(), // 使用ModTime作为创建时间的近似值
			})
		}
	}

	// 按创建时间升序排序
	sort.Slice(instanceList, func(i, j int) bool {
		return instanceList[i].createTime.Before(instanceList[j].createTime)
	})

	// 提取实例名称
	var instances []string
	for _, inst := range instanceList {
		instances = append(instances, inst.name)
	}

	return instances, nil
}

// CreateDefaultInstanceConfig creates a default instance configuration
func CreateDefaultInstanceConfig(instanceName string) *InstanceConfig {
	return &InstanceConfig{
		ServerName:            fmt.Sprintf("ARK Server %s", instanceName),
		ServerPassword:        "",
		ServerAdminPassword:   "adminpassword",
		MaxPlayers:            70,
		MapName:               "TheIsland_WP",
		RCONPort:              27020,
		QueryPort:             27015,
		Port:                  7777,
		ModIDs:                "",
		SaveDir:               instanceName,
		ClusterID:             "",
		CustomStartParameters: "-NoBattlEye -crossplay -NoHangDetection",
		EnableAsaPlugin:       true,
		BindDomain:            "",
	}
}

// GetGameIniContent reads and returns the content of Game.ini for an instance
func GetGameIniContent(instanceName string) (string, error) {
	gameIniPath := filepath.Join(InstancesDir, instanceName, "Config", "Game.ini")

	if _, err := os.Stat(gameIniPath); os.IsNotExist(err) {
		return "", fmt.Errorf("Game.ini not found for instance '%s'", instanceName)
	}

	content, err := os.ReadFile(gameIniPath)
	if err != nil {
		return "", fmt.Errorf("failed to read Game.ini: %w", err)
	}

	return string(content), nil
}

// GetGameUserSettingsContent reads and returns the content of GameUserSettings.ini for an instance
func GetGameUserSettingsContent(instanceName string) (string, error) {
	gameUserSettingsPath := filepath.Join(InstancesDir, instanceName, "Config", "GameUserSettings.ini")

	if _, err := os.Stat(gameUserSettingsPath); os.IsNotExist(err) {
		return "", fmt.Errorf("GameUserSettings.ini not found for instance '%s'", instanceName)
	}

	content, err := os.ReadFile(gameUserSettingsPath)
	if err != nil {
		return "", fmt.Errorf("failed to read GameUserSettings.ini: %w", err)
	}

	return string(content), nil
}

// SaveGameIniContent writes content to the Game.ini file for an instance
func SaveGameIniContent(instanceName string, content string) error {
	gameIniPath := filepath.Join(InstancesDir, instanceName, "Config", "Game.ini")

	// Create the directory if it doesn't exist
	configDir := filepath.Dir(gameIniPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write the content to the file
	if err := os.WriteFile(gameIniPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write Game.ini: %w", err)
	}

	return nil
}

// SaveGameUserSettingsContent writes content to the GameUserSettings.ini file for an instance
func SaveGameUserSettingsContent(instanceName string, content string) error {
	gameUserSettingsPath := filepath.Join(InstancesDir, instanceName, "Config", "GameUserSettings.ini")

	// Create the directory if it doesn't exist
	configDir := filepath.Dir(gameUserSettingsPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write the content to the file
	if err := os.WriteFile(gameUserSettingsPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write GameUserSettings.ini: %w", err)
	}

	return nil
}

// GetServerGameIniContent reads and returns the content of Game.ini from the base server directory
func GetServerGameIniContent() (string, error) {
	gameIniPath := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer/Game.ini")

	if _, err := os.Stat(gameIniPath); os.IsNotExist(err) {
		return "", fmt.Errorf("Game.ini not found in server base directory")
	}

	content, err := os.ReadFile(gameIniPath)
	if err != nil {
		return "", fmt.Errorf("failed to read Game.ini: %w", err)
	}

	return string(content), nil
}

// GetServerGameUserSettingsContent reads and returns the content of GameUserSettings.ini from the base server directory
func GetServerGameUserSettingsContent() (string, error) {
	gameUserSettingsPath := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer/GameUserSettings.ini")

	if _, err := os.Stat(gameUserSettingsPath); os.IsNotExist(err) {
		return "", fmt.Errorf("GameUserSettings.ini not found in server base directory")
	}

	content, err := os.ReadFile(gameUserSettingsPath)
	if err != nil {
		return "", fmt.Errorf("failed to read GameUserSettings.ini: %w", err)
	}

	return string(content), nil
}

// CheckForDuplicatePorts checks if there are duplicate ports in instance configurations
func CheckForDuplicatePorts() error {
	instances, err := GetAvailableInstances()
	if err != nil {
		return err
	}

	portMap := make(map[int]string)
	rconMap := make(map[int]string)
	queryMap := make(map[int]string)

	for _, instanceName := range instances {
		config, err := LoadInstanceConfig(instanceName)
		if err != nil {
			continue
		}

		if config.Port > 0 {
			if existing, exists := portMap[config.Port]; exists {
				return fmt.Errorf("port conflict: game port %d is used by both '%s' and '%s'", config.Port, existing, instanceName)
			}
			portMap[config.Port] = instanceName
		}

		if config.RCONPort > 0 {
			if existing, exists := rconMap[config.RCONPort]; exists {
				return fmt.Errorf("port conflict: RCON port %d is used by both '%s' and '%s'", config.RCONPort, existing, instanceName)
			}
			rconMap[config.RCONPort] = instanceName
		}

		if config.QueryPort > 0 {
			if existing, exists := queryMap[config.QueryPort]; exists {
				return fmt.Errorf("port conflict: query port %d is used by both '%s' and '%s'", config.QueryPort, existing, instanceName)
			}
			queryMap[config.QueryPort] = instanceName
		}
	}

	return nil
}

// SyncInstanceConfigFromSource syncs instance configuration and Config folder from a source instance
// It copies:
// 1. The entire Config folder (all files)
// 2. Specific fields from instance_config.ini: ModIDs, ServerPassword, ServerAdminPassword, BindDomain
// 3. CustomStartParameters (if syncCustomStartParameters is true)
// 4. EnableAsaPlugin (if syncEnableAsaPlugin is true)
// The target instance keeps its other configuration fields (ServerName, Port, RCONPort, QueryPort, MaxPlayers, MapName, SaveDir, ClusterID)
func SyncInstanceConfigFromSource(sourceInstanceName, targetInstanceName string, syncCustomStartParameters bool, syncEnableAsaPlugin bool) error {
	// Load source instance configuration
	sourceConfig, err := LoadInstanceConfig(sourceInstanceName)
	if err != nil {
		return fmt.Errorf("failed to load source instance config: %w", err)
	}

	// Load target instance configuration
	targetConfig, err := LoadInstanceConfig(targetInstanceName)
	if err != nil {
		return fmt.Errorf("failed to load target instance config: %w", err)
	}

	// Sync specific fields from source to target
	// Keep target's ServerName, Port, RCONPort, QueryPort, MaxPlayers, MapName, SaveDir, ClusterID
	targetConfig.ModIDs = sourceConfig.ModIDs
	targetConfig.ServerPassword = sourceConfig.ServerPassword
	targetConfig.ServerAdminPassword = sourceConfig.ServerAdminPassword
	targetConfig.BindDomain = sourceConfig.BindDomain

	// Conditionally sync CustomStartParameters
	if syncCustomStartParameters {
		targetConfig.CustomStartParameters = sourceConfig.CustomStartParameters
	}

	// Conditionally sync EnableAsaPlugin
	if syncEnableAsaPlugin {
		targetConfig.EnableAsaPlugin = sourceConfig.EnableAsaPlugin
	}

	// Save updated target configuration
	if err := SaveInstanceConfig(targetInstanceName, targetConfig); err != nil {
		return fmt.Errorf("failed to save target instance config: %w", err)
	}

	// Copy entire Config folder from source to target
	sourceConfigDir := filepath.Join(InstancesDir, sourceInstanceName, "Config")
	targetConfigDir := filepath.Join(InstancesDir, targetInstanceName, "Config")

	// Check if source Config directory exists
	if _, err := os.Stat(sourceConfigDir); os.IsNotExist(err) {
		return fmt.Errorf("source instance Config directory not found: %s", sourceConfigDir)
	}

	// Create target Config directory if it doesn't exist
	if err := os.MkdirAll(targetConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create target Config directory: %w", err)
	}

	// Copy all files from source Config to target Config
	entries, err := os.ReadDir(sourceConfigDir)
	if err != nil {
		return fmt.Errorf("failed to read source Config directory: %w", err)
	}

	for _, entry := range entries {
		srcPath := filepath.Join(sourceConfigDir, entry.Name())
		dstPath := filepath.Join(targetConfigDir, entry.Name())

		if entry.IsDir() {
			// Recursively copy directories
			if err := CopyDir(srcPath, dstPath); err != nil {
				return fmt.Errorf("failed to copy directory %s: %w", entry.Name(), err)
			}
		} else {
			// Copy file
			srcFile, err := os.Open(srcPath)
			if err != nil {
				return fmt.Errorf("failed to open source file %s: %w", entry.Name(), err)
			}

			dstFile, err := os.Create(dstPath)
			if err != nil {
				srcFile.Close()
				return fmt.Errorf("failed to create target file %s: %w", entry.Name(), err)
			}

			if _, err := io.Copy(dstFile, srcFile); err != nil {
				srcFile.Close()
				dstFile.Close()
				return fmt.Errorf("failed to copy file %s: %w", entry.Name(), err)
			}

			srcFile.Close()
			dstFile.Close()
		}
	}

	return nil
}

// SyncInstanceConfigToMultiple syncs instance configuration and Config folder from a source instance to multiple target instances
// with optional flags to control which fields are synced
// It syncs:
// 1. The entire Config folder (all files)
// 2. Specific fields from instance_config.ini: ModIDs, ServerPassword, ServerAdminPassword, BindDomain
// 3. CustomStartParameters (if syncCustomStartParameters is true)
// 4. EnableAsaPlugin (if syncEnableAsaPlugin is true)
// The target instances keep their other configuration fields (ServerName, Port, RCONPort, QueryPort, MaxPlayers, MapName, SaveDir, ClusterID)
// Returns a map of target instance names to their sync results (error or nil if successful)
func SyncInstanceConfigToMultiple(sourceInstanceName string, targetInstanceNames []string, syncCustomStartParameters *bool, syncEnableAsaPlugin *bool) map[string]error {
	results := make(map[string]error)

	// Validate that we have target instances
	if len(targetInstanceNames) == 0 {
		return results
	}

	// Default values for optional sync flags (sync by default if not specified)
	syncStartParams := true
	syncPlugin := true
	if syncCustomStartParameters != nil {
		syncStartParams = *syncCustomStartParameters
	}
	if syncEnableAsaPlugin != nil {
		syncPlugin = *syncEnableAsaPlugin
	}

	// Sync to each target instance
	for _, targetName := range targetInstanceNames {
		results[targetName] = SyncInstanceConfigFromSource(sourceInstanceName, targetName, syncStartParams, syncPlugin)
	}

	return results
}
