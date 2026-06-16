package asaserverv2

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const mirrorDirPrefix = "server-files-tmp-"

// exeFiles 是需要复制（而非链接）的可执行文件名
var exeFiles = map[string]bool{
	"ArkAscendedServer.exe": true,
	"AsaApiLoader.exe":      true,
}

// InstanceMirrorDir 返回实例镜像目录路径
func InstanceMirrorDir(instanceName string) string {
	return filepath.Join(asaserver.BaseDir, mirrorDirPrefix+instanceName)
}

// SetupInstanceMirror 创建实例镜像目录
// 通过 filepath.Walk 扫描 server-files，自动为目录创建 junction
// 仅复制 exe 文件，Config/Logs/Save 指向实例本地目录
func SetupInstanceMirror(instanceName string, cfg *asaserver.InstanceConfig) (string, error) {
	mirrorDir := InstanceMirrorDir(instanceName)

	// 清理已有镜像
	if _, err := os.Stat(mirrorDir); err == nil {
		logger.GetLogger().Infof("Cleaning up existing mirror for instance '%s'", instanceName)
		if err := CleanupInstanceMirror(instanceName); err != nil {
			return "", fmt.Errorf("failed to cleanup existing mirror: %w", err)
		}
	}

	// 检查实例 Config 目录是否存在且非空
	instanceConfigDir := filepath.Join(asaserver.InstancesDir, instanceName, "Config")
	instanceLogsDir := filepath.Join(asaserver.InstancesDir, instanceName, "Logs")
	instanceSaveDir := filepath.Join(asaserver.InstancesDir, instanceName, "Save")

	// Config 目录必须已存在且非空，否则报错
	if _, err := os.Stat(instanceConfigDir); os.IsNotExist(err) {
		return "", fmt.Errorf("instance config directory does not exist: %s, please create the instance first", instanceConfigDir)
	}
	entries, _ := os.ReadDir(instanceConfigDir)
	if len(entries) == 0 {
		return "", fmt.Errorf("instance config directory is empty: %s, please configure the instance first", instanceConfigDir)
	}

	// Logs 和 Save 目录可以自动创建
	for _, dir := range []string{instanceLogsDir, instanceSaveDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("failed to create instance directory %s: %w", dir, err)
		}
	}

	// 确保 Logs 目录中至少有 ShooterGame.log
	logFile := filepath.Join(instanceLogsDir, "ShooterGame.log")
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		if err := os.WriteFile(logFile, []byte(""), 0644); err != nil {
			return "", fmt.Errorf("failed to create log file: %w", err)
		}
	}

	// 构建例外路径映射
	// key: server-files 内的相对路径，value: junction 目标
	exceptionTargets := buildExceptionTargets(instanceName, cfg)

	logger.GetLogger().Infof("Creating instance mirror at %s", mirrorDir)

	// Walk server-files 目录树
	err := filepath.Walk(asaserver.ServerFilesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(asaserver.ServerFilesDir, path)
		if err != nil {
			return err
		}

		// 根目录特殊处理
		if relPath == "." {
			return os.MkdirAll(mirrorDir, 0755)
		}

		// 标准化路径分隔符
		relPath = filepath.ToSlash(relPath)
		mirrorPath := filepath.Join(mirrorDir, filepath.FromSlash(relPath))

		if info.IsDir() {
			skip, err := processDirectory(path, mirrorPath, relPath, info, exceptionTargets)
			if err != nil {
				return err
			}
			if skip {
				return filepath.SkipDir
			}
			return nil
		}

		// 文件处理
		return processFile(path, mirrorPath, relPath, info)
	})

	if err != nil {
		// 清理失败的镜像
		_ = CleanupInstanceMirror(instanceName)
		return "", fmt.Errorf("failed to walk server files: %w", err)
	}

	// Walk 完成后，补充缺失的 exception targets
	// SaveDir 目录在源中可能不存在（服务器启动后才生成），需要预先创建
	for relPath, target := range exceptionTargets {
		mirrorPath := filepath.Join(mirrorDir, filepath.FromSlash(relPath))
		if _, statErr := os.Stat(mirrorPath); os.IsNotExist(statErr) {
			// 源目录不存在，Walk 未访问 → junction 未创建
			// 预先在源目录创建该目录
			srcPath := filepath.Join(asaserver.ServerFilesDir, filepath.FromSlash(relPath))
			if mkErr := os.MkdirAll(srcPath, 0755); mkErr != nil {
				logger.GetLogger().Warnf("Failed to pre-create source dir %s: %v", srcPath, mkErr)
			}
			// 在镜像中创建 junction
			if jErr := createJunction(mirrorPath, target); jErr != nil {
				_ = CleanupInstanceMirror(instanceName)
				return "", fmt.Errorf("failed to create exception junction for %s: %w", relPath, jErr)
			}
			logger.GetLogger().Infof("Pre-created exception junction: %s -> %s", relPath, target)
		}
	}

	logger.GetLogger().Infof("Instance mirror created successfully at %s", mirrorDir)
	return mirrorDir, nil
}

