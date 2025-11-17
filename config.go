package main

import (
	"bufio"
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
	ConfigFile = filepath.Join(BaseDir, ".ark_server_manager_config")

	// Create necessary directories
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
