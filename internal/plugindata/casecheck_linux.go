//go:build linux

package plugindata

import (
	"path/filepath"
	"sync"

	"asa-server/pkg/logger"
)

// warnedCaseMismatchDirs 记录已经报过一次的 mirrorDir，避免同一份镜像在
// 每次 listMirrorPlugins 调用（启动/停止/快照 tick）时都重复刷同一条警告。
var warnedCaseMismatchDirs sync.Map

// warnIfPluginsPathCaseMismatch 在「按 pluginsRelPath 精确大小写找不到插件目录」时，
// 用 detectPluginsCaseMismatch 大小写不敏感地找一遍：找到了就说明这不是「用户没装
// ArkApi」，而是 SteamCMD/ArkApi 落盘的大小写与硬编码常量对不上——这正是
// docs/LINUX_COMPATIBILITY_PLAN.md §5.12 表格第 1 条要核对的坑。
//
// 找不到发行版真实大小写之前没法把这个常量变成动态的（改动风险高于收益，见该节讨论），
// 所以这里只做只读诊断：日志报出实际大小写，不改变匹配/返回值语义。
func warnIfPluginsPathCaseMismatch(mirrorDir string) {
	win64Dir := win64DirFromMirror(mirrorDir)
	actualName, ok := detectPluginsCaseMismatch(win64Dir)
	if !ok {
		return
	}
	if _, already := warnedCaseMismatchDirs.LoadOrStore(mirrorDir, struct{}{}); already {
		return
	}
	logger.Warnf(
		"检测到 ArkApi 插件目录大小写与预期不符：磁盘上是 %q，程序按 %q 匹配，"+
			"插件配置/数据隔离不会生效",
		filepath.Join(win64Dir, actualName), filepath.Join(win64Dir, "ArkApi"))
}
