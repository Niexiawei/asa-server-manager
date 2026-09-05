package instance

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/mirror"
	procpkg "asa-server/internal/process"
	"asa-server/internal/rconx"
	statepkg "asa-server/internal/state"
	"asa-server/pkg/logger"
	"asa-server/pkg/procx"
	"asa-server/pkg/tail"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// arkExeName 是游戏服务端可执行文件名，进程查找按平台各自的规则匹配它 ——
// 见 gameproc_windows.go / gameproc_linux.go。
const arkExeName = "ArkAscendedServer.exe"

// asaApiLoaderExeName 是 ArkApi 的加载器。启用 ArkApi 时它**取代** arkExeName 被启动，
// 并且——这一点很反直觉——它创建出来的游戏进程的命令行里写的仍然是它自己，
// 而不是 ArkAscendedServer.exe。见 gameproc_linux.go 的说明。
const asaApiLoaderExeName = "AsaApiLoader.exe"

// 停止类操作的预检文案，面向用户，可直接展示。
const (
	ReasonNotStarted  = "实例当前不处于已启动状态"
	ReasonProcessGone = "实例进程已不存在"
)

// IsStoppable 判断实例是否真的可以停止：状态必须是 started，且进程确实还活着。
// 返回的 reason 面向用户，可直接展示。
//
// 为什么两个判据都要看：状态是 BadgerDB 里的记录，服务器崩溃时不会自动更新；
// 只看状态会让已经死掉的实例被拉进倒计时，白等一轮公告发不出去，最后还是停不掉。
//
// **副作用**：实例没有状态记录时会补写一条（见 reconcileMissingState）。
func IsStoppable(instanceName string) (bool, string) {
	state, err := statepkg.GetLatestInstanceState(instanceName)
	if err != nil {
		return reconcileMissingState(instanceName)
	}
	if state.Status != statepkg.StatusStarted {
		return false, ReasonNotStarted
	}
	// 先状态后进程：全停的场景下一次 netstat 都不会跑
	if !procpkg.IsInstanceProcessAlive(instanceName) {
		return reconcileCrashedState(instanceName)
	}
	return true, ""
}

// reconcileCrashedState 处理「状态是 started，但进程已经不在」：多半是异常崩溃。
// 补写一条 stopped 记录，写法与 reconcileMissingState 一致——不补的话这台实例会
// 一直卡在过期的 started，后续所有依赖状态判断的路径（批量操作预检、前端列表、
// 下一次调度）都会继续被这条记录误导，包括「重新启动它」都会被 CAS 拒绝
// （Start 只接受 stopped/failed 系状态，不接受 started）。
func reconcileCrashedState(instanceName string) (bool, string) {
	logger.Warnf(
		"Instance '%s' state says started but its process is gone; reconciling to stopped",
		instanceName,
	)
	if err := statepkg.WriteInstanceState(instanceName, statepkg.StatusStopped,
		"auto-recovered: process exited unexpectedly"); err != nil {
		logger.Errorf("Failed to reconcile crashed state for instance '%s': %v", instanceName, err)
	}
	return false, ReasonProcessGone
}

// reconcileMissingState 处理「读不到状态记录」：改用进程存活作判据，并补写一条记录。
//
// 不能像以前那样直接放行。GetLatestInstanceState 在实例从没写过状态时会返回
// "no state found"（新建的实例、改过名的实例——状态键前缀是 state:{name}:，
// 改名后旧记录就成了孤儿——或者状态库被重置过）。
// 而 CompareAndSwapInstanceState 对这种情况**并不报错**：它把无记录当 stopped，
// 悄悄 return false 把实例 skip 掉。放行的话，倒计时会先白烧一整轮，
// 公告一条都发不出去，等 CAS 收场时人已经等完了。
//
// 补写记录是为了让 CAS、前端列表、下一次预检读到同一个事实：
// 进程活着就补 started（否则倒计时跑完，CAS 仍会因「无记录 = stopped」把它 skip 掉），
// 进程没了就补 stopped。
func reconcileMissingState(instanceName string) (bool, string) {
	alive := procpkg.IsInstanceProcessAlive(instanceName)

	status := statepkg.StatusStopped
	if alive {
		status = statepkg.StatusStarted
	}

	logger.Warnf(
		"Instance '%s' has no state record; reconciling to %s based on the live process",
		instanceName, status,
	)
	if err := statepkg.WriteInstanceState(instanceName, status, ""); err != nil {
		logger.Errorf("Failed to reconcile state for instance '%s': %v", instanceName, err)
	}

	if !alive {
		return false, ReasonProcessGone
	}
	return true, ""
}

func killGameServer(pid int) {
	if err := procx.TerminateTree(pid); err != nil {
		logger.Warnf("failed to kill process PID %d: %s", pid, err.Error())
		_ = procx.KillTree(pid)
	}
}

// 游戏进程出现的等待上限，按是否启用 ArkApi 分两档。
const (
	// gamePIDWaitTimeout 是直接启动 ArkAscendedServer.exe 的情形。实测游戏进程
	// 2~3 秒就会出现，30 秒有充足余量。
	gamePIDWaitTimeout = 30 * time.Second
	// gamePIDWaitTimeoutArkApi 是经 AsaApiLoader.exe 启动的情形。加载器在创建游戏
	// 进程**之前**要先去第三方 CDN 下载与当前 exe 匹配的 offsets cache，真机实测
	// 游戏进程出现在 20 多秒 —— 离 30 秒只差几秒，CDN 慢一点就必然超时。
	// 放宽到 3 分钟不是无脑加大：真正起不来的情形由 launcherExited 提前失败兜住
	// （见 waitForGamePID），不会真的等满。
	//
	// 多数情况下现在已经不走那次下载了：pkg/arkcache 会在启动前把缓存备好，加载器
	// 只做一次 HEAD 比对就直接采用（docs/ARKAPI_CACHE_PREFETCH_PLAN.md）。但这一档
	// **不收紧** —— 预取失败时的降级路径就是让 ArkApi 自己去下，那条路仍然需要
	// 这 3 分钟；而且断网机器上即使缓存有效，ArkApi 也要先耗掉 60~120 秒的 HEAD
	// 重试才肯用它。
	gamePIDWaitTimeoutArkApi = 3 * time.Minute
)