// buildExceptionTargets 构建例外路径到目标目录的映射
func buildExceptionTargets(instanceName string, cfg *asaserver.InstanceConfig) map[string]string {
	targets := map[string]string{
		"ShooterGame/Saved/Config/WindowsServer": filepath.Join(asaserver.InstancesDir, instanceName, "Config"),
		"ShooterGame/Saved/Logs":                 filepath.Join(asaserver.InstancesDir, instanceName, "Logs"),
	}

	// SaveDir 映射
	saveDir := cfg.SaveDir
	if saveDir == "" {
		saveDir = instanceName
	}
	targets["ShooterGame/Saved/"+saveDir] = filepath.Join(asaserver.InstancesDir, instanceName, "Save")

	return targets
}

// processDirectory 处理目录条目
// 返回 (skip bool, err error) - skip=true 表示跳过该目录的子目录遍历
func processDirectory(srcPath, mirrorPath, relPath string, info os.FileInfo, exceptionTargets map[string]string) (bool, error) {
	// 检查是否是精确的例外路径
	if target, ok := exceptionTargets[relPath]; ok {
		// 创建 junction 指向实例本地目录，不需要遍历子目录
		return true, createJunction(mirrorPath, target)
	}

	// 检查是否有以此路径为前缀的例外
	hasExceptionChild := false
	for exPath := range exceptionTargets {
		if strings.HasPrefix(exPath, relPath+"/") {
			hasExceptionChild = true
			break
		}
	}

	if hasExceptionChild {
		// 创建真实目录，Walk 会自动递归进入子目录
		return false, os.MkdirAll(mirrorPath, 0755)
	}

	// 检查是否包含需要复制的 exe 文件（如 Win64 目录包含 ArkAscendedServer.exe）
	if containsExeFiles(srcPath) {
		// 创建真实目录，Walk 会递归进入处理 exe 文件
		return false, os.MkdirAll(mirrorPath, 0755)
	}

	// 无例外子路径：创建 junction 指向原始目录，跳过子目录遍历
	return true, createJunction(mirrorPath, srcPath)
}

// containsExeFiles 递归检查目录（含子目录）是否包含需要复制的 exe 文件
func containsExeFiles(dirPath string) bool {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			// 递归检查子目录
			if containsExeFiles(filepath.Join(dirPath, entry.Name())) {
				return true
			}
		} else if exeFiles[entry.Name()] {
			return true
		}
	}
	return false
}

// processFile 处理文件条目
func processFile(srcPath, mirrorPath, relPath string, info os.FileInfo) error {
	// 检查父目录是否已经是 junction
	// 如果是，文件通过 junction 即可访问，无需处理
	parentDir := filepath.Dir(mirrorPath)
	if isJunctionOrSymlink(parentDir) {
		return nil // 父目录是 junction，文件已可访问
	}

	// 检查是否是 exe 文件需要复制
	fileName := info.Name()
	if exeFiles[fileName] {
		return copyFile(srcPath, mirrorPath)
	}

	// 其他文件：创建文件符号链接
	return createFileSymlink(mirrorPath, srcPath)
}

// isJunctionOrSymlink 检查路径是否是 junction 或符号链接
func isJunctionOrSymlink(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	// os.ModeSymlink 检测符号链接和 junction
	if fi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	// 额外检查 Windows reparse point（junction 在某些 Go 版本中不被 Lstat 检测为 ModeSymlink）
	if isWindowsReparsePoint(path) {
		return true
	}
	return false
}

// isWindowsReparsePoint 检查 Windows 路径是否是 reparse point（包括 junction）
func isWindowsReparsePoint(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	// 在 Windows 上，junction 会被 os.Lstat 返回的 FileInfo.Mode() 包含 ModeSymlink
	// 但为了安全起见，也检查文件属性
	if fi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	return false
}

// createJunction 创建 NTFS junction
func createJunction(linkPath, targetPath string) error {
	// 确保目标路径是绝对路径
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %w", targetPath, err)
	}

	// 确保链接的父目录存在
	parentDir := filepath.Dir(linkPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
	}

	cmd := exec.Command("cmd", "/c", "mklink", "/J", linkPath, absTarget)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create junction %s -> %s: %s: %w", linkPath, absTarget, string(output), err)
	}

	logger.GetLogger().Debugf("Created junction: %s -> %s", linkPath, absTarget)
	return nil
}

// createFileSymlink 创建文件符号链接
// 如果创建失败且不是 exe 文件，跳过（不回退到复制，避免复制大量非必要文件如 .pdb）
func createFileSymlink(linkPath, targetPath string) error {
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %w", targetPath, err)
	}

	parentDir := filepath.Dir(linkPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
	}

	// 尝试使用 os.Symlink（需要管理员权限或开发者模式）
	if err := os.Symlink(absTarget, linkPath); err != nil {
		// 回退：使用 mklink 命令
		cmd := exec.Command("cmd", "/c", "mklink", linkPath, absTarget)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		output, cmdErr := cmd.CombinedOutput()
		if cmdErr != nil {
			// 文件符号链接失败（无权限），跳过该文件
			// 目录内的非 exe 文件可通过其他方式访问
			logger.GetLogger().Debugf("Skipped file symlink (no permission): %s", linkPath)
			return nil
		}
		_ = output
	}

	logger.GetLogger().Debugf("Created file symlink: %s -> %s", linkPath, absTarget)
	return nil
}

