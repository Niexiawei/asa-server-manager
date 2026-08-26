package syncthingmanage

import (
	"asa-server/pkg/proctree"
	"asa-server/pkg/procx"
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"asa-server/internal/logger"
)

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
	job           *proctree.ProcessJob
	cmdPid        int
}

const (
	syncthingConfigFileName = "config.xml"
)

var globalManager *SyncthingManager
var syncthingConfigDir string // Syncthing 配置文件和可执行文件目录

// Initialize initializes the syncthing manager and returns the config
// directory path. It ensures a syncthing binary is present, downloading the
// pinned release from GitHub on first run (see install.go), but a download
// failure does not fail Initialize itself — this runs at API server startup
// (InitializationBasicComponents), and a network hiccup fetching an optional
// companion tool must not take down the whole server. Start() surfaces the
// missing binary as its own error instead.
func Initialize(basedir string) (string, error) {
	// Use BaseDir instead of temp directory
	dir := filepath.Join(basedir, "syncthing")

	// Create syncthing directory if it doesn't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create syncthing directory: %v", err)
	}

	syncthingConfigDir = dir
	globalManager = &SyncthingManager{
		runDir:  dir,
		running: false,
	}

	binPath, err := ensureSyncthingBinary(context.Background(), dir)
	if err != nil {
		logger.GetLogger().Errorf(
			"syncthing binary unavailable, start/restart will fail until this is resolved (check network access / download.github_proxy): %v", err)
		return dir, nil
	}
	globalManager.syncthingPath = binPath

	return dir, nil
}

// Start starts the syncthing process asynchronously
func (m *SyncthingManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("syncthing is already running")
	}
	if m.syncthingPath == "" {
		return fmt.Errorf("syncthing binary is not available (download failed at startup, see logs)")
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

	job, err := proctree.Start(ctx, cmd)
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
		if exited := procx.WaitProcessExit(ctx, m.cmdPid, 2*time.Second); exited {
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
		if m.IsRunning() {
			return err
		}
	}

	// Wait a bit before restarting
	time.Sleep(500 * time.Millisecond)

	return m.Start()
}

// IsRunning checks if syncthing is running
func (m *SyncthingManager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// GetStartErr returns the last start error
func (m *SyncthingManager) GetStartErr() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
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

	return m.running
}

// Cleanup removes the temp directory and files
func (m *SyncthingManager) Cleanup() error {
	// C3 fix: Call Stop() before acquiring lock to avoid deadlock
	if err := m.Stop(); err != nil {
		// M11 fix: Log warning but continue cleanup even if Stop fails
		logger.GetLogger().Warnf("syncthing Cleanup: Stop failed (continuing cleanup): %v", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

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

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// LogWriter is an adapter that writes log messages to logger
type LogWriter struct {
	tag     string
	logFunc func(string, ...interface{})
}

func (w *LogWriter) Write(p []byte) (n int, err error) {
	text := string(p)
	// Remove ANSI color codes
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
