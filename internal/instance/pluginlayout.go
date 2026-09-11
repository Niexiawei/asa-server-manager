package instance

import (
	"os"
	"path/filepath"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/mirror"
	"asa-server/internal/plugindata"
	procpkg "asa-server/internal/process"
	"asa-server/pkg/logger"
)

// MigratePluginLayouts 在程序启动时把所有**未运行**的实例迁移到每实例插件目录
// （docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §4.4），全部迁完后退役 server-files 里那份全局插件。
//
// 运行中的实例绝不迁移：它的活数据还在镜像的真实 Plugins 目录里，运行中复制 SQLite
// 会复制出互相撕裂的文件组。它们留给自己下一次 StartServer 迁移（那时必然已停止），
// 全局插件也因此要留到它们迁完才能退役——没迁的实例迁移时还要从那里拷插件。
func MigratePluginLayouts() {
	names, err := cfgpkg.GetAvailableInstances()
	if err != nil {
		logger.Warnf("列出实例失败，跳过插件目录迁移（各实例启动时仍会迁移）: %v", err)
		return
	}

	pending := 0
	for _, name := range names {
		// GetAvailableInstances 列的是实例目录下的所有子目录，没有实例配置的不是实例
		if _, err := os.Stat(filepath.Join(cfgpkg.InstancesDir, name, "instance_config.ini")); err != nil {
			continue
		}
		if running, _ := procpkg.IsServerRunning(name); running {
			if !plugindata.IsMigrated(name) {
				pending++
				logger.Infof("实例 %s 正在运行，ArkApi 插件目录迁移推迟到它下一次启动", name)
			}
			continue
		}
		if err := plugindata.MigrateInstance(name, mirror.InstanceMirrorDir(name)); err != nil {
			pending++
			logger.Errorf("迁移实例 %s 的 ArkApi 插件目录失败（原有数据未改动，启动时会重试）: %v", name, err)
		}
	}

	if pending == 0 {
		plugindata.RetireLegacyServerPlugins()
	}
}
