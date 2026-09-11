package plugindata

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/logger"
)

// 按实例启用 / 禁用插件（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §4.5）。
//
// 实例配置里的 DisabledArkApiPlugins 是唯一的真相，插件目录的位置由它推导：
// 启用的放在 ArkApi/Plugins（镜像 junction 的目标，ArkApi 加载这里），禁用的放在
// ArkApi/PluginsDisabled（不在 junction 之下，ArkApi 看不到，快照也自然跳过）。
// 实例停止时立刻落位；运行中只写配置，由下一次 StartServer 在同步镜像之前落位——
// 运行中的 ArkApi 占着 dll，目录挪不动。

// InstanceDisabledPluginsDir 返回实例的禁用插件目录。
func InstanceDisabledPluginsDir(instanceName string) string {
	return filepath.Join(InstanceArkApiDir(instanceName), instanceDisabledDirName)
}

// InstanceBackupsDir 返回实例的插件备份目录：被更新或卸载替换下来的插件目录放在这里。
func InstanceBackupsDir(instanceName string) string {
	return filepath.Join(InstanceArkApiDir(instanceName), instanceBackupsDirName)
}

// TryLockInstance 尝试拿实例级锁。拿不到说明实例正在启动（PrepareForStart 持锁），
// 或者另一个插件操作正在进行。成功时必须调用返回的 unlock。
func TryLockInstance(instanceName string) (unlock func(), ok bool) {
	mu := instanceLock(instanceName)
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

// PrepareForStart 是 StartServer 在同步镜像前后的插件目录准备，全程持实例级锁：
//
//	迁移（已迁移时只 stat 一下标记） → 按禁用列表落位 → syncMirror（同步并校验镜像）
//
// 持锁覆盖到镜像同步结束，是为了让安装、卸载与启动互斥：同步到一半时有人往
// Plugins 里换目录，镜像 junction 那头看到的是半成品。
//
// 迁移或落位失败都中止启动：带着没迁完的目录去同步，镜像里的旧内容会被粗暴合并进去；
// 该禁用的插件挪不出去就启动，等于加载了用户明确禁用的插件。syncMirror 的错误原样返回。
func PrepareForStart(instanceName, mirrorDir string, disabled []string, syncMirror func() error) error {
	mu := instanceLock(instanceName)
	mu.Lock()
	defer mu.Unlock()

	if err := migrateInstance(instanceName, mirrorDir); err != nil {
		return fmt.Errorf("迁移实例 %s 的 ArkApi 插件目录失败，已中止启动（原有数据未改动）: %w", instanceName, err)
	}
	if err := ReconcileLocked(instanceName, disabled); err != nil {
		return fmt.Errorf("按启用/禁用设置整理实例 %s 的插件目录失败，已中止启动: %w", instanceName, err)
	}
	return syncMirror()
}

// DisabledPlugins 读实例配置里的禁用列表。读不到配置时返回 nil（全部视为启用）。
func DisabledPlugins(instanceName string) []string {
	cfg, err := cfgpkg.LoadInstanceConfig(instanceName)
	if err != nil {
		return nil
	}
	return cfg.DisabledArkApiPlugins
}

// FindInstancePlugin 找到插件 plugin 在实例里的目录：在 Plugins 下 enabled=true，
// 在 PluginsDisabled 下 enabled=false，都没有时 ok=false。只看已迁移布局。
//
// 这里的 enabled 说的是**目录位置**，不是配置：运行中改过开关的实例，两者可能暂时不一致。
func FindInstancePlugin(instanceName, plugin string) (dir string, enabled, ok bool) {
	if p := filepath.Join(InstancePluginsDir(instanceName), plugin); isDir(p) {
		return p, true, true
	}
	if p := filepath.Join(InstanceDisabledPluginsDir(instanceName), plugin); isDir(p) {
		return p, false, true
	}
	return "", false, false
}

// ReconcileLocked 按禁用列表把插件目录挪到该在的位置。调用方持有实例级锁，并保证实例已停止。
//
// 尚未迁移的实例什么都不做：它的插件还是全局的，迁移之后由下一次调用落位。
// 某个插件在两处都有同名目录时不猜哪份是对的，报错交给用户处理。
// 单个插件挪失败不影响其他插件，全部错误合并返回。
func ReconcileLocked(instanceName string, disabled []string) error {
	if !IsMigrated(instanceName) {
		return nil
	}
	enabledRoot := InstancePluginsDir(instanceName)
	disabledRoot := InstanceDisabledPluginsDir(instanceName)

	var errs []error
	for _, p := range subdirNames(enabledRoot) {
		if slices.Contains(disabled, p) {
			errs = append(errs, movePluginDir(p, enabledRoot, disabledRoot))
		}
	}
	for _, p := range subdirNames(disabledRoot) {
		if !slices.Contains(disabled, p) {
			errs = append(errs, movePluginDir(p, disabledRoot, enabledRoot))
		}
	}
	return errors.Join(errs...)
}

func movePluginDir(plugin, fromRoot, toRoot string) error {
	src, dst := filepath.Join(fromRoot, plugin), filepath.Join(toRoot, plugin)
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("插件 %s 在 %s 和 %s 下都有，无法判断以哪份为准，请手工删除其中一份", plugin, fromRoot, toRoot)
	}
	if err := os.MkdirAll(toRoot, 0755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("移动插件 %s 失败（文件可能被占用）: %w", plugin, err)
	}
	logger.Infof("插件目录已落位: %s -> %s", src, dst)
	return nil
}

// subdirNames 列出 dir 下的子目录名；dir 不存在时返回 nil。
func subdirNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}
