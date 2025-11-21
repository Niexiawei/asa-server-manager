package asaserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
}

// EnsureDirectories Initialize directories based on executable location
func EnsureDirectories() error {
	// Get the directory where the executable is located
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	BaseDir = filepath.Dir(exe)
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

	// If file doesn't exist, return empty mappings
	if _, err := os.Stat(LogMappingFile); os.IsNotExist(err) {
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

	var instances []string
	for _, entry := range entries {
		if entry.IsDir() {
			instances = append(instances, entry.Name())
		}
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
