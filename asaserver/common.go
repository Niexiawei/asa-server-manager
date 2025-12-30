package asaserver

import (
	"asa-server/common"
	"asa-server/logger"
	"asa-server/win32api"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TailLogFileWithLinesContext(ctx context.Context, logPath string, lastNLines int, printFunc func(string)) {
	if ctx.Err() != nil {
		return
	}
	var (
		stop func()
	)
	stop = TailLogFileWithLines(logPath, lastNLines, printFunc)

	go func() {
		select {
		case <-ctx.Done():
			stop()
		}
	}()
}

// TailLogFileWithLines monitors a log file in real-time asynchronously
// Reads and returns the last N lines first, then monitors for new lines
// Returns a stop function to terminate monitoring
// The printFunc closure is called with each log line (historical lines + new lines)
func TailLogFileWithLines(logPath string, lastNLines int, printFunc func(string)) func() {
	stopChan := make(chan struct{})

	go func() {
		// Open file once in read-only mode
		file, err := os.OpenFile(logPath, os.O_RDONLY, 0)
		if err != nil {
			printFunc("Failed to open log file: " + fmt.Sprintf("%v", err))
			return
		}
		defer file.Close()

		// First, read and send the last N lines from the file
		lastLines, err := readLastNLines(logPath, lastNLines)
		if err != nil {
			printFunc("Failed to read last " + fmt.Sprintf("%d", lastNLines) + " lines: " + fmt.Sprintf("%v", err))
			return
		}

		// Send the last N lines
		for _, line := range lastLines {
			select {
			case <-stopChan:
				return
			default:
				if line != "" {
					printFunc(line)
				}
			}
		}

		// Get the current file info and position to start monitoring from
		// We need to get the file size after reading to ensure we start monitoring from the end of the content we just read
		fileInfo, err := file.Stat()
		if err != nil {
			printFunc("Failed to get file info: " + fmt.Sprintf("%v", err))
			return
		}

		lastPosition := fileInfo.Size()
		// Create a watcher for file system events
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			printFunc("Failed to create file watcher: " + fmt.Sprintf("%v", err))
			return
		}
		defer watcher.Close()

		// Watch the logs directory for changes
		//logsDir := filepath.Dir(logPath)
		if err := watcher.Add(logPath); err != nil {
			printFunc("Failed to watch logs directory: " + fmt.Sprintf("%v", err))
			return
		}

		for {
			select {
			case <-stopChan:
				// Stop signal received
				return
			default:
			}

			// Read available content from the log file
			if content, newPos, _, found := readNewLogContent(file, lastPosition); found {
				if content != "" {
					// Call the closure function to print each line
					for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
						if line != "" {
							select {
							case <-stopChan:
								return
							default:
								printFunc(line)
							}
						}
					}
				}
				lastPosition = newPos
			}

			// Wait for file system events or timeout
			select {
			case <-stopChan:
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// React to Write and Create events on the log file
				if event.Name == logPath && (event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create) {
					// File was written to, continue loop to read new content
					continue
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				printFunc("Watcher error: " + fmt.Sprintf("%v", err))
				return
			case <-time.After(100 * time.Millisecond):
				// Periodic check for file updates
				continue
			}
		}
	}()

	// Return the stop function
	return func() {
		close(stopChan)
	}
}

// readNewLogContent reads new content from log file starting at lastPosition
func readNewLogContent(file *os.File, lastPosition int64) (string, int64, int, bool) {
	// Get file info for size checking
	fileInfo, err := file.Stat()
	if err != nil || fileInfo.Size() == 0 {
		return "", lastPosition, 0, false
	}

	// If file was truncated, restart from beginning
	if lastPosition > fileInfo.Size() {
		lastPosition = 0
	}

	// Seek to last position
	if _, err := file.Seek(lastPosition, 0); err != nil {
		return "", lastPosition, 0, false
	}

	// Read new content
	var newLines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		newLines = append(newLines, scanner.Text())
	}

	// Get new position
	newPosition, _ := file.Seek(0, io.SeekCurrent)

	// Join all lines with newlines
	content := strings.Join(newLines, "\n")
	if len(newLines) > 0 {
		content += "\n"
	}

	return content, newPosition, len(newLines), true
}

