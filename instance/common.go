package instance

import "asa-server/mirror"

import cfgpkg "asa-server/config"

import (
	"asa-server/logger"
	"asa-server/pkg/tail"
	"asa-server/pkg/winproc"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

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

	// H4 fix: Removed defer close(processPid) and defer close(processErr)
	// to prevent send-on-closed-channel panic when the goroutine is still
	// running after the select returns via ctx.Done() or timeout.
	// The buffered channels will be garbage collected.

	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			process, err := winproc.QueryProcess("ArkAscendedServer.exe", fmt.Sprintf("Port=%d", port))
			if err != nil {
				select {
				case processErr <- err:
				default:
				}
				return
			}
			if len(process) > 0 {
				select {
				case processPid <- process[0].ProcessId:
				default:
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}()

	select {
	case err := <-processErr:
		return 0, err
	case pid := <-processPid:
		return pid, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(30 * time.Second):
		return 0, fmt.Errorf("ARK API loading server error: ArkAscendedServer.exe did not appear within 30 seconds")
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
	if err := tail.WithCallback(ctx, logPath, 10, func(line string) {
		// Check if we've reached the completion marker
		if strings.Contains(line, completionMarker) {
			// Load existing mod info if file exists
			modInfoPath := filepath.Join(cfgpkg.BaseDir, "mod_info.json")
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
	}); err != nil {
		logger.GetLogger().Warnf("Failed to tail log for mod extraction (%s): %v", logPath, err)
		return
	}

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
	config, err := cfgpkg.LoadInstanceConfig(instanceName)
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
	if strings.Contains(response, "World Saved") {
		logger.GetLogger().Infof("Server instance %s is saving world...", instanceName)
	} else {
		logger.GetLogger().Errorf("server instance %s is saving world error: %v", instanceName, err)
		return fmt.Errorf("server instance %s is saving world error: %w", instanceName, err)
	}

	if config.MapName == "BobsMissions_WP" {
		return nil
	}

	// v2: 存档路径改为实例本地目录
	saveDirPath := filepath.Join(cfgpkg.InstancesDir, instanceName, "Save", config.MapName)

	// 确保存档目录存在
	if err := os.MkdirAll(saveDirPath, 0755); err != nil {
		return fmt.Errorf("failed to create save directory: %w", err)
	}

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

type waitServerStoppedFunc func(stopComplete bool)

// waitServerStopped monitors the server shutdown process:
// 1. "Closing by request" -> closingCallback (server received shutdown request)
// 2. "Log file closed" + PID exit -> stoppedCallback(true) (server fully stopped)
func waitServerStopped(ctx context.Context, pid int, gameLogPath string,
	closingCallback func(), stoppedCallback waitServerStoppedFunc) {

	ctxLocal, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		closingReceived bool
		logFileClosed   bool
		processExited   bool
		stopped         = make(chan struct{})
		mu              sync.Mutex
		stoppedClosed   bool
	)

	checkComplete := func() {
		mu.Lock()
		defer mu.Unlock()
		if logFileClosed && processExited && !stoppedClosed {
			stoppedClosed = true
			stoppedCallback(true)
			close(stopped)
		}
	}

	// Monitor process exit
	go func() {
		if exited := winproc.WaitProcessExit(ctxLocal, pid, 500*time.Millisecond); exited {
			mu.Lock()
			processExited = true
			mu.Unlock()
			checkComplete()
		}
	}()

	// Monitor log file for shutdown markers
	if err := tail.WithCallback(ctxLocal, gameLogPath, 0, func(line string) {
		mu.Lock()
		if strings.Contains(line, "Closing by request") && !closingReceived {
			closingReceived = true
			mu.Unlock()
			closingCallback()
			return
		}
		if strings.Contains(line, "Log file closed") {
			logFileClosed = true
			mu.Unlock()
			checkComplete()
			return
		}
		mu.Unlock()
	}); err != nil {
		logger.GetLogger().Warnf("Failed to tail log for shutdown monitoring (%s): %v", gameLogPath, err)
	}

	<-stopped
}

func waitServerStartup(pid int, gameLogPath string, callback waitServerStartupFunc, successfullyCallback func()) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var latestLogLineMu sync.Mutex
	var latestLogLine string
	var networkErrLine string
	var (
		startup   = make(chan struct{}) // 无缓冲，使用 close() 通知
		closeOnce sync.Once             // C5 fix: protect close(startup) from double-close
	)

	safeCloseStartup := func() {
		closeOnce.Do(func() { close(startup) })
	}

	go func() {
		if exited := winproc.WaitProcessExit(ctx, pid, 500*time.Millisecond); exited {
			latestLogLineMu.Lock()
			logLine := latestLogLine
			if networkErrLine != "" {
				logLine = networkErrLine
			}
			latestLogLineMu.Unlock()
			callback(false, logLine)
			safeCloseStartup() // C5 fix: process exit path
		}
	}()

	if err := tail.WithCallback(ctx, gameLogPath, 0, func(line string) {
		latestLogLineMu.Lock()
		latestLogLine = line // H6 fix: protected by mutex
		if strings.Contains(line, "ApiError: Failed (serverUnreachable)") {
			networkErrLine = line
		}
		latestLogLineMu.Unlock()
		// Check for successful startup message
		if strings.Contains(line, "Server has completed startup and is now advertising for join") {
			callback(true, "")
			safeCloseStartup() // C5 fix: successful startup path
			cancel()
		}
		if strings.Contains(line, "Initialize Primal Game Data Override") {
			successfullyCallback()
		}
	}); err != nil {
		logger.GetLogger().Warnf("Failed to tail log for startup monitoring (%s): %v", gameLogPath, err)
	}
	<-startup
}

// findServerPIDByPort 通过 WMI 查询 ArkAscendedServer.exe 进程命令行中的端口来查找 PID
// 不依赖端口是否被监听，适用于启动中等过渡状态
func findServerPIDByPort(port int) (int, error) {
	processes, err := winproc.QueryProcess("ArkAscendedServer.exe", fmt.Sprintf("Port=%d", port))
	if err != nil {
		return 0, fmt.Errorf("WMI query failed: %w", err)
	}
	if len(processes) == 0 {
		return 0, fmt.Errorf("no ArkAscendedServer process found with Port=%d", port)
	}
	return int(processes[0].ProcessId), nil
}

// asaVersionTarget "ArkVersion" 的 UTF-16LE 编码 + 结尾 0x0000
var asaVersionTarget = []byte{
	0x41, 0x00, 0x72, 0x00, 0x6B, 0x00, 0x56, 0x00, 0x65, 0x00, 0x72, 0x00, 0x73, 0x00, 0x69,
	0x00, 0x6F, 0x00, 0x6E, 0x00, 0x00, 0x00,
}

// GetAsaVersion 从 ArkAscendedServer.exe 中提取 ASA 版本号
// 通过搜索 UTF-16LE 编码的 "ArkVersion\0" 标记，读取其后的 UTF-16 版本字符串
func GetAsaVersion(exePath string) (string, error) {
	// 只读打开，不影响正在运行的服务器进程
	file, err := os.Open(exePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	buffer := make([]byte, 1024*1024)
	overlap := len(asaVersionTarget) - 1

	// 分块扫描，块间保留 overlap 字节重叠，避免目标串跨块被漏掉
	var fileOffset int64
	var foundOffset int64 = -1
	validLen, err := io.ReadFull(file, buffer)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}

	for {
		if idx := bytes.Index(buffer[:validLen], asaVersionTarget); idx >= 0 {
			foundOffset = fileOffset + int64(idx)
			break
		}

		if validLen < len(buffer) {
			break // EOF
		}

		copy(buffer, buffer[validLen-overlap:validLen])
		fileOffset += int64(validLen - overlap)

		n, err := io.ReadFull(file, buffer[overlap:])
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return "", err
		}
		validLen = overlap + n
	}

	if foundOffset < 0 {
		return "", fmt.Errorf("failed to find ArkVersion string in the executable")
	}

	// 读取标记后紧跟的 UTF-16LE 字符串，直到 0x0000 结束
	if _, err := file.Seek(foundOffset+int64(len(asaVersionTarget)), io.SeekStart); err != nil {
		return "", err
	}

	reader := bufio.NewReader(file)
	var version strings.Builder
	buf := make([]byte, 2)
	for {
		if _, err := io.ReadFull(reader, buf); err != nil {
			break
		}
		unicodeVal := uint16(buf[0]) | uint16(buf[1])<<8
		if unicodeVal == 0 {
			break
		}
		r := rune(unicodeVal)
		if !utf8.ValidRune(r) {
			return "", fmt.Errorf("failed to convert UTF-16 code unit while reading version: %#06X", unicodeVal)
		}
		version.WriteRune(r)
	}
	return version.String(), nil
}

type asaVersionCacheEntry struct {
	modTime time.Time
	size    int64
	version string
}

// asaVersionCache key: exe 路径 -> asaVersionCacheEntry
var asaVersionCache sync.Map

// GetInstanceAsaVersion 获取实例的 ASA 版本号
// 优先读取实例镜像目录的 exe；镜像不存在（实例从未启动）时回退到基础安装目录
// 内置基于 modTime+size 的缓存，服务器更新后自动失效
func GetInstanceAsaVersion(instanceName string) (string, error) {
	arkExe := filepath.Join(mirror.InstanceMirrorDir(instanceName), "ShooterGame/Binaries/Win64/ArkAscendedServer.exe")
	stat, err := os.Stat(arkExe)
	if err != nil {
		arkExe = filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame/Binaries/Win64/ArkAscendedServer.exe")
		stat, err = os.Stat(arkExe)
		if err != nil {
			return "", fmt.Errorf("ArkAscendedServer.exe not found")
		}
	}

	// 缓存命中：modTime 和 size 均未变化
	if v, ok := asaVersionCache.Load(arkExe); ok {
		entry := v.(asaVersionCacheEntry)
		if entry.modTime.Equal(stat.ModTime()) && entry.size == stat.Size() {
			return entry.version, nil
		}
	}

	version, err := GetAsaVersion(arkExe)
	if err != nil {
		return "", err
	}
	asaVersionCache.Store(arkExe, asaVersionCacheEntry{
		modTime: stat.ModTime(),
		size:    stat.Size(),
		version: version,
	})
	return version, nil
}
