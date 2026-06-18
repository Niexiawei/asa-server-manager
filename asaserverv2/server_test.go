package asaserverv2

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testInstanceName = "jibian_test"

// setupTestEnv 初始化测试环境
// 设置 ASA_BASEDIR 并初始化目录和日志
func setupTestEnv(t *testing.T) {
	t.Helper()

	// 设置基础目录
	if err := os.Setenv("ASA_BASEDIR", "E:\\asa_server_data"); err != nil {
		t.Fatalf("Failed to set ASA_BASEDIR: %v", err)
	}

	// 初始化目录
	if err := asaserver.EnsureDirectories(); err != nil {
		t.Fatalf("Failed to ensure directories: %v", err)
	}

	// 初始化日志
	logger.InitLoggerWithBaseDir(asaserver.BaseDir)

	// 初始化日志映射
	if err := asaserver.InitializeLogMapping(); err != nil {
		t.Fatalf("Failed to initialize log mapping: %v", err)
	}

	if err := asaserver.InitStateManager(asaserver.BaseDir); err != nil {
		t.Fatalf("Failed to initialize state manager: %v", err)
	}

	t.Logf("BaseDir: %s", asaserver.BaseDir)
	t.Logf("InstancesDir: %s", asaserver.InstancesDir)
	t.Logf("ServerFilesDir: %s", asaserver.ServerFilesDir)
}

// TestSyncInstanceMirror 测试镜像目录创建和清理
func TestSyncInstanceMirror(t *testing.T) {
	setupTestEnv(t)

	// 加载实例配置
	cfg, err := asaserver.LoadInstanceConfig(testInstanceName)
	if err != nil {
		t.Fatalf("Failed to load instance config: %v", err)
	}
	t.Logf("Instance config loaded: ServerName=%s, Port=%d, RCONPort=%d", cfg.ServerName, cfg.Port, cfg.RCONPort)

	// 创建镜像目录
	mirrorDir, err := SyncInstanceMirror(testInstanceName, cfg)
	if err != nil {
		t.Fatalf("Failed to setup instance mirror: %v", err)
	}
	t.Logf("Mirror dir: %s", mirrorDir)

	// 验证镜像目录存在
	if _, err := os.Stat(mirrorDir); os.IsNotExist(err) {
		t.Fatal("Mirror directory does not exist after setup")
	}

	// 验证关键路径
	exePath := mirrorDir + "/ShooterGame/Binaries/Win64/ArkAscendedServer.exe"
	if _, err := os.Stat(exePath); os.IsNotExist(err) {
		t.Errorf("ArkAscendedServer.exe not found at %s", exePath)
	}

	configPath := mirrorDir + "/ShooterGame/Saved/Config/WindowsServer"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Errorf("Config junction not found at %s", configPath)
	}

	logsPath := mirrorDir + "/ShooterGame/Saved/Logs"
	if _, err := os.Stat(logsPath); os.IsNotExist(err) {
		t.Errorf("Logs junction not found at %s", logsPath)
	}

	// 清理
	if err := CleanupInstanceMirror(testInstanceName); err != nil {
		t.Fatalf("Failed to cleanup instance mirror: %v", err)
	}

	// 验证清理后镜像目录不存在
	if _, err := os.Stat(mirrorDir); !os.IsNotExist(err) {
		t.Error("Mirror directory still exists after cleanup")
	}

	t.Log("Mirror setup and cleanup completed successfully")
}

// TestMirrorStructure 测试并打印镜像目录结构
func TestMirrorStructure(t *testing.T) {
	setupTestEnv(t)

	cfg, err := asaserver.LoadInstanceConfig(testInstanceName)
	if err != nil {
		t.Fatalf("Failed to load instance config: %v", err)
	}

	mirrorDir, err := SyncInstanceMirror(testInstanceName, cfg)
	if err != nil {
		t.Fatalf("Failed to setup mirror: %v", err)
	}
	defer CleanupInstanceMirror(testInstanceName)

	// 打印目录结构
	t.Logf("Mirror structure at: %s", mirrorDir)
	filepath.Walk(mirrorDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(mirrorDir, path)
		depth := 0
		for _, c := range rel {
			if c == '\\' || c == '/' {
				depth++
			}
		}
		indent := ""
		for i := 0; i < depth; i++ {
			indent += "  "
		}

		suffix := ""
		// 使用 cmd /c dir 检查是否是 junction（Go 的 Lstat 在 Windows 上不可靠）
		fi, lerr := os.Lstat(path)
		if lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			target, rlerr := os.Readlink(path)
			if rlerr == nil {
				suffix = fmt.Sprintf(" [JUNCTION -> %s]", target)
			} else {
				suffix = fmt.Sprintf(" [SYMLINK err=%v]", rlerr)
			}
		}
		if info.IsDir() && suffix == "" {
			suffix = " [DIR]"
		}
		t.Logf("%s%s%s", indent, info.Name(), suffix)
		return nil
	})
}

