package asaserver

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"asa-server/logger"

	"golang.org/x/sys/windows"
	"znkr.io/diff"
)

const mirrorDirPrefix = "server-files-tmp-"

// exeFiles 是需要复制（而非链接）的可执行文件名
var exeFiles = map[string]bool{
	"ArkAscendedServer.exe": true,
	"AsaApiLoader.exe":      true,
}

// mirrorEntryType 条目类型
const (
	EntryTypeDirectory = iota
	EntryTypeSymlink
	EntryTypeFile
)

// mirrorEntry 镜像目录中的一个条目
type mirrorEntry struct {
	RelPath   string // 相对路径 (forward slash)
	EntryType int    // EntryTypeDirectory / EntryTypeSymlink / EntryTypeFile
}

var (
	elevated    bool
	elevatedErr error
	once        sync.Once

	// mirrorSyncMu 序列化所有实例的镜像同步操作，防止并发 Walk ServerFilesDir 时
	// 因 os.ReadDir 瞬态竞争导致 containsExeFiles 返回错误结果，进而创建出错误的 junction
	mirrorSyncMu sync.Mutex
)

func IsElevated() bool {
	once.Do(func() {
		var token windows.Token
		err := windows.OpenProcessToken(
			windows.CurrentProcess(),
			windows.TOKEN_QUERY,
			&token,
		)
		if err != nil {
			elevated = false
			elevatedErr = err
			return
		}
		defer token.Close()

		elevated = token.IsElevated()
	})

	return elevated
}

// InstanceMirrorDir 返回实例镜像目录路径
func InstanceMirrorDir(instanceName string) string {
	return filepath.Join(BaseDir, mirrorDirPrefix+instanceName)
}

// SyncInstanceMirror 同步实例镜像目录
// 如果镜像不存在则创建，存在则增量同步
func SyncInstanceMirror(instanceName string, cfg *InstanceConfig) (string, error) {
	mirrorSyncMu.Lock()
	defer mirrorSyncMu.Unlock()

	mirrorDir := InstanceMirrorDir(instanceName)

	// 检查实例 Config 目录是否存在且非空
	if err := validateInstanceConfig(instanceName); err != nil {
		return "", err
	}

	// 确保 Logs 和 Save 目录存在
	if err := ensureInstanceDirs(instanceName); err != nil {
		return "", err
	}

	exceptionTargets := buildExceptionTargets(instanceName, cfg)

	// 检查镜像是否已存在
	if _, err := os.Stat(mirrorDir); os.IsNotExist(err) {
		// 镜像不存在，从头创建
		return createInstanceMirror(instanceName, mirrorDir, exceptionTargets)
	}

	// 镜像已存在，增量同步
	logger.GetLogger().Infof("Syncing existing mirror for instance '%s'", instanceName)
	if err := syncMirrorEntries(mirrorDir, exceptionTargets); err != nil {
		// 同步失败，尝试重建
		logger.GetLogger().Warnf("Mirror sync failed, recreating: %v", err)
		if err := CleanupInstanceMirror(instanceName); err != nil {
			return "", fmt.Errorf("failed to cleanup mirror before recreate: %w", err)
		}
		return createInstanceMirror(instanceName, mirrorDir, exceptionTargets)
	}

	logger.GetLogger().Infof("Mirror synced successfully at %s", mirrorDir)
	return mirrorDir, nil
}

// validateInstanceConfig 验证实例 Config 目录
func validateInstanceConfig(instanceName string) error {
	instanceConfigDir := filepath.Join(InstancesDir, instanceName, "Config")
	if _, err := os.Stat(instanceConfigDir); os.IsNotExist(err) {
		return fmt.Errorf("instance config directory does not exist: %s, please create the instance first", instanceConfigDir)
	}
	entries, _ := os.ReadDir(instanceConfigDir)
	if len(entries) == 0 {
		return fmt.Errorf("instance config directory is empty: %s, please configure the instance first", instanceConfigDir)
	}
	return nil
}

