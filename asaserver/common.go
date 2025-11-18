package asaserver

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
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
		lastPosition := int64(0)

		// Create a watcher for file system events
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			printFunc(fmt.Sprintf("❌ Failed to create file watcher: %v", err))
			return
		}
		defer watcher.Close()

		// Watch the logs directory for changes
		logsDir := filepath.Dir(logPath)
		if err := watcher.Add(logsDir); err != nil {
			printFunc(fmt.Sprintf("❌ Failed to watch logs directory: %v", err))
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
			if content, newPos, _, found := readNewLogContent(logPath, lastPosition); found {
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
				printFunc(fmt.Sprintf("❌ Watcher error: %v", err))
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
func readNewLogContent(logPath string, lastPosition int64) (string, int64, int, bool) {
	file, err := os.Open(logPath)
	if err != nil {
		return "", lastPosition, 0, false
	}
	defer file.Close()

	// Get file size
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