// copyFile 复制单个文件
func copyFile(src, dst string) error {
	// 确保目标的父目录存在
	parentDir := filepath.Dir(dst)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %w", src, err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", dst, err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy file %s -> %s: %w", src, dst, err)
	}

	// 保留文件权限
	srcInfo, err := os.Stat(src)
	if err == nil {
		_ = os.Chmod(dst, srcInfo.Mode())
	}

	logger.GetLogger().Debugf("Copied file: %s -> %s", src, dst)
	return nil
}

// CleanupInstanceMirror 安全删除实例镜像目录
// 仅删除链接本身，不删除链接目标
func CleanupInstanceMirror(instanceName string) error {
	mirrorDir := InstanceMirrorDir(instanceName)

	if _, err := os.Stat(mirrorDir); os.IsNotExist(err) {
		return nil // 不存在，无需清理
	}

	logger.GetLogger().Infof("Cleaning up instance mirror: %s", mirrorDir)

	// 深度优先遍历，按深度降序清理
	var allEntries []struct {
		path  string
		depth int
		isDir bool
	}

	err := filepath.Walk(mirrorDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(mirrorDir, path)
		depth := strings.Count(filepath.ToSlash(relPath), "/")
		allEntries = append(allEntries, struct {
			path  string
			depth int
			isDir bool
		}{path, depth, info.IsDir()})
		return nil
	})
	if err != nil {
		logger.GetLogger().Warnf("Error walking mirror directory: %v", err)
	}

	// 按深度降序排序
	sortByDepthDescending(allEntries)

	// 第一步：删除所有符号链接/junction（不跟符号链接删除目标）
	for _, entry := range allEntries {
		if entry.path == mirrorDir {
			continue
		}

		if isJunctionOrSymlink(entry.path) {
			if err := os.Remove(entry.path); err != nil {
				logger.GetLogger().Warnf("Failed to remove symlink/junction %s: %v", entry.path, err)
			} else {
				logger.GetLogger().Debugf("Removed symlink/junction: %s", entry.path)
			}
		}
	}

	// 第二步：清理剩余的真实文件和空目录
	// 重新遍历（因为符号链接已删除，结构可能已简化）
	err = filepath.Walk(mirrorDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 忽略已删除的条目
		}
		if path == mirrorDir {
			return nil
		}

		// 跳过仍然存在的符号链接（不应该还有）
		if isJunctionOrSymlink(path) {
			return nil
		}

		if info.IsDir() {
			// 尝试删除空目录
			if err := os.Remove(path); err != nil {
				// 非空目录，稍后处理
			}
		} else {
			// 删除真实文件
			if err := os.Remove(path); err != nil {
				logger.GetLogger().Warnf("Failed to remove file %s: %v", path, err)
			}
		}
		return nil
	})
	if err != nil {
		logger.GetLogger().Warnf("Error during cleanup walk: %v", err)
	}

	// 最后尝试删除根目录
	if err := os.Remove(mirrorDir); err != nil {
		// 如果删除失败，使用 RemoveAll（此时应该只剩空目录）
		if err := os.RemoveAll(mirrorDir); err != nil {
			return fmt.Errorf("failed to remove mirror directory %s: %w", mirrorDir, err)
		}
	}

	logger.GetLogger().Infof("Instance mirror cleaned up: %s", mirrorDir)
	return nil
}

// sortByDepthDescending 按深度降序排序（最深的在前）
func sortByDepthDescending(entries []struct {
	path  string
	depth int
	isDir bool
}) {
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].depth > entries[i].depth {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
}

// CleanupStaleMirrors 启动时清理非运行实例的残留镜像目录
func CleanupStaleMirrors() error {
	baseDir := asaserver.BaseDir
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return fmt.Errorf("failed to read base directory: %w", err)
	}

	cleaned := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasPrefix(name, mirrorDirPrefix) {
			continue
		}

		instanceName := strings.TrimPrefix(name, mirrorDirPrefix)
		if instanceName == "" {
			continue
		}

		// 检查实例是否正在运行
		running, err := asaserver.IsServerRunning(instanceName)
		if err != nil {
			logger.GetLogger().Warnf("Failed to check if instance %s is running: %v", instanceName, err)
			// 保守处理：也检查 PID
			running, _ = asaserver.IsServerRunningByPID(instanceName)
		}

		if running {
			logger.GetLogger().Infof("Instance '%s' is running, keeping mirror directory", instanceName)
			continue
		}

		logger.GetLogger().Infof("Cleaning up stale mirror for non-running instance '%s'", instanceName)
		if err := CleanupInstanceMirror(instanceName); err != nil {
			logger.GetLogger().Warnf("Failed to cleanup stale mirror for instance '%s': %v", instanceName, err)
		} else {
			cleaned++
		}
	}

	if cleaned > 0 {
		logger.GetLogger().Infof("Cleaned up %d stale mirror directories", cleaned)
	}

	return nil
}