// ensureInstanceDirs 确保 Logs 和 Save 目录存在
func ensureInstanceDirs(instanceName string) error {
	instanceLogsDir := filepath.Join(InstancesDir, instanceName, "Logs")
	instanceSaveDir := filepath.Join(InstancesDir, instanceName, "Save")

	for _, dir := range []string{instanceLogsDir, instanceSaveDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create instance directory %s: %w", dir, err)
		}
	}

	// 确保 Logs 目录中至少有 ShooterGame.log
	logFile := filepath.Join(instanceLogsDir, "ShooterGame.log")
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		if err := os.WriteFile(logFile, []byte(""), 0644); err != nil {
			return fmt.Errorf("failed to create log file: %w", err)
		}
	}
	return nil
}

// createInstanceMirror 从头创建实例镜像目录
func createInstanceMirror(instanceName string, mirrorDir string, exceptionTargets map[string]string) (string, error) {
	logger.GetLogger().Infof("Creating instance mirror at %s", mirrorDir)

	// Walk server-files 目录树
	err := filepath.Walk(ServerFilesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(ServerFilesDir, path)
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
		_ = CleanupInstanceMirror(instanceName)
		return "", fmt.Errorf("failed to walk server files: %w", err)
	}

	// Walk 完成后，补充缺失的 exception targets
	for relPath, target := range exceptionTargets {
		mirrorPath := filepath.Join(mirrorDir, filepath.FromSlash(relPath))
		if _, statErr := os.Stat(mirrorPath); os.IsNotExist(statErr) {
			srcPath := filepath.Join(ServerFilesDir, filepath.FromSlash(relPath))
			if mkErr := os.MkdirAll(srcPath, 0755); mkErr != nil {
				logger.GetLogger().Warnf("Failed to pre-create source dir %s: %v", srcPath, mkErr)
			}
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
func buildExceptionTargets(instanceName string, cfg *InstanceConfig) map[string]string {
	targets := map[string]string{
		"ShooterGame/Saved/Config/WindowsServer": filepath.Join(InstancesDir, instanceName, "Config"),
		"ShooterGame/Saved/Logs":                 filepath.Join(InstancesDir, instanceName, "Logs"),
	}

	// SaveDir 映射
	saveDir := cfg.SaveDir
	if saveDir == "" {
		saveDir = instanceName
	}
	targets["ShooterGame/Saved/"+saveDir] = filepath.Join(InstancesDir, instanceName, "Save")

	return targets
}

// processDirectory 处理目录条目
// 返回 (skip bool, err error) - skip=true 表示跳过该目录的子目录遍历
func processDirectory(srcPath, mirrorPath, relPath string, _ os.FileInfo, exceptionTargets map[string]string) (bool, error) {
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
func processFile(srcPath, mirrorPath, _ string, info os.FileInfo) error {
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

// createJunction 创建目录 junction
// Go 1.21+ 的 os.Symlink 在 Windows 上对目录目标自动创建 junction（无需管理员权限）
func createJunction(linkPath, targetPath string) error {
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %w", targetPath, err)
	}

	parentDir := filepath.Dir(linkPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
	}

	if err := os.Symlink(absTarget, linkPath); err != nil {
		return fmt.Errorf("failed to create junction %s -> %s: %w", linkPath, absTarget, err)
	}

	logger.GetLogger().Debugf("Created junction: %s -> %s", linkPath, absTarget)
	return nil
}

// createFileSymlink 创建文件符号链接
// 失败时一律回退到复制（跳过文件会导致游戏缺文件无法启动）
// 管理员检测仅影响日志级别
func createFileSymlink(linkPath, targetPath string) error {
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %w", targetPath, err)
	}

	parentDir := filepath.Dir(linkPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
	}

	if err := os.Symlink(absTarget, linkPath); err != nil {
		if IsElevated() {
			logger.GetLogger().Warnf("Symlink failed even with admin, fallback copy: %s: %v", linkPath, err)
		} else {
			logger.GetLogger().Debugf("No admin, fallback copy: %s", linkPath)
		}
		return copyFile(targetPath, linkPath)
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
	for i := range len(entries) {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].depth > entries[i].depth {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
}

// CleanupMirrorCache 清理指定实例的启动缓存（镜像目录）
// 对外暴露的方法，供用户手动清理
func CleanupMirrorCache(instanceName string) error {
	return CleanupInstanceMirror(instanceName)
}

// VerifyAndRepairInstanceMirror 校验镜像关键路径的完整性，不完整时自动清理并重建一次
// 返回最终有效的 mirrorDir，或错误
//
// 关键路径：ShooterGame/Binaries/Win64 必须可访问。
// 该目录在以下情况会缺失或失效：
//   - 并发创建时 containsExeFiles 因 os.ReadDir 竞态返回 false，目录被错误地建成 junction
//   - junction 目标在 ARK 更新过程中被临时移除
func VerifyAndRepairInstanceMirror(instanceName string, cfg *InstanceConfig, mirrorDir string) (string, error) {
	exeWorkDir := filepath.Join(mirrorDir, "ShooterGame/Binaries/Win64")
	if _, err := os.Stat(exeWorkDir); err == nil {
		return mirrorDir, nil
	}

	logger.GetLogger().Warnf("Mirror integrity check failed for %s, recreating mirror...", instanceName)
	_ = CleanupInstanceMirror(instanceName)

	newMirrorDir, err := SyncInstanceMirror(instanceName, cfg)
	if err != nil {
		return "", fmt.Errorf("failed to recreate instance mirror: %w", err)
	}

	exeWorkDir = filepath.Join(newMirrorDir, "ShooterGame/Binaries/Win64")
	if _, err := os.Stat(exeWorkDir); err != nil {
		return "", fmt.Errorf("mirror integrity check failed after recreate, %s not found: %w", exeWorkDir, err)
	}

	logger.GetLogger().Infof("Mirror recreated successfully for instance %s", instanceName)
	return newMirrorDir, nil
}

// ==================== 增量同步相关 ====================

// syncMirrorEntries 增量同步镜像目录
// 使用 diff 对比源目录和镜像目录，只处理差异
func syncMirrorEntries(mirrorDir string, exceptionTargets map[string]string) error {
	srcDir := ServerFilesDir

	// 收集源目录条目
	sourceEntries, err := collectSourceEntries(srcDir, exceptionTargets)
	if err != nil {
		return fmt.Errorf("failed to collect source entries: %w", err)
	}

	// 收集镜像目录条目
	mirrorEntries, err := collectMirrorEntries(mirrorDir)
	if err != nil {
		return fmt.Errorf("failed to collect mirror entries: %w", err)
	}

	logger.GetLogger().Infof("Syncing mirror: %d source entries, %d mirror entries", len(sourceEntries), len(mirrorEntries))

	// 使用 diff 对比
	edits := diff.EditsFunc(sourceEntries, mirrorEntries,
		func(a, b mirrorEntry) bool {
			return a.RelPath == b.RelPath
		},
	)

	added := 0
	removed := 0
	updated := 0

	for _, edit := range edits {
		switch edit.Op {
		case diff.Insert:
			// 源有、镜像无 → 创建
			if err := syncEntry(srcDir, mirrorDir, edit.X, exceptionTargets); err != nil {
				logger.GetLogger().Warnf("Failed to sync entry %s: %v", edit.X.RelPath, err)
			} else {
				added++
			}
		case diff.Delete:
			// 镜像有、源无 → 删除
			if err := removeMirrorEntry(mirrorDir, edit.Y); err != nil {
				logger.GetLogger().Warnf("Failed to remove mirror entry %s: %v", edit.Y.RelPath, err)
			} else {
				removed++
			}
		case diff.Match:
			// 两边都有 → 检查是否需要更新
			if err := reconcileEntry(srcDir, mirrorDir, edit.X, edit.Y, exceptionTargets); err != nil {
				logger.GetLogger().Warnf("Failed to reconcile entry %s: %v", edit.X.RelPath, err)
			} else {
				updated++
			}
		}
	}

	// 补充缺失的 exception targets
	for relPath, target := range exceptionTargets {
		mirrorPath := filepath.Join(mirrorDir, filepath.FromSlash(relPath))
		if _, statErr := os.Stat(mirrorPath); os.IsNotExist(statErr) {
			srcPath := filepath.Join(srcDir, filepath.FromSlash(relPath))
			if mkErr := os.MkdirAll(srcPath, 0755); mkErr != nil {
				logger.GetLogger().Warnf("Failed to pre-create source dir %s: %v", srcPath, mkErr)
			}
			if jErr := createJunction(mirrorPath, target); jErr != nil {
				return fmt.Errorf("failed to create exception junction for %s: %w", relPath, jErr)
			}
			logger.GetLogger().Infof("Pre-created exception junction: %s -> %s", relPath, target)
			added++
		}
	}

	logger.GetLogger().Infof("Mirror sync completed: %d added, %d removed, %d checked", added, removed, updated)
	return nil
}

// collectSourceEntries 收集源目录的所有条目
// 对 exception targets 和 exeFiles 目录做特殊处理
func collectSourceEntries(srcDir string, exceptionTargets map[string]string) ([]mirrorEntry, error) {
	var entries []mirrorEntry

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		relPath = filepath.ToSlash(relPath)

		if info.IsDir() {
			// 精确匹配 exception target → 记录为 symlink（junction），不递归
			if _, ok := exceptionTargets[relPath]; ok {
				entries = append(entries, mirrorEntry{RelPath: relPath, EntryType: EntryTypeSymlink})
				return filepath.SkipDir
			}

			// 检查是否有 exception 子路径
			hasExceptionChild := false
			for exPath := range exceptionTargets {
				if strings.HasPrefix(exPath, relPath+"/") {
					hasExceptionChild = true
					break
				}
			}

			if hasExceptionChild {
				// 需要递归的中间目录
				entries = append(entries, mirrorEntry{RelPath: relPath, EntryType: EntryTypeDirectory})
				return nil
			}

			// 检查是否包含 exe 文件
			if containsExeFiles(path) {
				entries = append(entries, mirrorEntry{RelPath: relPath, EntryType: EntryTypeDirectory})
				return nil
			}

			// 普通目录 → junction，不递归（与 collectMirrorEntries 中 junction 的 EntryTypeSymlink 保持一致）
			entries = append(entries, mirrorEntry{RelPath: relPath, EntryType: EntryTypeSymlink})
			return filepath.SkipDir
		}

		// 文件
		parentDir := filepath.Dir(path)
		parentRelPath := filepath.ToSlash(filepath.Dir(filepath.FromSlash(relPath)))
		if parentRelPath == "." {
			parentRelPath = ""
		}

		// 如果父目录是 junction（非 exception、非 exe 目录），文件不会在 mirror 中出现
		// 但 Walk 还是会访问到，我们需要跳过
		// 通过检查父目录是否会被 junction 来判断
		parentIsJunction := false
		if !isExceptionOrIntermediateDir(parentRelPath, exceptionTargets) && !containsExeFiles(parentDir) {
			parentIsJunction = true
		}
		if parentIsJunction {
			// 父目录会被 junction，文件自动包含，不需要单独记录
			return nil
		}

		// 按实际创建行为区分：exe 文件被复制（EntryTypeFile），其他文件被符号链接（EntryTypeSymlink）
		// 与 processFile / syncEntry 的实际行为保持一致，避免增量同步产生虚假的类型不匹配
		entryType := EntryTypeSymlink
		if exeFiles[filepath.Base(path)] {
			entryType = EntryTypeFile
		}
		entries = append(entries, mirrorEntry{RelPath: relPath, EntryType: entryType})
		return nil
	})

	if err != nil {
		return nil, err
	}

	// 按 RelPath 排序
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].RelPath < entries[j].RelPath
	})

	return entries, nil
}

