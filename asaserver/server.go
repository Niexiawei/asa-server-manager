package asaserver

import (
	"asa-server/common"
	"asa-server/logger"
	"asa-server/win32api"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aymanbagabas/go-pty"
	"github.com/fsnotify/fsnotify"
	"github.com/gorcon/rcon"
)

// logMappingMutex protects the instance to log file mapping
var logMappingMutex sync.RWMutex
var serverActionsLock sync.Locker

// instanceLogMapping stores the mapping of instance names to their log file paths
var instanceLogMapping = make(map[string]string)

// LogWriter is a custom writer that forwards output to logger
type LogWriter struct {
	loggerFn func(string)
}

// Write implements the io.Writer interface
func (lw *LogWriter) Write(p []byte) (n int, err error) {
	if lw.loggerFn != nil {
		lw.loggerFn(string(p))
	}
	return len(p), nil
}

// removeANSIEscapes removes ANSI escape sequences from a string
func removeANSIEscapes(s string) string {
	// This regex pattern matches ANSI escape sequences
	// Including color codes, cursor movement, and other control sequences
	var result strings.Builder
	i := 0
	for i < len(s) {
		// Check if this is the start of an escape sequence
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip the escape sequence
			i += 2
			// Skip until we find a letter or other terminator
			for i < len(s) {
				c := s[i]
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '@' {
					i++
					break
				}
				i++
			}
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return strings.TrimSpace(result.String())
}

// InitializeLogMapping loads log mappings from persistent storage
func InitializeLogMapping() error {
	var (
		backSyncStart = make(chan struct{}, 1)
	)
	defer close(backSyncStart)

	mappings, err := LoadLogMappingFromFile()
	if err != nil {
		return fmt.Errorf("failed to load log mapping from file: %w", err)
	}

	logMappingMutex.Lock()
	instanceLogMapping = mappings
	logMappingMutex.Unlock()

	if len(mappings) > 0 {
		logger.GetLogger().Infof("Loaded %d instance log mappings from persistent storage", len(mappings))
	}

	go func() {
		logger.GetLogger().Info("Starting log mapping file change listener...")
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			panic(fmt.Sprintf("Failed to create file watcher: %v", err))
		}

		defer watcher.Close()
		if err := watcher.Add(LogMappingFile); err != nil {
			panic(fmt.Sprintf("Failed to watch logs directory: %v", err))
		}

		backSyncStart <- struct{}{}

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Name != LogMappingFile {
					continue
				}

				if event.Op&fsnotify.Write != fsnotify.Write {
					continue
				}

				mappings, err := LoadLogMappingFromFile()
				if err != nil {
					logger.GetLogger().Errorf("failed to load log mapping from file: %v", err)
					continue
				}

				logMappingMutex.Lock()
				instanceLogMapping = mappings
				logMappingMutex.Unlock()

				if len(mappings) > 0 {
					logger.GetLogger().Infof("Loaded %d instance log mappings from persistent storage", len(mappings))
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.GetLogger().Errorf("Watcher error: %v", err)
				return
			}
		}

	}()

	<-backSyncStart

	return nil
}

// PersistLogMapping saves the current log mappings to storage
func PersistLogMapping() error {
	logMappingMutex.RLock()
	mappingsCopy := make(map[string]string)
	for k, v := range instanceLogMapping {
		mappingsCopy[k] = v
	}
	logMappingMutex.RUnlock()

	return SaveLogMappingToFile(mappingsCopy)
}

// RemoveInstanceLogMapping removes the log mapping for an instance
func RemoveInstanceLogMapping(instanceName string) error {
	logMappingMutex.Lock()
	delete(instanceLogMapping, instanceName)
	logMappingMutex.Unlock()
	// Persist the updated mappings to file
	return PersistLogMapping()
}

