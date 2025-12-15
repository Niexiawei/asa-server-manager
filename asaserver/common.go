package asaserver

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// TailLogFile monitors a log file in real-time asynchronously
// Returns a stop function to terminate monitoring
// The printFunc closure is called with each new log line
func TailLogFile(logPath string, printFunc func(string)) func() {
	stopChan := make(chan struct{})

	go func() {
		// 等待日志文件存在
		for {
			select {
			case <-stopChan:
				return
			default:
			}
			if _, err := os.Stat(logPath); err == nil {
				break // 文件存在，开始监听
			}
			time.Sleep(100 * time.Millisecond) // 每100ms检查一次
		}

		// Open file once in read-only mode
		file, err := os.OpenFile(logPath, os.O_RDONLY, 0)
		if err != nil {
			printFunc("Failed to open log file: " + fmt.Sprintf("%v", err))
			return
		}
		defer file.Close()

		lastPosition := int64(0)

		// Create a watcher for file system events
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			printFunc("Failed to create file watcher: " + fmt.Sprintf("%v", err))
			return
		}
		defer watcher.Close()

		// Watch the logs directory for changes
		logsDir := filepath.Dir(logPath)
		if err := watcher.Add(logsDir); err != nil {
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
							printFunc(line)
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
		var allLines []string
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			allLines = append(allLines, scanner.Text())
		}

		// Calculate starting index for the last N lines
		startIdx := 0
		if len(allLines) > lastNLines {
			startIdx = len(allLines) - lastNLines
		}

		// Send the last N lines (or all lines if less than N)
		for i := startIdx; i < len(allLines); i++ {
			select {
			case <-stopChan:
				return
			default:
				if allLines[i] != "" {
					printFunc(allLines[i])
				}
			}
		}

		// Get the current file position to start monitoring from
		lastPosition, _ := file.Seek(0, 1)

		// Create a watcher for file system events
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			printFunc("Failed to create file watcher: " + fmt.Sprintf("%v", err))
			return
		}
		defer watcher.Close()

		// Watch the logs directory for changes
		logsDir := filepath.Dir(logPath)
		if err := watcher.Add(logsDir); err != nil {
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
	newPosition, _ := file.Seek(0, 1)

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