// isExceptionOrIntermediateDir 检查路径是否是 exception target 或其中间路径
func isExceptionOrIntermediateDir(relPath string, exceptionTargets map[string]string) bool {
	if _, ok := exceptionTargets[relPath]; ok {
		return true
	}
	for exPath := range exceptionTargets {
		if strings.HasPrefix(exPath, relPath+"/") {
			return true
		}
	}
	return false
}

// collectMirrorEntries 收集镜像目录的所有条目
func collectMirrorEntries(mirrorDir string) ([]mirrorEntry, error) {
	var entries []mirrorEntry

	err := filepath.Walk(mirrorDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无法访问的条目
		}

		relPath, err := filepath.Rel(mirrorDir, path)
		if err != nil {
			return nil
		}
		if relPath == "." {
			return nil
		}

		relPath = filepath.ToSlash(relPath)

		// 判断条目类型
		if isJunctionOrSymlink(path) {
			if info.IsDir() {
				// junction 或目录符号链接
				entries = append(entries, mirrorEntry{RelPath: relPath, EntryType: EntryTypeSymlink})
				return filepath.SkipDir // 不递归进入
			}
			// 文件符号链接
			entries = append(entries, mirrorEntry{RelPath: relPath, EntryType: EntryTypeSymlink})
			return nil
		}

		if info.IsDir() {
			entries = append(entries, mirrorEntry{RelPath: relPath, EntryType: EntryTypeDirectory})
			return nil
		}

		// 真实文件
		entries = append(entries, mirrorEntry{RelPath: relPath, EntryType: EntryTypeFile})
		return nil
	})

	if err != nil {
		return nil, err
	}

	// 按 RelPath 排序
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].RelPath < entries[j].RelPath
	})

	return entries, nil
}