// GetGameLogFileName returns the log file name for a given instance based on running order
// The naming convention is: ShooterGame.log for the first instance, ShooterGame_2.log, ShooterGame_3.log, etc.
// It finds the first available (non-existent) log file number
func GetGameLogFileName(instanceName string) (string, error) {
	logsDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Logs")

	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create logs directory: %w", err)
	}

	logMappingMutex.RLock()
	defer logMappingMutex.RUnlock()

	// Find the first available log file number (starting from 1)
	// First, check if ShooterGame.log (number 1) is available
	logFileName := "ShooterGame.log"
	used := false

	for _, logPath := range instanceLogMapping {
		if filepath.Base(logPath) == logFileName {
			used = true
			break
		}
	}
	if !used {
		return logFileName, nil
	}

	// If ShooterGame.log is used, find the first available numbered file
	// Check ShooterGame_2.log, ShooterGame_3.log, etc.

	for i := 2; i <= 999; i++ {
		logFileName := fmt.Sprintf("ShooterGame_%d.log", i)
		used := false
		for _, logPath := range instanceLogMapping {
			if filepath.Base(logPath) == logFileName {
				used = true
				break
			}
		}

		if !used {
			return logFileName, nil
		}
	}

	// Fallback (should never happen in practice)
	return "", fmt.Errorf("could not find available log file number for instance %s", instanceName)
}

// GetGameLogFilePath returns the full path to the log file for a given instance
func GetGameLogFilePath(instanceName string) (string, error) {
	logsDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Logs")

	// Check if we have a cached mapping
	logMappingMutex.RLock()
	if logPath, exists := instanceLogMapping[instanceName]; exists {
		logMappingMutex.RUnlock()
		return logPath, nil
	}
	logMappingMutex.RUnlock()

	// Get the log file name and create the mapping
	logFileName, err := GetGameLogFileName(instanceName)
	if err != nil {
		return "", err
	}

	logPath := filepath.Join(logsDir, logFileName)

	// Store the mapping
	logMappingMutex.Lock()
	instanceLogMapping[instanceName] = logPath
	logMappingMutex.Unlock()

	return logPath, nil
}

// SetInstanceLogFile manually sets the log file path for an instance
// This is useful when you want to explicitly map an instance to a log file
func SetInstanceLogFile(instanceName, logFilePath string) {
	logMappingMutex.Lock()
	instanceLogMapping[instanceName] = logFilePath
	logMappingMutex.Unlock()
}

// GetInstanceLogFile retrieves the log file path for an instance from the mapping
func GetInstanceLogFile(instanceName string) (string, bool) {
	logMappingMutex.RLock()
	logPath, exists := instanceLogMapping[instanceName]
	logMappingMutex.RUnlock()
	return logPath, exists
}

// IsServerRunning checks if a server instance is running
// It checks by verifying if both the game port and RCON port are listening
// This uniquely identifies the specific server instance
func IsServerRunning(instanceName string) (bool, error) {
	// Load the instance configuration to get the ports
	config, err := LoadInstanceConfig(instanceName)

	if err != nil {
		return false, err
	}

	// Build the netstat command with port filtering for efficiency
	// Check for both game port and RCON port in a single netstat query
	cmd := exec.Command("netstat", "-ano")
	// Hide the cmd window on Windows
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to execute netstat: %w", err)
	}

	netstatOutput := string(output)

	lines := strings.Split(netstatOutput, "\n")

	portStr := fmt.Sprintf(":%d", config.Port)

	for _, line := range lines {
		if strings.Contains(line, portStr) {
			// The last field in the line is the PID
			fields := strings.Fields(line)
			if len(fields) > 2 {
				if !strings.Contains(fields[1], portStr) {
					continue
				}
				pid, err := strconv.Atoi(fields[len(fields)-1])
				if err == nil && pid > 0 {
					return true, nil
				}
			}
		}
	}
	//logger.GetLogger().Warnf("Game port :%d not found", config.Port)
	return false, nil
}

// IsServerRunningByPID checks if a server instance is running by verifying if the saved PID process exists
// This method retrieves the PID from the instance directory and checks if the process is still active
func IsServerRunningByPID(instanceName string) (bool, error) {
	// Retrieve the saved PID for the instance
	pid, err := GetInstancePID(instanceName)
	if err != nil {
		return false, fmt.Errorf("failed to get instance PID: %w", err)
	}

	// Check if the process with this PID exists and is running
	exited, err := win32api.IsProcessExited(uint32(pid))
	if err != nil {
		return false, fmt.Errorf("failed to check process status: %w", err)
	}

	// If process has exited, it's not running
	if exited {
		return false, nil
	}

	return true, nil
}

