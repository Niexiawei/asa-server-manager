package frpmanage

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

//go:embed frpc.exe
var frpcAssets embed.FS

// FrpcManager manages the frpc process lifecycle
type FrpcManager struct {
	runDir            string
	frpcPath          string
	cmd               *exec.Cmd
	mu                sync.Mutex
	running           bool
	startErr          error // Last start error
	execDoneCtx       context.Context
	execDoneCtxCancel func()
}

const (
	frpcConfigFileName = "frpc.toml"
)

var globalManager *FrpcManager
var frpConfigDir string // FRP 配置文件和可执行文件目录

// Initialize initializes the frpc manager and extracts frpc.exe, returns config directory path
func Initialize(basedir string) (string, error) {
	// Use asaserver.BaseDir instead of temp directory
	dir := filepath.Join(basedir, "frp")

	// Create frp directory if it doesn't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create frp directory: %v", err)
	}

	frpcPath := filepath.Join(dir, "frpc.exe")
	// Extract frpc.exe from embedded files
	data, err := frpcAssets.ReadFile("frpc.exe")
	if err != nil {
		return "", fmt.Errorf("failed to read embedded frpc.exe: %v", err)
	}

	// Write to file
	if err := os.WriteFile(frpcPath, data, 0755); err != nil {
		return "", fmt.Errorf("failed to write frpc.exe to frp directory: %v", err)
	}

	frpConfigDir = dir
	globalManager = &FrpcManager{
		runDir:   dir,
		frpcPath: frpcPath,
		running:  false,
	}

	return dir, nil
}

// Start starts the frpc process asynchronously
func (m *FrpcManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("frpc is already running")
	}

	// Check if config file exists, create default if not
	configPath := filepath.Join(m.runDir, "frpc.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := createDefaultFRPConfig(configPath); err != nil {
			return fmt.Errorf("failed to create default frpc config: %v", err)
		}
	}

	// Clear previous error
	m.startErr = nil

	// Launch process startup in background goroutine
	go m.asyncStart(configPath)

	return nil
}

// asyncStart performs the actual process startup in background
func (m *FrpcManager) asyncStart(configPath string) {
	m.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	// Create new command
	m.cmd = exec.CommandContext(ctx, m.frpcPath, "-c", configPath)
	m.execDoneCtx = ctx
	m.execDoneCtxCancel = cancel
	// Set up stdout/stderr to redirect to logger
	m.cmd.Stdout = &LogWriter{tag: "[frpc]", logFunc: logger.GetLogger().Infof}
	m.cmd.Stderr = &LogWriter{tag: "[frpc]", logFunc: logger.GetLogger().Errorf}

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
		m.startErr = fmt.Errorf("frpc process exited immediately: %v", err)
		m.mu.Unlock()
	case <-time.After(500 * time.Millisecond):
		// Process is still running
		m.mu.Lock()
		m.running = true
		m.mu.Unlock()
	}
}

// Stop stops the frpc process
func (m *FrpcManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running || m.cmd == nil {
		return fmt.Errorf("frpc is not running")
	}
	
	m.execDoneCtxCancel()
	m.running = false
	m.cmd = nil
	return nil
}

// Restart restarts the frpc process
func (m *FrpcManager) Restart() error {
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

// IsRunning checks if frpc is running
func (m *FrpcManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// CheckStatus checks the actual running status of frpc process
// Updates running flag if process has exited or failed to start
func (m *FrpcManager) CheckStatus() bool {
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
func (m *FrpcManager) Cleanup() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running && m.cmd != nil {
		m.cmd.Process.Kill()
	}

	if m.runDir != "" {
		if err := os.RemoveAll(m.runDir); err != nil {
			return fmt.Errorf("failed to cleanup temp directory: %v", err)
		}
	}

	logger.GetLogger().Infof(" frpc manager cleanup ...")

	return nil
}

// GetGlobalManager returns the global frpc manager instance
func GetGlobalManager() *FrpcManager {
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

// createDefaultFRPConfig creates a default frpc configuration file
func createDefaultFRPConfig(configPath string) error {
	defaultConfig := `# FRP Client Configuration
# Generated automatically if not present
serverAddr = "127.0.0.1"
serverPort = 7000
auth.token = "your-token-here"

loginFailExit = true
protocol = "tcp"

# Client metadata
metaData.version = "0.57.0"

# Connection pool
connPoolCount = 1

# Log
log.to = "console"
log.level = "info"
log.maxDays = 3
`
	return os.WriteFile(configPath, []byte(defaultConfig), 0644)
}