// FindLatestLogFile finds the latest log file (ShooterGame.log or ShooterGame_N.log)
// When multiple servers run, logs are named ShooterGame.log, ShooterGame_2.log, etc.
func FindLatestLogFile(logsDir string) (string, error) {
	// List all files in the logs directory
	files, err := os.ReadDir(logsDir)
	if err != nil {
		return "", err
	}

	// Find the latest ShooterGame log file
	var logFiles []string
	for _, file := range files {
		if !file.IsDir() && strings.HasPrefix(file.Name(), "ShooterGame") && strings.HasSuffix(file.Name(), ".log") {
			logFiles = append(logFiles, file.Name())
		}
	}

	if len(logFiles) == 0 {
		return "", fmt.Errorf("no ShooterGame log files found")
	}

	// Sort to get the latest (highest numbered) log file
	// ShooterGame.log comes first, then ShooterGame_2.log, ShooterGame_3.log, etc.
	var latestLog string
	for _, log := range logFiles {
		if latestLog == "" || log > latestLog {
			latestLog = log
		}
	}

	return filepath.Join(logsDir, latestLog), nil
}

func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !os.IsNotExist(err)
}

var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// readLastNLines efficiently reads the last N lines from a file by reading from the end
func readLastNLines(filePath string, n int) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Get file info to determine size
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := fileInfo.Size()

	if fileSize == 0 {
		return []string{}, nil
	}

	// If file is smaller than a reasonable chunk size, just read it normally
	const chunkSize = 4096
	lines := []string{}
	offset := fileSize
	for len(lines) < n && offset > 0 {
		// Calculate how much to read
		readSize := int64(chunkSize)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize

		// Seek to the position
		_, err := file.Seek(offset, 0)
		if err != nil {
			return nil, err
		}

		// Read the chunk
		chunk := make([]byte, readSize)
		_, err = file.Read(chunk)
		if err != nil && err != io.EOF {
			return nil, err
		}

		// Count newlines in this chunk and process lines
		chunkStr := string(chunk)
		newLines := strings.Split(chunkStr, "\n")

		// If this isn't the last chunk we're reading, the first element
		// is a continuation of a line from the next chunk
		if len(lines) > 0 && len(newLines) > 0 {
			newLines[0] = newLines[0] + lines[0]
			lines = lines[1:]
		}

		// Add the new lines to our result
		for i := len(newLines) - 1; i >= 0; i-- {
			if i == len(newLines)-1 && len(lines) == 0 && offset+readSize < fileSize {
				// Skip the last line if this isn't the end of the file (it's incomplete)
				continue
			}
			if strings.TrimSpace(newLines[i]) != "" || len(newLines[i]) > 0 {
				lines = append([]string{newLines[i]}, lines...)
				if len(lines) >= n {
					break
				}
			}
		}
	}

	// Return the last N lines (or all lines if less than N)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	return lines, nil
}

func splitOnNewlineOrCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	// 查找 \n 或 \r 的最早位置
	for i, b := range data {
		if b == '\n' || b == '\r' {
			// 返回不包含分隔符的 token（类似 ScanLines）
			return i + 1, dropSep(data[:i], b), nil
		}
	}
	// 如果到 EOF，返回剩余的数据（如果非空）
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	// 需要更多数据
	return 0, nil, nil
}

// 如果希望去掉 CR/LF 之后仍保留 token，这里只是示例（可按需修改）
func dropSep(b []byte, sep byte) []byte {
	// 直接返回 b（不包含 sep），如果想保留 sep 则改这里
	return b
}

func WaitGamePidExit(ctx context.Context, pid int) bool {
	for {
		select {
		case <-ctx.Done():
			return false
			// Startup completed successfully via log detection
		case <-time.After(2 * time.Second):
			// Check if process is still running before timing out
			if exited, _ := win32api.IsProcessExited(uint32(pid)); exited {
				// Process is still running, consider it a success
				return true
			}
		}
	}
}

