package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
}

// Initialize directories based on executable location
func ensureDirectories() error {
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
		MaxPlayers: 70,
		MapName:    "TheIsland_WP",
		RCONPort:   27020,
		QueryPort:  27015,
		Port:       7777,
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
			fmt.Sscanf(value, "%d", &config.MaxPlayers)
		case "MapName":
			config.MapName = value
		case "RCONPort":
			fmt.Sscanf(value, "%d", &config.RCONPort)
		case "QueryPort":
			fmt.Sscanf(value, "%d", &config.QueryPort)
		case "Port":
			fmt.Sscanf(value, "%d", &config.Port)
		case "ModIDs":
			config.ModIDs = value
		case "SaveDir":
			config.SaveDir = value
		case "ClusterID":
			config.ClusterID = value
		case "CustomStartParameters":
			config.CustomStartParameters = value
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
	)

	if err := os.WriteFile(configFile, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
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
	}
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