// CopyDir copies a directory recursively
func CopyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := CopyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			srcFile, err := os.Open(srcPath)
			if err != nil {
				return err
			}
			defer srcFile.Close()

			dstFile, err := os.Create(dstPath)
			if err != nil {
				return err
			}
			defer dstFile.Close()

			if _, err := io.Copy(dstFile, srcFile); err != nil {
				return err
			}
		}
	}

	return nil
}

// setupInstanceConfig sets up the instance configuration directory with proper symlinks or junctions
func setupInstanceConfig(instanceName string, confReset *func()) error {
	instanceConfigDir := filepath.Join(InstancesDir, instanceName, "Config")
	baseConfigDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")
	baseConfigDirBackup := baseConfigDir + ".bak"

	// 1. If instance Config directory doesn't exist, copy from base server config
	if _, err := os.Stat(instanceConfigDir); os.IsNotExist(err) {
		logger.GetLogger().Infof("Copying base server configuration to instance '%s'...", instanceName)
		if err := CopyDir(baseConfigDir, instanceConfigDir); err != nil {
			return fmt.Errorf("failed to copy config directory: %w", err)
		}
	}

	// 2. Backup the original Config directory if not already backed up
	fileInfo, err := os.Lstat(baseConfigDir)
	if err == nil {
		// If it's not a symlink and backup doesn't exist, back it up
		isSymlink := (fileInfo.Mode() & os.ModeSymlink) != 0
		_, backupErr := os.Stat(baseConfigDirBackup)
		if !isSymlink && os.IsNotExist(backupErr) {
			logger.GetLogger().Info("Backing up original configuration directory...")
			if err := os.Rename(baseConfigDir, baseConfigDirBackup); err != nil {
				return fmt.Errorf("failed to backup original config directory: %w", err)
			}
		}
	}

	// 3. Remove the symlink/directory and create a junction to instance config
	if err := os.RemoveAll(baseConfigDir); err != nil {
		return fmt.Errorf("failed to remove base config directory: %w", err)
	}

	// Create junction from base config to instance config (no admin required on Windows)
	absInstanceConfigDir, err := filepath.Abs(instanceConfigDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of instance config: %w", err)
	}

	// Try to create as junction using mklink command (works without admin on Windows for NTFS)
	// This is more reliable than os.Symlink which requires admin privileges
	cmd := exec.Command("cmd", "/c", "mklink", "/J", baseConfigDir, absInstanceConfigDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create directory junction: %w", err)
	}

	if confReset != nil {
		reset := func() {
			// Remove the junction
			if err := os.RemoveAll(baseConfigDir); err != nil {
				logger.GetLogger().Warnf("Warning: Failed to remove junction for instance %s: %v", instanceName, err)
			}

			// Restore the original configuration directory from backup if it exists
			if _, err := os.Stat(baseConfigDirBackup); err == nil {
				if err := os.Rename(baseConfigDirBackup, baseConfigDir); err != nil {
					logger.GetLogger().Warnf("Warning: Failed to restore original config directory for instance %s: %v", instanceName, err)
				} else {
					logger.GetLogger().Infof("Original configuration directory restored for instance: %s", instanceName)
				}
			}
		}
		*confReset = reset
	}

	return nil
}

func removeNotRunningServerLogMapper() error {
	servers, err := GetAvailableInstances()
	if err != nil {
		return err
	}
	for _, s := range servers {
		running, err := IsServerRunning(s)
		if err != nil {
			return err
		}
		if !running {
			if err := RemoveInstanceLogMapping(s); err != nil {
				return err
			}
		}
	}
	return nil
}

type StartServerOptions struct {
	SetGameLogPath               func(path string)
	GameInitializationSuccessful func(logPath string)
	WaitServerCompleted          bool
	ParentCtx                    context.Context
	SetPid                       func(pid int)
}

type StartServerOptionsFunc func(options *StartServerOptions)

func WithSetGameLogPath(callback func(path string)) StartServerOptionsFunc {
	return func(options *StartServerOptions) {
		options.SetGameLogPath = callback
	}
}
func WithGameInitializationSuccessful(callback func(path string)) StartServerOptionsFunc {
	return func(options *StartServerOptions) {
		options.GameInitializationSuccessful = callback
	}
}