func killGameServer(pid int) {
	if err := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid)).Run(); err != nil {
		logger.GetLogger().Warnf("failed to kill process PID %d: %s", pid, err.Error())
		_ = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid)).Run()
	}
}
func WaitArkApiRunServer(ctx context.Context, port int) (uint32, error) {
	var (
		processErr = make(chan error, 1)
		processPid = make(chan uint32, 1)
	)

	defer close(processPid)
	defer close(processErr)

	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			process, err := common.QueryProcess("ArkAscendedServer.exe", fmt.Sprintf("Port=%d", port))
			if err != nil {
				processErr <- err
				return
			}
			if len(process) > 0 {
				processPid <- process[0].ProcessId
				return
			}
			<-time.After(200 * time.Millisecond)
		}
	}()
	select {
	case err := <-processErr:
		return 0, err
	case pid := <-processPid:
		return pid, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(10 * time.Second):
		return 0, fmt.Errorf("ARK API loading server error")
	}
}

// ModInfo represents a mod with its ID and name
type ModInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MonitorAndExtractModInfo monitors log file for mod information and saves it to a JSON file
func MonitorAndExtractModInfo(pctx context.Context, logPath string, instanceName string) {
	// Regular expression to match mod info lines
	modRegex := regexp.MustCompile(`^\[.+\]LogCFCore: Mod valid: (.+) \((\d+)\)$`)
	completionMarker := "Initialize Primal Game Data Override."
	ctx, cancel := context.WithCancel(pctx)
	defer cancel()
	// Map to store mod ID -> name mappings
	mods := make(map[string]string)

	// Start monitoring the log file
	TailLogFileWithLinesContext(ctx, logPath, 10, func(line string) {
		// Check if we've reached the completion marker
		if strings.Contains(line, completionMarker) {
			// Load existing mod info if file exists
			modInfoPath := filepath.Join(BaseDir, "mod_info.json")
			existingMods := make(map[string]string)

			if _, err := os.Stat(modInfoPath); err == nil {
				// File exists, load it
				file, err := os.Open(modInfoPath)
				if err == nil {
					defer file.Close()
					decoder := json.NewDecoder(file)
					var existingModList []ModInfo
					if err := decoder.Decode(&existingModList); err == nil {
						for _, mod := range existingModList {
							existingMods[mod.ID] = mod.Name
						}
					}
				}
			}

			// Merge new mods with existing ones
			for id, name := range mods {
				existingMods[id] = name
			}

			// Convert merged map to slice of ModInfo structs
			modList := make([]ModInfo, 0, len(existingMods))
			for id, name := range existingMods {
				modList = append(modList, ModInfo{ID: id, Name: name})
			}

			// Create output directory if it doesn't exist
			outputDir := filepath.Dir(modInfoPath)
			if err := os.MkdirAll(outputDir, 0755); err != nil {
				logger.GetLogger().Warnf("Failed to create output directory for mod info: %v", err)
				cancel()
				return
			}

			// Write to JSON file
			jsonFile, err := os.Create(modInfoPath)
			if err != nil {
				logger.GetLogger().Warnf("Failed to create mod info JSON file: %v", err)
				cancel()
				return
			}
			defer jsonFile.Close()
			// Convert mods to JSON
			encoder := json.NewEncoder(jsonFile)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(modList); err != nil {
				logger.GetLogger().Warnf("Failed to encode mod info JSON: %v", err)
				cancel()
				return
			}
			logger.GetLogger().Infof("Successfully extracted and saved %d mod(s) from instance %s to %s", len(modList), instanceName, modInfoPath)
			cancel()
			return // Stop monitoring after saving
		}

		// Try to match mod info line
		matches := modRegex.FindStringSubmatch(line)
		if len(matches) == 3 {
			modName := strings.TrimSpace(matches[1])
			modID := matches[2]
			if _, exists := mods[modID]; !exists { // Avoid duplicates
				mods[modID] = modName
				logger.GetLogger().Debugf("Found mod in instance %s: %s (%s)", instanceName, modName, modID)
			}
		}
	})

	select {
	case <-ctx.Done():
		return
	}
}

var (
	savePathReplacement = map[string]string{
		"BobsMissions_WP": "BobsMissions",
	}
)

