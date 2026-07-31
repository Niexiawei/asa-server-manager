package config

import (
	"asa-server/internal/pkg/fsutil"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jinzhu/copier"
)

var (
	BaseDir        string
	InstancesDir   string
	ServerFilesDir string
	SteamCmdDir    string
	BackupsDir     string
	ConfigFile     string
)

const (
	SteamCmdURL = "https://steamcdn-a.akamaihd.net/client/installer/steamcmd.zip"
)

// InstanceConfig represents an instance configuration
type InstanceConfig struct {
	ServerName              string
	ServerPassword          string
	ServerAdminPassword     string
	MaxPlayers              int
	MapName                 string
	RCONPort                int
	Port                    int
	ModIDs                  string
	SaveDir                 string
	ClusterID               string
	CustomStartParameters   string
	EnableAsaPlugin         bool
	BindDomain              string
	MessageOfTheDay         string
	MessageOfTheDayDuration int
}

type UpdateInstanceConfigRequest struct {
	ServerName              string `json:"ServerName,omitempty"`
	ServerPassword          string `json:"ServerPassword,omitempty"`
	ServerAdminPassword     string `json:"ServerAdminPassword,omitempty"`
	MaxPlayers              *int   `json:"MaxPlayers,omitempty"`
	MapName                 string `json:"MapName,omitempty"`
	RCONPort                *int   `json:"RCONPort,omitempty"`
	Port                    *int   `json:"Port,omitempty"`
	ModIDs                  string `json:"ModIDs,omitempty"`
	SaveDir                 string `json:"SaveDir,omitempty"`
	ClusterID               string `json:"ClusterID,omitempty"`
	CustomStartParameters   string `json:"CustomStartParameters,omitempty"`
	EnableAsaPlugin         *bool  `json:"EnableAsaPlugin,omitempty"`
	BindDomain              string `json:"BindDomain,omitempty"`
	MessageOfTheDay         string `json:"MessageOfTheDay,omitempty"`
	MessageOfTheDayDuration *int   `json:"MessageOfTheDayDuration,omitempty"`
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
		case "MessageOfTheDay":
			config.MessageOfTheDay = value
		case "MessageOfTheDayDuration":
			if val, err := strconv.Atoi(value); err == nil {
				config.MessageOfTheDayDuration = val
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	if err := SetMessageOfTheDay(instanceName, config.MessageOfTheDay, config.MessageOfTheDayDuration); err != nil {
		return nil, fmt.Errorf("error setting message of the day: %w", err)
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
Port=%d
ModIDs=%s
CustomStartParameters=%s
SaveDir=%s
ClusterID=%s
EnableAsaPlugin=%v
BindDomain=%s
MessageOfTheDayDuration=%d
MessageOfTheDay=%s
`,
		config.ServerName,
		config.ServerPassword,
		config.ServerAdminPassword,
		config.MaxPlayers,
		config.MapName,
		config.RCONPort,
		config.Port,
		config.ModIDs,
		config.CustomStartParameters,
		config.SaveDir,
		config.ClusterID,
		config.EnableAsaPlugin,
		config.BindDomain,
		config.MessageOfTheDayDuration,
		config.MessageOfTheDay,
	)

	if err := os.WriteFile(configFile, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// UpdateInstanceConfig updates the configuration for an instance with partial updates
func UpdateInstanceConfig(instanceName string, req UpdateInstanceConfigRequest) error {
	// Load current config
	currentConfig, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return fmt.Errorf("failed to load current config: %w", err)
	}

	stringUpdates := struct {
		ServerName            string
		ServerAdminPassword   string
		MapName               string
		SaveDir               string
		ClusterID             string
		CustomStartParameters string
		BindDomain            string
		MessageOfTheDay       string
	}{}

	if err := copier.CopyWithOption(&stringUpdates, &req, copier.Option{IgnoreEmpty: true}); err != nil {
		return fmt.Errorf("failed to map config updates: %w", err)
	}
	if err := copier.CopyWithOption(currentConfig, &stringUpdates, copier.Option{IgnoreEmpty: true}); err != nil {
		return fmt.Errorf("failed to apply config updates: %w", err)
	}

	currentConfig.ServerPassword = req.ServerPassword
	currentConfig.ModIDs = req.ModIDs

	if req.MaxPlayers != nil {
		currentConfig.MaxPlayers = *req.MaxPlayers
	}
	if req.RCONPort != nil {
		currentConfig.RCONPort = *req.RCONPort
	}
	if req.Port != nil {
		currentConfig.Port = *req.Port
	}
	if req.EnableAsaPlugin != nil {
		currentConfig.EnableAsaPlugin = *req.EnableAsaPlugin
	}
	if req.MessageOfTheDayDuration != nil {
		currentConfig.MessageOfTheDayDuration = *req.MessageOfTheDayDuration
	}

	// Save updated config
	return SaveInstanceConfig(instanceName, currentConfig)
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
	}

	return nil
}

type SyncInstanceConfigOptions struct {
	SyncCustomStartParameters   bool
	SyncEnableAsaPlugin         bool
	OnlySyncServerGameINIConfig bool
}

func newSyncInstanceConfigOptions() *SyncInstanceConfigOptions {
	return &SyncInstanceConfigOptions{
		SyncCustomStartParameters:   false,
		SyncEnableAsaPlugin:         false,
		OnlySyncServerGameINIConfig: false,
	}
}

type SetSyncInstanceConfig func(*SyncInstanceConfigOptions)

func WithSyncCustomStartParameters(sync bool) SetSyncInstanceConfig {
	return func(o *SyncInstanceConfigOptions) {
		o.SyncCustomStartParameters = sync
	}
}

func WithSyncEnableAsaPlugin(sync bool) SetSyncInstanceConfig {
	return func(o *SyncInstanceConfigOptions) {
		o.SyncEnableAsaPlugin = sync
	}
}

func WithOnlySyncServerGameINIConfig(sync bool) SetSyncInstanceConfig {
	return func(o *SyncInstanceConfigOptions) {
		o.OnlySyncServerGameINIConfig = sync
	}
}

// SyncInstanceConfigFromSource syncs instance configuration and Config folder from a source instance
// It copies:
// 1. The entire Config folder (all files)
// 2. Specific fields from instance_config.ini: ModIDs, ServerPassword, ServerAdminPassword, BindDomain
// 3. CustomStartParameters (if syncCustomStartParameters is true)
// 4. EnableAsaPlugin (if syncEnableAsaPlugin is true)
// The target instance keeps its other configuration fields (ServerName, Port, RCONPort, MaxPlayers, MapName, SaveDir, ClusterID)
func SyncInstanceConfigFromSource(sourceInstanceName, targetInstanceName string, options ...SetSyncInstanceConfig) error {
	opts := newSyncInstanceConfigOptions()
	for _, option := range options {
		option(opts)
	}
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
	// Keep target's ServerName, Port, RCONPort, MaxPlayers, MapName, SaveDir, ClusterID
	targetConfig.ModIDs = sourceConfig.ModIDs
	targetConfig.ServerPassword = sourceConfig.ServerPassword
	targetConfig.ServerAdminPassword = sourceConfig.ServerAdminPassword
	targetConfig.BindDomain = sourceConfig.BindDomain

	if !opts.OnlySyncServerGameINIConfig {
		// Conditionally sync CustomStartParameters
		if opts.SyncCustomStartParameters {
			targetConfig.CustomStartParameters = sourceConfig.CustomStartParameters
		}

		// Conditionally sync EnableAsaPlugin
		if opts.SyncEnableAsaPlugin {
			targetConfig.EnableAsaPlugin = sourceConfig.EnableAsaPlugin
		}
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
			if err := fsutil.CopyDir(srcPath, dstPath); err != nil {
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
// The target instances keep their other configuration fields (ServerName, Port, RCONPort, MaxPlayers, MapName, SaveDir, ClusterID)
// Returns a map of target instance names to their sync results (error or nil if successful)
func SyncInstanceConfigToMultiple(sourceInstanceName string, targetInstanceNames []string, options ...SetSyncInstanceConfig) map[string]error {
	results := make(map[string]error)

	// Validate that we have target instances
	if len(targetInstanceNames) == 0 {
		return results
	}
	// Sync to each target instance
	for _, targetName := range targetInstanceNames {
		results[targetName] = SyncInstanceConfigFromSource(sourceInstanceName, targetName, options...)
	}

	return results
}

// SetMessageOfTheDay updates the MessageOfTheDay section in GameUserSettings.ini
func SetMessageOfTheDay(instanceName string, message string, duration int) error {
	gameUserSettingsPath := filepath.Join(InstancesDir, instanceName, "Config", "GameUserSettings.ini")

	if _, err := os.Stat(gameUserSettingsPath); os.IsNotExist(err) {
		return fmt.Errorf("GameUserSettings.ini not found for instance '%s'", instanceName)
	}

	contentBytes, err := os.ReadFile(gameUserSettingsPath)
	if err != nil {
		return fmt.Errorf("failed to read GameUserSettings.ini: %w", err)
	}

	lines := strings.Split(string(contentBytes), "\n")
	var outputLines []string
	inTargetSection := false
	sectionFound := false

	for _, line := range lines {
		trimLine := strings.TrimSpace(line)
		// Keep the line (remove \r if present to normalize)
		cleanLine := strings.TrimRight(line, "\r")

		// Detect section start
		if strings.HasPrefix(trimLine, "[") && strings.HasSuffix(trimLine, "]") {
			if trimLine == "[MessageOfTheDay]" {
				inTargetSection = true
				sectionFound = true
				outputLines = append(outputLines, cleanLine)
				outputLines = append(outputLines, fmt.Sprintf("Message=%s", message))
				outputLines = append(outputLines, fmt.Sprintf("Duration=%d", duration))
				continue
			} else {
				// If we were in the target section and encounter a new section,
				// ensure there is a blank line before the new section starts.
				if inTargetSection {
					if len(outputLines) > 0 && outputLines[len(outputLines)-1] != "" {
						outputLines = append(outputLines, "")
					}
				}
				inTargetSection = false
			}
		}

		if inTargetSection {
			// Skip existing keys we want to override
			parts := strings.SplitN(trimLine, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				if key == "Message" || key == "Duration" {
					continue
				}
			}
			// Also skip empty lines immediately inside the section to avoid gaps
			if trimLine == "" {
				continue
			}
		}

		outputLines = append(outputLines, cleanLine)
	}

	if !sectionFound {
		if len(outputLines) > 0 && outputLines[len(outputLines)-1] != "" {
			outputLines = append(outputLines, "")
		}
		outputLines = append(outputLines, "[MessageOfTheDay]")
		outputLines = append(outputLines, fmt.Sprintf("Message=%s", message))
		outputLines = append(outputLines, fmt.Sprintf("Duration=%d", duration))
	}

	newContent := strings.Join(outputLines, "\r\n")

	return os.WriteFile(gameUserSettingsPath, []byte(newContent), 0644)
}