// ErrLauncherExited 表示启动链进程在游戏进程出现之前就结束了。调用方据此把启动器的
// 退出状态补进错误消息 —— 那才是用户真正需要看到的东西。
var ErrLauncherExited = errors.New("启动器进程在游戏进程出现之前就退出了")

// waitForGamePID polls for the real game process and returns its PID, matching
// on AltSaveDirectoryName rather than Port: a numeric port substring can
// collide with -QueryPort=/-RCONPort=, while SaveDir is unique per instance in
// this project by construction (see docs/LINUX_COMPATIBILITY_PLAN.md §5.3).
// This is required whenever Handle.LauncherPID from runner.Run isn't the game
// process's own PID — AsaApiLoader.exe wraps and spawns it as a child on
// either platform, and Linux's umu-run wraps every launch regardless of
// AsaApiLoader. 匹配规则按平台拆分，见 gameproc_*.go。
//
// launcherExited 在启动链进程结束时被关闭。有它才能把「加载器秒退」这种最常见的
// 失败从「干等满超时」变成「立刻报错，并说出退出状态」—— 尤其在 ArkApi 那档超时
// 长达 3 分钟之后，这一条是必需的而不是优化。可以传 nil（永不触发）。
func waitForGamePID(ctx context.Context, saveDir string, timeout time.Duration, launcherExited <-chan struct{}) (uint32, error) {
	marker := fmt.Sprintf("AltSaveDirectoryName=%s", saveDir)

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
			process, err := queryGameProcesses(marker)
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
	case <-launcherExited:
		// 最后再查一次：轮询间隔是 200ms，「游戏进程刚出现、启动器恰好同时退出」
		// 这个窗口真实存在，直接判失败会误杀一次本来成功的启动。
		if process, err := queryGameProcesses(marker); err == nil && len(process) > 0 {
			return process[0].ProcessId, nil
		}
		return 0, ErrLauncherExited
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(timeout):
		return 0, fmt.Errorf("游戏进程在 %s 内没有出现", timeout)
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
				logger.Warnf("Failed to create output directory for mod info: %v", err)
				cancel()
				return
			}

			// Write to JSON file
			jsonFile, err := os.Create(modInfoPath)
			if err != nil {
				logger.Warnf("Failed to create mod info JSON file: %v", err)
				cancel()
				return
			}
			defer jsonFile.Close()
			// Convert mods to JSON
			encoder := json.NewEncoder(jsonFile)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(modList); err != nil {
				logger.Warnf("Failed to encode mod info JSON: %v", err)
				cancel()
				return
			}
			logger.Infof("Successfully extracted and saved %d mod(s) from instance %s to %s", len(modList), instanceName, modInfoPath)
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
				logger.Debugf("Found mod in instance %s: %s (%s)", instanceName, modName, modID)
			}
		}
	}); err != nil {
		logger.Warnf("Failed to tail log for mod extraction (%s): %v", logPath, err)
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
	ctx := context.Background()
	// Get the current time to compare against the file's modification time
	startTime := time.Now()
	// Send the DoExit command (this triggers world save)
	response, err := rconx.Execute(ctx, instanceName, "saveworld")
	if err != nil {
		return fmt.Errorf("failed to send command: %w", err)
	}

	// Check if response contains "World Saved" to confirm server is saving
	if strings.Contains(response, "World Saved") {
		logger.Infof("Server instance %s is saving world...", instanceName)
	} else {
		logger.Errorf("server instance %s is saving world error: %v", instanceName, err)
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
					logger.Infof("World saved successfully for instance %s. Save file: %s. saved Milliseconds %d",
						instanceName, saveFilePath, diffMilli)
					return nil
				}
			} else {
				logger.Warnf("Save file not found or error checking: %v", err)
			}
			logger.Infof("The world archive has not been uploaded yet: %s", instanceName)
		case <-ctx.Done():
			logger.Errorf("timeout waiting for world save to complete for instance %s. Save file: %s", instanceName, saveFilePath)
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
		if exited := procx.WaitProcessExit(ctxLocal, pid, 500*time.Millisecond); exited {
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
		logger.Warnf("Failed to tail log for shutdown monitoring (%s): %v", gameLogPath, err)
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
		if exited := procx.WaitProcessExit(ctx, pid, 500*time.Millisecond); exited {
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
		logger.Warnf("Failed to tail log for startup monitoring (%s): %v", gameLogPath, err)
	}
	<-startup
}

// findServerPIDBySaveDir 通过进程命令行中的 AltSaveDirectoryName 查找 ArkAscendedServer.exe 的 PID。
// 不依赖端口是否被监听，适用于启动中等过渡状态（匹配规则按平台拆分，见 gameproc_*.go）。
func findServerPIDBySaveDir(saveDir string) (int, error) {
	processes, err := queryGameProcesses(fmt.Sprintf("AltSaveDirectoryName=%s", saveDir))
	if err != nil {
		return 0, fmt.Errorf("process query failed: %w", err)
	}
	if len(processes) == 0 {
		return 0, fmt.Errorf("no ArkAscendedServer process found with AltSaveDirectoryName=%s", saveDir)
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
