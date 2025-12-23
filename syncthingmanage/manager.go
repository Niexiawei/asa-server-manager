package syncthingmanage

import (
	"asa-server/common"
	"asa-server/processjob"
	"bufio"
	"context"
	"crypto/md5"
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"asa-server/logger"
)

//go:embed syncthing.exe
var syncthingAssets embed.FS

// SyncthingManager manages the syncthing process lifecycle
type SyncthingManager struct {
	runDir        string
	syncthingPath string
	cmd           *exec.Cmd
	mu            sync.RWMutex
	running       bool
	startErr      error // Last start error
	Ctx           context.Context
	cancel        func()
	job           *processjob.ProcessJob
	cmdPid        int
}

const (
	syncthingConfigFileName = "config.xml"
)

var globalManager *SyncthingManager
var syncthingConfigDir string // Syncthing 配置文件和可执行文件目录

// Initialize initializes the syncthing manager and extracts syncthing.exe, returns config directory path
func Initialize(basedir string) (string, error) {
	// Use asaserver.BaseDir instead of temp directory
	dir := filepath.Join(basedir, "syncthing")

	// Create syncthing directory if it doesn't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create syncthing directory: %v", err)
	}

	syncthingPath := filepath.Join(dir, "syncthing.exe")
	// Extract syncthing.exe from embedded files
	data, err := syncthingAssets.ReadFile("syncthing.exe")
	if err != nil {
		return "", fmt.Errorf("failed to read embedded syncthing.exe: %v", err)
	}

	// Calculate MD5 of embedded file
	embeddedMD5 := md5.Sum(data)

	// Check if file already exists and compare MD5
	if fileInfo, err := os.Stat(syncthingPath); err == nil && fileInfo.Mode().IsRegular() {
		existingData, err := os.ReadFile(syncthingPath)
		if err == nil {
			existingMD5 := md5.Sum(existingData)
			if existingMD5 == embeddedMD5 {
				// MD5 matches, skip writing
				logger.GetLogger().Infof("syncthing.exe MD5 matches, skipping write")
				syncthingConfigDir = dir
				globalManager = &SyncthingManager{
					runDir:        dir,
					syncthingPath: syncthingPath,
					running:       false,
				}
				return dir, nil
			}
		}
	}

	// Write to file
	if err := os.WriteFile(syncthingPath, data, 0755); err != nil {
		return "", fmt.Errorf("failed to write syncthing.exe to syncthing directory: %v", err)
	}

	syncthingConfigDir = dir
	globalManager = &SyncthingManager{
		runDir:        dir,
		syncthingPath: syncthingPath,
		running:       false,
	}

	return dir, nil
}

// Start starts the syncthing process asynchronously
func (m *SyncthingManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("syncthing is already running")
	}

	// Clear previous error
	m.startErr = nil

	// Launch process startup in background goroutine
	go m.asyncStart()

	return nil
}

// asyncStart performs the actual process startup in background
func (m *SyncthingManager) asyncStart() {
	ctx, cancel := context.WithCancel(context.Background())
	// Create new command
	cmd := exec.Command(m.syncthingPath, "serve", "--home", m.runDir,
		"--no-browser", "--no-restart",
		"--no-upgrade")
	// Set up stdout/stderr to redirect to logger
	cmd.Stdout = &LogWriter{tag: "[syncthing]", logFunc: logger.GetLogger().Infof}
	cmd.Stderr = &LogWriter{tag: "[syncthing]", logFunc: logger.GetLogger().Errorf}

	job, err := processjob.Start(ctx, cmd)
	if err != nil {
		m.startErr = err
		m.cmd = nil
		cancel()
		return
	}

	m.mu.Lock()
	m.Ctx = ctx
	m.job = job
	m.cancel = cancel
	m.running = true
	m.startErr = nil
	m.cmd = cmd
	m.cmdPid = cmd.Process.Pid
	m.mu.Unlock()

	// Monitor process in background to detect if it exits immediately
	done := make(chan error, 1)

	go func() {
		done <- job.Wait()
	}()

	go func() {
		if exited := common.WaitGamePidExit(ctx, m.cmdPid); exited {
			fmt.Println("syncthing process exited")
			cancel()
			job.Close()
		}
	}()

	// Check if process is still running after startup
	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.running = false
			m.mu.Unlock()
			return
		case err := <-done:
			m.mu.Lock()
			m.running = false
			m.cmd = nil
			m.startErr = fmt.Errorf("syncthing process exited immediately: %v", err)
			logger.GetLogger().Infof("syncthing process exited err: %v", err)
			m.mu.Unlock()
			return
		case <-time.After(1000 * time.Millisecond):
			m.mu.Lock()
			m.running = true
			m.mu.Unlock()
		}
	}
}

// Stop stops the syncthing process
func (m *SyncthingManager) Stop() error {
	logger.GetLogger().Infof("syncthing stoping ...")
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running || m.cmd == nil {
		return fmt.Errorf("syncthing is not running")
	}

	m.cancel()
	m.job.Close()
	m.running = false
	m.cmd = nil
	logger.GetLogger().Infof("syncthing stoped")
	return nil
}

// Restart restarts the syncthing process
func (m *SyncthingManager) Restart() error {
	if err := m.Stop(); err != nil {
		// If not running, just start it
		if m.running {
			return err
		}
	}

	// Wait a bit before restarting
	time.Sleep(500 * time.Millisecond)

	return m.Start()
}

// IsRunning checks if syncthing is running
func (m *SyncthingManager) IsRunning() bool {
	return m.running
}

// GetStartErr returns the last start error
func (m *SyncthingManager) GetStartErr() error {
	return m.startErr
}

// CheckStatus checks the actual running status of syncthing process
// Updates running flag if process has exited or failed to start
func (m *SyncthingManager) CheckStatus() bool {
	if !m.running || m.cmd == nil {
		return false
	}

	return m.running
}

// Cleanup removes the temp directory and files
func (m *SyncthingManager) Cleanup() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.Stop(); err != nil {
		return err
	}

	if m.runDir != "" {
		if err := os.RemoveAll(m.runDir); err != nil {
			return fmt.Errorf("failed to cleanup temp directory: %v", err)
		}
	}

	logger.GetLogger().Infof("syncthing manager cleanup ...")

	return nil
}

// GetGlobalManager returns the global syncthing manager instance
func GetGlobalManager() *SyncthingManager {
	return globalManager
}

// LogWriter is an adapter that writes log messages to logger
type LogWriter struct {
	tag     string
	logFunc func(string, ...interface{})
}

func (w *LogWriter) Write(p []byte) (n int, err error) {
	text := string(p)
	// Remove ANSI color codes
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	text = ansiRegex.ReplaceAllString(text, "")

	// Split by newline and log each line
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			w.logFunc("%s %s", w.tag, line)
		}
	}
	return len(p), nil
}

var _ io.Writer = (*LogWriter)(nil)