func WithWaitServerCompleted() StartServerOptionsFunc {
	return func(options *StartServerOptions) {
		options.WaitServerCompleted = true
	}
}
func WithCtx(ctx context.Context) StartServerOptionsFunc {
	return func(options *StartServerOptions) {
		options.ParentCtx = ctx
	}
}

func WithSetPid(callback func(pid int)) StartServerOptionsFunc {
	return func(options *StartServerOptions) {
		options.SetPid = callback
	}
}

// StartServer starts a server instance
func StartServer(instanceName string, options ...StartServerOptionsFunc) error {
	opts := new(StartServerOptions)
	opts.WaitServerCompleted = false
	opts.ParentCtx = context.Background()
	for _, o := range options {
		o(opts)
	}

	ctx, cancel := context.WithCancel(opts.ParentCtx)
	defer cancel()

	var (
		confReset      func()
		startupSuccess = make(chan bool, 1)
		pid            int
	)

	defer func() {
		close(startupSuccess)
	}()

	if err := removeNotRunningServerLogMapper(); err != nil {
		return err
	}

	// Check for duplicate ports
	if err := CheckForDuplicatePorts(); err != nil {
		logger.GetLogger().Errorf("Port conflicts detected: %v", err)
		return err
	}

	config, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return err
	}

	logger.GetLogger().Infof("Starting server for instance: %s", instanceName)

	// Setup instance configuration directory and symlinks
	if err := setupInstanceConfig(instanceName, &confReset); err != nil {
		return err
	}

	// Ensure per-instance save directory exists
	saveDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/SavedArks", config.SaveDir)

	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return fmt.Errorf("failed to create save directory: %w", err)
	}

	// Build the command
	// Quote parameters that may contain special characters to prevent parsing issues
	mapParam := fmt.Sprintf("%s?listen?SessionName=%s?ServerPassword=%s?RCONEnabled=True?ServerAdminPassword=%s?AltSaveDirectoryName=%s",
		config.MapName,
		quotifyIfNeeded(config.ServerName),
		quotifyIfNeeded(config.ServerPassword),
		quotifyIfNeeded(config.ServerAdminPassword),
		config.SaveDir,
	)

	// Direct execution of ArkAscendedServer.exe on Windows
	arkExe := filepath.Join(ServerFilesDir, "ShooterGame/Binaries/Win64/ArkAscendedServer.exe")

	args := []string{
		mapParam,
	}

	if config.CustomStartParameters != "" {
		args = append(args, strings.Fields(config.CustomStartParameters)...)
	}

	args = append(args,
		fmt.Sprintf("-WinLiveMaxPlayers=%d", config.MaxPlayers),
		fmt.Sprintf("-Port=%d", config.Port),
		fmt.Sprintf("-QueryPort=%d", config.QueryPort),
		fmt.Sprintf("-RCONPort=%d", config.RCONPort),
		"-game",
		"-server",
		"-log",
	)

	// Handle BindDomain resolution and IP parameter injection
	if config.BindDomain != "" {
		// Resolve domain to IPv4 addresses
		if ipv4Addrs, err := common.ResolveDomainToIPv4(config.BindDomain); err == nil && len(ipv4Addrs) > 0 {
			// Use the first resolved IPv4 address
			ipv4Addr := ipv4Addrs[0]

			// Check if -ip or -serverip are already in CustomStartParameters
			customParams := strings.Fields(config.CustomStartParameters)
			ipFound := false
			serverIpFound := false

			// Look for existing -ip and -serverip parameters to replace them
			for i, param := range customParams {
				if strings.HasPrefix(param, "-ip=") {
					customParams[i] = fmt.Sprintf("-ip=%s", ipv4Addr)
					ipFound = true
				} else if param == "-ip" && i+1 < len(customParams) {
					customParams[i+1] = ipv4Addr
					ipFound = true
				} else if strings.HasPrefix(param, "-serverip=") {
					customParams[i] = fmt.Sprintf("-serverip=%s", ipv4Addr)
					serverIpFound = true
				} else if param == "-serverip" && i+1 < len(customParams) {
					customParams[i+1] = ipv4Addr
					serverIpFound = true
				}
			}

			// If we found and replaced parameters in CustomStartParameters, rebuild args
			if ipFound || serverIpFound {
				// Rebuild args with mapParam and modified custom parameters
				args = []string{mapParam}
				args = append(args, customParams...)
			} else {
				// Add the parameters if they weren't found in CustomStartParameters
				args = append(args, fmt.Sprintf("-ip=%s", ipv4Addr))
				args = append(args, fmt.Sprintf("-serverip=%s", ipv4Addr))
			}
		} else {
			logger.GetLogger().Warnf("Failed to resolve domain %s to IPv4 address, ignoring BindDomain parameter", config.BindDomain)
		}
	}

	if config.ModIDs != "" {
		args = append(args, fmt.Sprintf("-mods=%s", config.ModIDs))
	}

	if config.ClusterID != "" {
		clusterDir := filepath.Join(BaseDir, "clusters", config.ClusterID)
		if strings.Contains(config.CustomStartParameters, "-ClusterDirOverride") {
			args = append(args,
				fmt.Sprintf("-ClusterId=%s", config.ClusterID),
			)
		} else {
			args = append(args,
				fmt.Sprintf("-ClusterDirOverride=%s", clusterDir),
				fmt.Sprintf("-ClusterId=%s", config.ClusterID),
			)
		}
	}

	var (
		arkAsaApiRunning bool = false
	)

	if config.EnableAsaPlugin {
		arkApiExe := filepath.Join(ServerFilesDir, "ShooterGame/Binaries/Win64/AsaApiLoader.exe")
		if FileExists(arkApiExe) {
			arkExe = arkApiExe
			arkAsaApiRunning = true
		}
	}

	// Get the game log file path and establish mapping
	gameLogPath, err := GetGameLogFilePath(instanceName)
	if err != nil {
		return fmt.Errorf("failed to get game log file path: %w", err)
	}

	if opts.SetGameLogPath != nil {
		opts.SetGameLogPath(gameLogPath)
	}

	// Create the log file if it doesn't exist
	if _, err := os.Stat(gameLogPath); os.IsNotExist(err) {
		// Create the log file with empty content
		if err := os.WriteFile(gameLogPath, []byte(""), 0644); err != nil {
			return fmt.Errorf("failed to create log file %s: %w", gameLogPath, err)
		}
		logger.GetLogger().Infof("Created log file: %s", gameLogPath)
	}

	if arkAsaApiRunning {
		if logger.GetLogMode() == logger.CLIMode {
			newArgs := []string{"/C", "start", "", arkExe}
			newArgs = append(newArgs, args...)
			c := exec.Command("cmd", newArgs...)
			if err := c.Start(); err != nil {
				return fmt.Errorf("failed to start server: %w", err)
			}
			pid = c.Process.Pid
		} else {
			CleanConsoleOutput := func(r io.Reader, w io.Writer) error {
				// 匹配 ANSI 转义序列以及上面提到的控制符
				// 包括 ESC [ ? ... h/l/m 等序列
				ansiRegexp := regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
				// 去除其它 C0 控制字符（保留换行符 \n）
				ctrlRegexp := regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`)

				scanner := bufio.NewScanner(r)
				for scanner.Scan() {
					line := scanner.Bytes()

					// 去掉 ANSI / 控制字符
					line = ansiRegexp.ReplaceAll(line, []byte{})
					line = ctrlRegexp.ReplaceAll(line, []byte{})

					// 输出，保证每行以换行符结尾
					line = bytes.TrimRight(line, " \t")
					if _, err := w.Write(append(line, '\n')); err != nil {
						return err
					}
				}
				return scanner.Err()
			}
			pp, err := pty.New()
			if err != nil {
				log.Fatalf("failed to open pty: %s", err)
			}

			logWriter := &LogWriter{
				loggerFn: func(msg string) {
					msg = strings.TrimRight(msg, "\n\r")
					if msg != "" {
						logger.GetLogger().Infof("[%s][AsaApiLoader] %s", instanceName, msg)
					}
				},
			}
			c := pp.Command(arkExe, args...)
			if err := c.Start(); err != nil {
				return fmt.Errorf("failed to start server: %w", err)
			}
			pid = c.Process.Pid
			go CleanConsoleOutput(pp, logWriter)
			logger.GetLogger().Infof("[%s] Redirecting AsaApiLoader output to logger", instanceName)
		}
		_pid, err := WaitArkApiRunServer(ctx, config.QueryPort)
		if err != nil {
			return fmt.Errorf("failed to start server: %w", err)
		}
		pid = int(_pid)
	} else {
		cmd := exec.Command(arkExe, args...)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start server: %w", err)
		}
		// Save the PID to the instance directory
		pid = cmd.Process.Pid
	}

	if err := SaveInstancePID(instanceName, pid); err != nil {
		logger.GetLogger().Warnf("Failed to save PID for instance %s: %v", instanceName, err)
	}

	logger.GetLogger().Infof("Server started for instance: %s. It should be fully operational in approximately 60 seconds.", instanceName)
	logger.GetLogger().Infof("Game log file: %s", gameLogPath)
	// Persist the log mapping for future restarts
	if err := PersistLogMapping(); err != nil {
		logger.GetLogger().Warnf("Failed to persist log mapping: %v", err)
	}

	if ctx.Err() != nil {
		killGameServer(pid)
	}

	if opts.SetPid != nil {
		opts.SetPid(pid)
	}

	if opts.GameInitializationSuccessful != nil {
		opts.GameInitializationSuccessful(gameLogPath)
	}

	// Monitor for mod information in a separate goroutine
	go MonitorAndExtractModInfo(ctx, gameLogPath, instanceName)

	if opts.WaitServerCompleted {
		go func() {
			if exited := WaitGamePidExit(ctx, pid); exited {
				cancel()
			}
		}()

		TailLogFileWithLinesContext(ctx, gameLogPath, 0, func(line string) {
			// Check for successful startup message
			if strings.Contains(line, "Server has completed startup and is now advertising for join") {
				startupSuccess <- true
			}
			if strings.Contains(line, "has successfully started!") {
				confReset()
			}
		})

	} else {
		time.Sleep(60 * time.Second)
		confReset()
	}

	if opts.WaitServerCompleted {
		select {
		case <-ctx.Done():
			return fmt.Errorf("start game server exited")
		case <-startupSuccess:
			return nil
		}
	}

	return nil
}

// GetPIDByPort finds the PID of the process listening on a specific port
func GetPIDByPort(port int) (int, error) {
	cmd := exec.Command("netstat", "-ano")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to execute netstat: %w", err)
	}

	netstatOutput := string(output)
	portStr := fmt.Sprintf(":%d", port)

	// Split output into lines and search for the port
	lines := strings.Split(netstatOutput, "\n")
	for _, line := range lines {
		if strings.Contains(line, portStr) {
			// The last field in the line is the PID
			fields := strings.Fields(line)
			if len(fields) > 2 {
				if !strings.Contains(fields[1], portStr) {
					continue
				}
				pid, err := strconv.Atoi(fields[len(fields)-1])
				if err == nil && pid > 0 {
					return pid, nil
				}
			}
		}
	}

	return 0, fmt.Errorf("no process found listening on port %d", port)
}

// StopServer stops a server instance
func StopServer(instanceName string) error {
	var (
		pid int
	)
	running, err := IsServerRunning(instanceName)
	if err != nil || !running {
		logger.GetLogger().Warnf("Server for instance %s is not running.", instanceName)
		return fmt.Errorf("server for instance %s is not running", instanceName)
	}

	logger.GetLogger().Infof("Stopping server for instance: %s", instanceName)
	// Try graceful shutdown with RCON

	config, configErr := LoadInstanceConfig(instanceName)
	if configErr != nil {
		return fmt.Errorf("failed to load instance config: %w", configErr)
	}
	pid, err = GetPIDByPort(config.Port)
	if err != nil {
		return fmt.Errorf("failed to find process PID: %w", err)
	}

	if err := SaveWorldSafely(instanceName); err != nil {
		return fmt.Errorf("failed to save world safely: %w", err)
	}

	response, err := SendRCONCommand(instanceName, "DoExit")

	if err == nil && strings.Contains(response, "Exiting") {
		logger.GetLogger().Infof("Server instance %s reported 'Exiting...'. Awaiting shutdown...", instanceName)
		// Wait for process to finish using win32api.IsProcessExited
	} else {
		if err := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid)).Run(); err != nil {
			logger.GetLogger().Warnf("failed to kill process PID %d: %s", pid, err.Error())
			_ = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
		}
	}

	for {
		exited, err := win32api.IsProcessExited(uint32(pid))
		if err != nil || exited {
			break
		}
		time.Sleep(2 * time.Second)
	}

	logger.GetLogger().Infof("Server for instance %s has exited.", instanceName)

	if err == nil && config.EnableAsaPlugin {
		pid2, err := GetInstancePID(instanceName)
		if err == nil {
			_ = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid2)).Run()
		}
	}

	// Remove the log mapping for this instance (log file will be reused on next start)
	if err := RemoveInstanceLogMapping(instanceName); err != nil {
		logger.GetLogger().Warnf("Failed to remove log mapping for instance %s: %v", instanceName, err)
	}

	return nil
}

func KillServer(instanceName string) error {
	cfg, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return err
	}
	pid, err := GetPIDByPort(cfg.Port)
	if err != nil {
		return err
	}
	return exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
}

// RestartServer restarts a server instance
func RestartServer(instanceName string) error {
	if err := StopServer(instanceName); err != nil {
		return err
	}
	time.Sleep(10 * time.Second)
	return StartServer(instanceName)
}

// SendRCONCommand sends an RCON command to a server using gorcon/rcon library
func SendRCONCommand(instanceName string, command string) (string, error) {
	running, err := IsServerRunning(instanceName)
	if err != nil || !running {
		return "", fmt.Errorf("server for instance %s is not running", instanceName)
	}

	config, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return "", err
	}

	// Validate RCON password is not empty
	if config.ServerAdminPassword == "" {
		return "", fmt.Errorf("RCON password is empty for instance %s. Please set ServerAdminPassword in config", instanceName)
	}

	// Connect to RCON server with retry logic
	rconAddr := fmt.Sprintf("localhost:%d", config.RCONPort)
	logger.GetLogger().Infof("Instance: %s Connecting to RCON server at %s...", instanceName, rconAddr)

	var client *rcon.Conn
	var connectErr error

	// Try to connect with timeout and retry
	for attempt := 1; attempt <= 3; attempt++ {
		client, connectErr = rcon.Dial(rconAddr, config.ServerAdminPassword)
		if connectErr == nil {
			logger.GetLogger().Info("Connected to RCON server")
			break
		}

		logger.GetLogger().Warnf("Attempt %d failed: %v", attempt, connectErr)
		if attempt < 3 {
			logger.GetLogger().Info("   Retrying in 2 seconds...")
			time.Sleep(2 * time.Second)
		}
	}

	if connectErr != nil {
		logger.GetLogger().Errorf("RCON Connection failed (password: '%s')", config.ServerAdminPassword)
		return "", fmt.Errorf("failed to connect to RCON server at %s: %w", rconAddr, connectErr)
	}

	defer client.Close()
	// Send command
	logger.GetLogger().Infof("Sending RCON command '%s' to %s", command, rconAddr)
	response, err := client.Execute(command)
	if err != nil {
		return "", fmt.Errorf("RCON command execution failed: %w", err)
	}

	logger.GetLogger().Infof("RCON response: %s", response)
	return response, nil
}

// StartAllInstances starts all instances with delay between each
func StartAllInstances() error {
	instances, err := GetAvailableInstances()
	if err != nil {
		return err
	}

	fmt.Println("Starting all server instances...")

	for _, instanceName := range instances {
		running, err := IsServerRunning(instanceName)
		if err == nil && running {
			logger.GetLogger().Warnf("Instance %s is already running. Skipping...", instanceName)
			continue
		}

		if err := StartServer(instanceName); err != nil {
			logger.GetLogger().Errorf("Failed to start instance %s: %v", instanceName, err)
			continue
		}

		logger.GetLogger().Info("Waiting 30 seconds before starting the next instance...")
		time.Sleep(30 * time.Second)
	}

	logger.GetLogger().Info("All instances have been processed.")
	return nil
}

// StopAllInstances stops all instances
func StopAllInstances() error {
	instances, err := GetAvailableInstances()
	if err != nil {
		return err
	}

	fmt.Println("Stopping all server instances...")

	for _, instanceName := range instances {
		running, err := IsServerRunning(instanceName)
		if err == nil && !running {
			logger.GetLogger().Warnf("Instance %s is not running. Skipping...", instanceName)
			continue
		}

		if err := StopServer(instanceName); err != nil {
			logger.GetLogger().Errorf("Failed to stop instance %s: %v", instanceName, err)
		}
	}

	logger.GetLogger().Info("All instances have been stopped.")
	return nil
}

// GetRunningInstances returns a list of running instances
func GetRunningInstances() ([]string, error) {
	instances, err := GetAvailableInstances()
	if err != nil {
		return nil, err
	}

	var running []string
	for _, instanceName := range instances {
		if isRunning, err := IsServerRunning(instanceName); err == nil && isRunning {
			running = append(running, instanceName)
		}
	}

	return running, nil
}

// SaveInstancePID saves the PID of a running instance to its directory
func SaveInstancePID(instanceName string, pid int) error {
	instanceDir := filepath.Join(InstancesDir, instanceName)
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		return fmt.Errorf("failed to create instance directory: %w", err)
	}

	pidFile := filepath.Join(instanceDir, "pid")
	return os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644)
}

// GetInstancePID retrieves the PID of a running instance from its directory
func GetInstancePID(instanceName string) (int, error) {
	instanceDir := filepath.Join(InstancesDir, instanceName)
	pidFile := filepath.Join(instanceDir, "pid")

	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID: %w", err)
	}

	return pid, nil
}

// quotifyIfNeeded wraps a string in double quotes if it contains special characters
// that could interfere with command-line parameter parsing
func quotifyIfNeeded(value string) string {
	if value == "" {
		return value
	}
	// Replace spaces with dashes, converting multiple consecutive spaces to a single dash
	value = strings.TrimSpace(value)
	value = regexp.MustCompile(`\s+`).ReplaceAllString(value, "-")
	// Check if the value contains any special characters that need quoting
	// ARK server uses ? as parameter separator, so we need to quote values containing special characters
	return fmt.Sprintf("\"%s\"", value)
}

// SyncGameConfigToInstance reads Game.ini and GameUserSettings.ini from the base server directory
// and merges them with the instance's config files, only adding entries that don't exist in the instance files
func SyncGameConfigToInstance(instanceName string) error {
	baseConfigDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved/Config/WindowsServer")
	instanceConfigDir := filepath.Join(InstancesDir, instanceName, "Config")

	// Ensure instance config directory exists
	if err := os.MkdirAll(instanceConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create instance config directory: %w", err)
	}

	// Process Game.ini
	gameIniSourcePath := filepath.Join(baseConfigDir, "Game.ini")
	gameIniDestPath := filepath.Join(instanceConfigDir, "Game.ini")

	if err := syncConfigFile(gameIniSourcePath, gameIniDestPath); err != nil {
		return fmt.Errorf("failed to sync Game.ini: %w", err)
	}

	// Process GameUserSettings.ini
	gameUserSettingsSourcePath := filepath.Join(baseConfigDir, "GameUserSettings.ini")
	gameUserSettingsDestPath := filepath.Join(instanceConfigDir, "GameUserSettings.ini")

	if err := syncConfigFile(gameUserSettingsSourcePath, gameUserSettingsDestPath); err != nil {
		return fmt.Errorf("failed to sync GameUserSettings.ini: %w", err)
	}

	logger.GetLogger().Infof("Configuration files synced for instance '%s'", instanceName)
	return nil
}

// syncConfigFile synchronizes a single INI config file
// Copies source config file directly to destination using io.Copy
func syncConfigFile(sourcePath, destPath string) error {
	// Check if source file exists
	if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
		return nil // Source doesn't exist, nothing to sync
	}

	// Open source file
	srcFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source config file: %w", err)
	}
	defer srcFile.Close()

	// Create or truncate destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination config file: %w", err)
	}
	defer destFile.Close()

	// Copy file content directly
	if _, err := io.Copy(destFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy config file: %w", err)
	}

	return nil
}