// syncEntry 创建单个镜像条目
func syncEntry(srcDir, mirrorDir string, entry mirrorEntry, exceptionTargets map[string]string) error {
	srcPath := filepath.Join(srcDir, filepath.FromSlash(entry.RelPath))
	mirrorPath := filepath.Join(mirrorDir, filepath.FromSlash(entry.RelPath))

	// 检查是否是 exception target
	if target, ok := exceptionTargets[entry.RelPath]; ok {
		return createJunction(mirrorPath, target)
	}

	if entry.EntryType == EntryTypeDirectory {
		// 检查是否是 exception 中间路径
		if isExceptionOrIntermediateDir(entry.RelPath, exceptionTargets) {
			return os.MkdirAll(mirrorPath, 0755)
		}
		// 检查是否包含 exe 文件
		if containsExeFiles(srcPath) {
			return os.MkdirAll(mirrorPath, 0755)
		}
		// 普通目录 → junction
		return createJunction(mirrorPath, srcPath)
	}

	// 文件
	fileName := filepath.Base(entry.RelPath)
	if exeFiles[fileName] {
		return copyFile(srcPath, mirrorPath)
	}

	return createFileSymlink(mirrorPath, srcPath)
}

// removeMirrorEntry 安全移除镜像条目
func removeMirrorEntry(mirrorDir string, entry mirrorEntry) error {
	mirrorPath := filepath.Join(mirrorDir, filepath.FromSlash(entry.RelPath))

	if isJunctionOrSymlink(mirrorPath) {
		return os.Remove(mirrorPath) // junction/symlink：只删链接本身，不删目标内容
	}

	if entry.EntryType == EntryTypeFile {
		return os.Remove(mirrorPath)
	}

	// 真实目录：游戏运行时可能在此写入文件，使用 RemoveAll 强制清除
	return os.RemoveAll(mirrorPath)
}

