package instance

import (
	"errors"
	"os"
	"path/filepath"
	"slices"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/mirror"
	"asa-server/internal/plugindata"
	procpkg "asa-server/internal/process"
	statepkg "asa-server/internal/state"
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
		err := plugindata.MigrateInstance(name, mirror.InstanceMirrorDir(name), func() bool {
			return instanceActiveForMigration(name)
		})
		switch {
		case errors.Is(err, plugindata.ErrInstanceRunning):
			if !plugindata.IsMigrated(name) {
				pending++
				logger.Infof("实例 %s 正在运行或启动，ArkApi 插件目录迁移推迟到它下一次启动", name)
			}
		case err != nil:
			pending++
			logger.Errorf("迁移实例 %s 的 ArkApi 插件目录失败（原有数据未改动，启动时会重试）: %v", name, err)
		}
	}

	if pending == 0 {
		plugindata.RetireLegacyServerPlugins()
	}
}

// activeStatuses 是「实例可能有进程、或马上要有进程」的状态。
var activeStatuses = []statepkg.InstanceStatus{
	statepkg.StatusStartStartInitialization,
	statepkg.StatusStartStartInitializationSuccessful,
	statepkg.StatusStarting,
	statepkg.StatusStarted,
	statepkg.StatusStopping,
	statepkg.StatusRestarting,
	statepkg.StatusRestarted,
}

// instanceActiveForMigration 判断能不能动这个实例的插件数据：进程存活（端口在监听，
// 或保存的游戏 PID 仍属于它），**或**状态说它正在启动 / 运行 / 停止中。
//
// 曾经只看端口——而端口要到游戏完全起来才绑定，正在启动的实例、以及 asa-server
// 重启时仍在运行但还没绑端口的实例都会被当成已停止，活库被拿去收割
// （docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §7.2 P1-20）。
//
// 程序启动时读到的状态可能是崩溃前遗留的（例如停在 started），把它也算作活跃只会让
// 迁移推迟到该实例下一次启动——那条路径在实例锁下、实例必然已停止时迁移，是安全的方向。
func instanceActiveForMigration(name string) bool {
	if procpkg.IsInstanceProcessAlive(name) {
		return true
	}
	st, err := statepkg.GetLatestInstanceState(name)
	return err == nil && slices.Contains(activeStatuses, st.Status)
}