// TestMirrorRequiresConfig 测试 Config 目录不存在时应该报错
func TestMirrorRequiresConfig(t *testing.T) {
	setupTestEnv(t)

	cfg := &asaserver.InstanceConfig{
		SaveDir: "nonexistent_instance",
	}

	// 使用一个不存在的实例名，Config 目录不存在
	_, err := SyncInstanceMirror("nonexistent_instance_12345", cfg)
	if err == nil {
		t.Fatal("Expected error when Config directory does not exist, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

// TestStartAndStopServer 测试启动和停止服务器
// 使用 context 控制生命周期
func TestStartAndStopServer(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setupTestEnv(t)

	// 加载实例配置
	cfg, err := asaserver.LoadInstanceConfig(testInstanceName)
	if err != nil {
		t.Fatalf("Failed to load instance config: %v", err)
	}
	t.Logf("Starting server: %s (Port: %d, RCONPort: %d)", cfg.ServerName, cfg.Port, cfg.RCONPort)

	// 创建带超时的 context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 启动服务器
	t.Log("Starting server...")
	startErr := make(chan error, 1)
	go func() {
		startErr <- StartServer(testInstanceName, asaserver.WithCtx(ctx))
		log.Println("Started server")
	}()

	// 等待启动完成或超时
	select {
	case err := <-startErr:
		if err != nil {
			t.Fatalf("Failed to start server: %v", err)
		}
		t.Log("Server started successfully")
	case <-ctx.Done():
		t.Fatal("Timeout waiting for server to start")
	}

	// 等待一段时间让服务器完全初始化
	t.Log("Waiting 30 seconds for server initialization...")
	time.Sleep(100 * time.Second)

	// 验证服务器正在运行
	running, err := asaserver.IsServerRunning(testInstanceName)
	if err != nil {
		t.Fatalf("Failed to check server status: %v", err)
	}
	if !running {
		t.Fatal("Server should be running after start")
	}
	t.Log("Server is running")

	// 停止服务器
	t.Log("Stopping server...")
	if err := StopServer(testInstanceName); err != nil {
		t.Fatalf("Failed to stop server: %v", err)
	}

	// 验证服务器已停止
	running, err = asaserver.IsServerRunning(testInstanceName)
	if err != nil {
		t.Fatalf("Failed to check server status after stop: %v", err)
	}
	if running {
		t.Fatal("Server should not be running after stop")
	}

	// 验证镜像目录在停止后保留（持久化）
	mirrorDir := InstanceMirrorDir(testInstanceName)
	if _, err := os.Stat(mirrorDir); os.IsNotExist(err) {
		t.Errorf("Mirror directory should persist after stop: %s", mirrorDir)
	}

	// 手动清理镜像（模拟 CleanupMirrorCache）
	if err := CleanupMirrorCache(testInstanceName); err != nil {
		t.Errorf("Failed to cleanup mirror cache: %v", err)
	}

	t.Log("Server stopped successfully, mirror preserved then cleaned up")
}

// TestStartServerWithContextCancellation 测试通过取消 context 停止启动
func TestStartServerWithContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setupTestEnv(t)

	// 创建一个短超时的 context（10秒后自动取消）
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Log("Attempting to start server with 10s timeout context...")

	err := StartServer(testInstanceName, asaserver.WithCtx(ctx))
	if err != nil {
		t.Logf("Server start failed (expected due to short timeout): %v", err)
	} else {
		t.Log("Server started within timeout, stopping...")
		// 如果启动成功了，需要停止它
		if stopErr := StopServer(testInstanceName); stopErr != nil {
			t.Logf("Failed to stop server: %v", stopErr)
		}
	}

	// 确保镜像被清理（无论启动成功与否）
	mirrorDir := InstanceMirrorDir(testInstanceName)
	if _, err := os.Stat(mirrorDir); !os.IsNotExist(err) {
		t.Logf("Cleaning up mirror directory after test: %s", mirrorDir)
		CleanupInstanceMirror(testInstanceName)
	}
}

// TestForceStopServer 测试强制停止服务器
func TestForceStopServer(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	setupTestEnv(t)

	cfg, err := asaserver.LoadInstanceConfig(testInstanceName)
	if err != nil {
		t.Fatalf("Failed to load instance config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 启动服务器
	t.Log("Starting server for force stop test...")
	startErr := make(chan error, 1)
	go func() {
		startErr <- StartServer(testInstanceName, asaserver.WithCtx(ctx))
	}()

	select {
	case err := <-startErr:
		if err != nil {
			t.Fatalf("Failed to start server: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Timeout waiting for server to start")
	}

	// 确认运行中
	running, _ := asaserver.IsServerRunning(testInstanceName)
	if !running {
		t.Fatal("Server should be running")
	}

	t.Logf("Server running on port %d, force stopping...", cfg.Port)

	// 强制停止
	if err := ForceStopServer(testInstanceName); err != nil {
		t.Fatalf("Failed to force stop server: %v", err)
	}

	// 验证已停止
	time.Sleep(2 * time.Second)
	running, _ = asaserver.IsServerRunning(testInstanceName)
	if running {
		t.Fatal("Server should not be running after force stop")
	}

	// 验证镜像已清理
	mirrorDir := InstanceMirrorDir(testInstanceName)
	if _, err := os.Stat(mirrorDir); !os.IsNotExist(err) {
		t.Errorf("Mirror directory should be cleaned up after force stop: %s", mirrorDir)
	}

	t.Log("Force stop completed successfully")
}