// reconcileEntry 检查并修复已有条目
func reconcileEntry(srcDir, mirrorDir string, srcEntry, mirrorEntryItem mirrorEntry, exceptionTargets map[string]string) error {
	srcPath := filepath.Join(srcDir, filepath.FromSlash(srcEntry.RelPath))
	mirrorPath := filepath.Join(mirrorDir, filepath.FromSlash(mirrorEntryItem.RelPath))

	// 检查类型是否匹配
	if srcEntry.EntryType != mirrorEntryItem.EntryType {
		// 特殊情形：source=Symlink（非 exe 文件的意图类型）但 mirror=File
		// 这是 createFileSymlink 无权限时 fallback 到 copyFile 的合法结果，内容正确即可
		if srcEntry.EntryType == EntryTypeSymlink && mirrorEntryItem.EntryType == EntryTypeFile {
			srcMD5, err := fileMD5(srcPath)
			if err != nil {
				return fmt.Errorf("failed to compute source MD5 for %s: %w", srcPath, err)
			}
			dstMD5, err := fileMD5(mirrorPath)
			if err != nil {
				return fmt.Errorf("failed to compute mirror MD5 for %s: %w", mirrorPath, err)
			}
			if srcMD5 != dstMD5 {
				logger.GetLogger().Infof("File content changed (fallback copy): %s, recopying", srcEntry.RelPath)
				return copyFile(srcPath, mirrorPath)
			}
			return nil
		}
		// 真正的类型变更，删除旧的重建
		logger.GetLogger().Infof("Entry type changed for %s, recreating", srcEntry.RelPath)
		_ = removeMirrorEntry(mirrorDir, mirrorEntryItem)
		return syncEntry(srcDir, mirrorDir, srcEntry, exceptionTargets)
	}

	// 对于真实文件（非 symlink），检查 MD5
	if mirrorEntryItem.EntryType == EntryTypeFile {
		if !isJunctionOrSymlink(mirrorPath) {
			// 真实文件，比较 MD5
			srcMD5, err := fileMD5(srcPath)
			if err != nil {
				return fmt.Errorf("failed to compute source MD5 for %s: %w", srcPath, err)
			}
			dstMD5, err := fileMD5(mirrorPath)
			if err != nil {
				return fmt.Errorf("failed to compute mirror MD5 for %s: %w", mirrorPath, err)
			}
			if srcMD5 != dstMD5 {
				logger.GetLogger().Infof("File changed (MD5 mismatch): %s, recopying", srcEntry.RelPath)
				return copyFile(srcPath, mirrorPath)
			}
		}
	}

	// 对于 symlink/junction，检查是否仍然有效且目标正确
	if mirrorEntryItem.EntryType == EntryTypeSymlink {
		// 检查是否是 exception target — 验证目标路径是否正确
		if target, ok := exceptionTargets[srcEntry.RelPath]; ok {
			absTarget, _ := filepath.Abs(target)
			readTarget, err := os.Readlink(mirrorPath)
			if err != nil {
				// 无法读取链接目标，重建
				logger.GetLogger().Infof("Cannot read symlink target: %s, recreating", mirrorEntryItem.RelPath)
				_ = os.Remove(mirrorPath)
				return syncEntry(srcDir, mirrorDir, srcEntry, exceptionTargets)
			}
			absReadTarget, _ := filepath.Abs(readTarget)
			if absReadTarget != absTarget {
				// 目标不正确，重建
				logger.GetLogger().Infof("Symlink target mismatch for %s: got %s, want %s, recreating",
					mirrorEntryItem.RelPath, absReadTarget, absTarget)
				_ = os.Remove(mirrorPath)
				return syncEntry(srcDir, mirrorDir, srcEntry, exceptionTargets)
			}
			return nil
		}

		// 非 exception symlink，检查是否仍然有效
		if _, err := os.Stat(mirrorPath); err != nil {
			// 链接失效，重新创建
			logger.GetLogger().Infof("Broken symlink detected: %s, recreating", mirrorEntryItem.RelPath)
			_ = os.Remove(mirrorPath)
			return syncEntry(srcDir, mirrorDir, srcEntry, exceptionTargets)
		}
	}

	return nil
}

// fileMD5 计算文件的 MD5 哈希
func fileMD5(path string) ([md5.Size]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [md5.Size]byte{}, err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return [md5.Size]byte{}, err
	}

	var result [md5.Size]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}

// isRealFile 检查路径是否是真实文件（非 symlink/junction）
func isRealFile(path string) bool {
	if isJunctionOrSymlink(path) {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
