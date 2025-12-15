package syncthingmanage

import (
	"bufio"
	"context"
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
	runDir            string
	syncthingPath     string
	cmd               *exec.Cmd
	mu                sync.Mutex
	running           bool
	startErr          error // Last start error
	execDoneCtx       context.Context
	execDoneCtxCancel func()
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
	m.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	// Create new command
	m.cmd = exec.CommandContext(ctx, m.syncthingPath, "serve", "--home", m.runDir, "--no-browser", "--no-restart")
	m.execDoneCtx = ctx
	m.execDoneCtxCancel = cancel
	// Set up stdout/stderr to redirect to logger
	m.cmd.Stdout = &LogWriter{tag: "[syncthing]", logFunc: logger.GetLogger().Infof}
	m.cmd.Stderr = &LogWriter{tag: "[syncthing]", logFunc: logger.GetLogger().Errorf}

	// Start the process
	if err := m.cmd.Start(); err != nil {
		m.startErr = err
		m.cmd = nil
		m.mu.Unlock()
		return
	}

	m.mu.Unlock()

	// Monitor process in background to detect if it exits immediately
	done := make(chan error, 1)
	go func() {
		done <- m.cmd.Wait()
	}()

	// Check if process is still running after startup
	select {
	case err := <-done:
		// Process exited immediately
		m.mu.Lock()
		m.running = false
		m.cmd = nil
		m.startErr = fmt.Errorf("syncthing process exited immediately: %v", err)
		logger.GetLogger().Infof("syncthing process exited err: %v", err)
		m.mu.Unlock()
	case <-time.After(500 * time.Millisecond):
		// Process is still running
		m.mu.Lock()
		m.running = true
		m.mu.Unlock()
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

	m.execDoneCtxCancel()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// GetStartErr returns the last start error
func (m *SyncthingManager) GetStartErr() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startErr
}

// CheckStatus checks the actual running status of syncthing process
// Updates running flag if process has exited or failed to start
func (m *SyncthingManager) CheckStatus() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running || m.cmd == nil {
		return false
	}

	// If ProcessState is nil, process hasn't exited yet
	if m.cmd.ProcessState == nil {
		return m.running
	}

	// Process has exited or failed
	if m.cmd.ProcessState.Exited() {
		m.running = false
		m.cmd = nil
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