// SaveWorldSafely safely saves the world for an instance
// It sends the "DoExit" command (which triggers world save),
// then checks if the save file has been updated to ensure data persistence
func SaveWorldSafely(instanceName string) error {
	// Get instance configuration to determine save directory and map name
	config, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return fmt.Errorf("failed to load instance config: %w", err)
	}

	// Get the current time to compare against the file's modification time
	startTime := time.Now()
	// Send the DoExit command (this triggers world save)
	response, err := SendRCONCommand(instanceName, "saveworld")
	if err != nil {
		return fmt.Errorf("failed to send command: %w", err)
	}

	// Check if response contains "World Saved" to confirm server is saving
	if err == nil && strings.Contains(response, "World Saved") {
		logger.GetLogger().Infof("Server instance %s is saving world...", instanceName)
	} else {
		logger.GetLogger().Errorf("server instance %s is saving world error: %v", instanceName, err)
		return fmt.Errorf("server instance %s is saving world error: %w", instanceName, err)
	}

	if config.MapName == "BobsMissions_WP" {
		time.Sleep(2 * time.Second)
		return nil
	}

	// Construct the save file path: baseDir + 'server-files\ShooterGame\Saved' + {SaveDir} + {MapName} + {MapName}.ark
	// The actual path should be: BaseDir/server-files/ShooterGame/Saved/SavedArks/{SaveDir}/{MapName}.ark
	// Based on the code in server.go, saveDir is used as: filepath.Join(ServerFilesDir, "ShooterGame/Saved/SavedArks", config.SaveDir)
	saveDirPath := filepath.Join(ServerFilesDir, "ShooterGame/Saved", config.SaveDir, config.MapName)

	args := make([]string, 0, len(savePathReplacement)*2)
	for k, v := range savePathReplacement {
		args = append(args, k, v)
	}
	r := strings.NewReplacer(args...)
	saveDirPath = r.Replace(saveDirPath)
	saveFilePath := filepath.Join(saveDirPath, config.MapName+".ark")

	// Wait for the save file to be updated (check every 1 seconds for up to 300 seconds)
	maxWait := 300 * time.Second
	checkInterval := 1 * time.Second
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), maxWait)
	defer cancel()

	for {
		select {
		case <-ticker.C:
			// Check if the save file exists and has been updated since we sent the command
			fileInfo, err := os.Stat(saveFilePath)
			if err == nil {
				// Check if the file's modification time is greater than or equal to the start time
				// Add a small buffer (1 second) to account for timing precision
				fileModTime := fileInfo.ModTime()
				if fileModTime.After(startTime) || fileModTime.Equal(startTime) {
					diffMilli := fileModTime.UnixMilli() - startTime.UnixMilli()
					logger.GetLogger().Infof("World saved successfully for instance %s. Save file: %s. saved Milliseconds %d",
						instanceName, saveFilePath, diffMilli)
					return nil
				}
			} else {
				logger.GetLogger().Warnf("Save file not found or error checking: %v", err)
			}
			logger.GetLogger().Infof("The world archive has not been uploaded yet: %s", instanceName)
		case <-ctx.Done():
			logger.GetLogger().Errorf("timeout waiting for world save to complete for instance %s. Save file: %s", instanceName, saveFilePath)
			return fmt.Errorf("timeout waiting for world save to complete for instance %s. Save file: %s", instanceName, saveFilePath)
		}
	}
}

type waitServerStartupFunc func(startup bool, err string)

func waitServerStartup(pid int, gameLogPath string, callback waitServerStartupFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	latestLogLine := ""
	var (
		startup = make(chan struct{}, 1)
	)

	go func() {
		if exited := WaitGamePidExit(ctx, pid); exited {
			callback(false, latestLogLine)
			cancel()
		}
	}()

	TailLogFileWithLinesContext(ctx, gameLogPath, 0, func(line string) {
		latestLogLine = line
		// Check for successful startup message
		if strings.Contains(line, "Server has completed startup and is now advertising for join") {
			callback(true, "")
			startup <- struct{}{}
			cancel()
		}
	})
	<-startup
}
