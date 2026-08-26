// Package plugindata 把 ArkApi 插件的**配置与运行期数据**从共享的服务端目录里
// 隔离出来，落到每个实例自己的目录下。
//
// 要解决的问题：ArkApi 插件把运行期数据和插件二进制放在同一个目录里
// （典型是 Permissions 插件的 ArkDB.db，存的是玩家在本服的权限组）。
// 那个目录位于 server-files 之下，被镜像同步当成普通文件对待 ——
// 实例运行期写进去的数据与源版本 MD5 不同，下次同步就被源版本覆盖回去，
// 表现为「每次重启权限被重置」。而且镜像目录是临时的，随清理一起消失。
//
// 机制：**启停搬运**。启动前把实例目录里的配置与数据注入镜像，停止后回收回来。
// 不用链接：Windows 上文件符号链接需要 SeCreateSymbolicLinkPrivilege
// （提权逻辑已随镜像去管理员化删除），junction 只能链目录，
// 硬链接则会因为 SQLite 的 -wal/-shm 被动态删除重建而失效。
//
// 搬运有一个崩溃窗口（进程非正常退出时回收不执行），靠两处兜底：
// Rescue 的「镜像侧更新则以镜像侧为准」规则，以及运行期的在线快照（snapshot.go）。
//
// 本包**不依赖 mirror**：镜像目录一律由调用方以参数传入，
// 这样 mirror 可以反过来依赖本包，在销毁镜像前先做一次抢救性回收。
//
// 设计与取舍详见 docs/ARKAPI_PLUGIN_DATA_PLAN.md。
package plugindata

import (
	"os"
	"path/filepath"
	"strings"

	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/fsutil"
	"asa-server/pkg/logger"
)

const (
	// pluginsRelPath 是 ArkApi 插件目录在服务端文件树里的相对位置。
	pluginsRelPath = "ShooterGame/Binaries/Win64/ArkApi/Plugins"

	configFileName   = "config.json"
	configBackupName = "config.json.bak"
	snapshotsDirName = "snapshots"

	// instancePluginsDirName 是实例目录下存放插件配置与数据的子目录。
	instancePluginsDirName = "plugins"
)

// InstancePluginsDir 返回实例的插件数据根目录：{BaseDir}/instances/{name}/plugins
func InstancePluginsDir(instanceName string) string {
	return filepath.Join(cfgpkg.InstancesDir, instanceName, instancePluginsDirName)
}

// MirrorPluginsDir 返回镜像里的 ArkApi 插件目录。
func MirrorPluginsDir(mirrorDir string) string {
	return filepath.Join(mirrorDir, filepath.FromSlash(pluginsRelPath))
}

// listMirrorPlugins 列出镜像里实际存在的插件目录名。
// 以镜像为准而不是以实例目录为准：插件被卸载后，实例目录里的残留数据不该再被注入。
func listMirrorPlugins(mirrorDir string) []string {
	entries, err := os.ReadDir(MirrorPluginsDir(mirrorDir))
	if err != nil {
		warnIfPluginsPathCaseMismatch(mirrorDir) // 见 docs/LINUX_COMPATIBILITY_PLAN.md §5.12 表格第 1 条
		return nil                               // ArkApi 未安装，或镜像还没建起来
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// Rescue 在**覆盖或销毁镜像里的插件文件之前**做一次抢救性回收。
//
// 它是整个方案的成败所在：正常停止走 Reclaim，但 ARK 进程崩溃、机器断电、
// 管理器被杀、同步失败触发镜像重建等路径都不会走到 Reclaim。
// 没有这一步，那些场景下的数据会被下一次注入静默覆盖掉 —— 不报任何错，最难排查。
//
// 判定规则：镜像侧存在，且（实例侧不存在 || 镜像侧组内最新 mtime 更新）→ 以镜像侧为准。
// **绝不能无条件反向覆盖**，那正好会用陈旧的实例副本盖掉崩溃前的新数据。
//
// 全程 best-effort：任何一步失败只记日志，不阻断启动或清理。
func Rescue(instanceName, mirrorDir string) {
	harvest(instanceName, mirrorDir, false)
}

// Reclaim 在实例进程**完全退出之后**把镜像里的插件配置与数据收回实例目录。
//
// 必须等进程真的退出：运行中拷 SQLite 的文件组会拷出互相撕裂的组合，
// 得到的是损坏的副本。运行期要取数据只能走 snapshot.go 的在线快照。
func Reclaim(instanceName, mirrorDir string) {
	harvest(instanceName, mirrorDir, true)
}

// harvest 把镜像侧的插件文件收回实例侧。force=true 表示无条件收回（正常停止后），
// false 表示只在镜像侧更新时收回（抢救）。
func harvest(instanceName, mirrorDir string, force bool) {
	for _, plugin := range listMirrorPlugins(mirrorDir) {
		mirrorPlugin := filepath.Join(MirrorPluginsDir(mirrorDir), plugin)
		instPlugin := filepath.Join(InstancePluginsDir(instanceName), plugin)

		external, overridePath := hasExternalDBPath(instPlugin, mirrorPlugin)

		for _, g := range scanPluginDir(mirrorPlugin, plugin) {
			if g.IsConfig {
				mergeConfigInto(mirrorPlugin, instPlugin, g.Base, force)
				continue
			}
			if external {
				logger.Debugf(
					"插件 %s 的数据库路径已由用户接管（%s），跳过回收", plugin, overridePath)
				continue
			}
			if !force && !mirrorGroupIsNewer(mirrorPlugin, instPlugin, g) {
				continue
			}
			if err := replaceGroup(mirrorPlugin, instPlugin, g); err != nil {
				logger.Warnf("回收插件 %s 的文件组 %s 失败: %v", plugin, g.Base, err)
				continue
			}
			logger.Infof("已回收插件数据: %s/%s -> 实例 %s", plugin, g.Base, instanceName)
		}
	}
}

// mirrorGroupIsNewer 判断镜像侧的文件组是否比实例侧新（或实例侧压根不存在）。
func mirrorGroupIsNewer(mirrorPlugin, instPlugin string, g fileGroup) bool {
	mirrorT, mirrorOK := maxMTime(mirrorPlugin, g.Members)
	if !mirrorOK {
		return false
	}
	instT, instOK := maxMTime(instPlugin, g.allMemberPaths())
	if !instOK {
		return true // 实例侧没有 → 首次播种
	}
	return mirrorT.After(instT)
}

// Inject 在启动前把实例目录里的插件配置与数据注入镜像。
//
// 必须排在 SyncInstanceMirror / VerifyAndRepairInstanceMirror **之后**：
// 放在之前会被同步的 MD5 回写覆盖掉。调用方还应先跑一次 Rescue，
// 让上一轮崩溃遗留在镜像里的新数据先回到实例侧，否则这里会用旧副本盖掉它。
func Inject(instanceName, mirrorDir string) {
	for _, plugin := range listMirrorPlugins(mirrorDir) {
		mirrorPlugin := filepath.Join(MirrorPluginsDir(mirrorDir), plugin)
		instPlugin := filepath.Join(InstancePluginsDir(instanceName), plugin)

		if _, err := os.Stat(instPlugin); err != nil {
			// 实例侧还没有这个插件的数据：以镜像（即源服务端自带的那一份）为初值播种。
			// 正常路径上 Rescue 已经做过，这里是单独调用 Inject 时的兜底。
			harvestPlugin(mirrorPlugin, instPlugin, plugin)
		}

		external, overridePath := hasExternalDBPath(instPlugin, mirrorPlugin)
		if external {
			logger.Warnf(
				"插件 %s 的数据库路径已由用户设为 %s，管理器不再为其做隔离、回收与快照",
				plugin, overridePath)
		}

		for _, g := range scanPluginDir(instPlugin, plugin) {
			if !g.IsConfig && external {
				continue
			}
			if err := replaceGroup(instPlugin, mirrorPlugin, g); err != nil {
				logger.Warnf("注入插件 %s 的文件组 %s 失败: %v", plugin, g.Base, err)
				continue
			}
			logger.Debugf("已注入插件数据: 实例 %s -> %s/%s", instanceName, plugin, g.Base)
		}
	}
}

// harvestPlugin 无条件把单个插件的镜像侧文件收到实例侧，用于首次播种。
func harvestPlugin(mirrorPlugin, instPlugin, plugin string) {
	for _, g := range scanPluginDir(mirrorPlugin, plugin) {
		if g.IsConfig {
			mergeConfigInto(mirrorPlugin, instPlugin, g.Base, true)
			continue
		}
		if err := replaceGroup(mirrorPlugin, instPlugin, g); err != nil {
			logger.Warnf("播种插件 %s 的文件组 %s 失败: %v", plugin, g.Base, err)
		}
	}
}

// replaceGroup 把一个文件组从 srcDir **整组替换**到 dstDir。
//
// 「整组替换」不是「逐文件覆盖」：先把目标侧该组占用的全部路径删干净再拷。
// 否则会出现「新的主库 + 残留的旧 -wal」这种互不匹配的组合，
// SQLite 打开时会拿旧 WAL 去重放，结果比不搬还糟。
func replaceGroup(srcDir, dstDir string, g fileGroup) error {
	for _, rel := range g.allMemberPaths() {
		p := filepath.Join(dstDir, filepath.FromSlash(rel))
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for _, rel := range g.Members {
		src := filepath.Join(srcDir, filepath.FromSlash(rel))
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(dstDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err := fsutil.CopyFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// mergeConfigInto 把镜像侧的 config.json 合并进实例侧。
//
// 插件在运行期（尤其是版本更新后）会往 config.json 里写入新增项，所以配置必须双向。
// 但整体覆盖会踩另一个坑：用户可能刚在管理器里改过实例侧配置，
// 镜像侧此时是「旧值 + 插件新增项」，整体拷回会把用户的改动冲掉。
// 所以走 §4.6 的按键合并：实例侧值恒优先，镜像侧新增键并入，原有顺序保持不变。
func mergeConfigInto(mirrorPlugin, instPlugin, rel string, force bool) {
	mirrorPath := filepath.Join(mirrorPlugin, filepath.FromSlash(rel))
	instPath := filepath.Join(instPlugin, filepath.FromSlash(rel))

	mirrorData, err := os.ReadFile(mirrorPath)
	if err != nil {
		return
	}

	instData, err := os.ReadFile(instPath)
	if err != nil {
		// 实例侧还没有 → 首次播种，原样落地
		if err := os.MkdirAll(filepath.Dir(instPath), 0755); err != nil {
			logger.Warnf("创建实例插件目录 %s 失败: %v", filepath.Dir(instPath), err)
			return
		}
		if err := os.WriteFile(instPath, mirrorData, 0644); err != nil {
			logger.Warnf("播种插件配置 %s 失败: %v", instPath, err)
		}
		return
	}

	if !force {
		mirrorT, ok := maxMTime(mirrorPlugin, []string{rel})
		instT, _ := maxMTime(instPlugin, []string{rel})
		if !ok || !mirrorT.After(instT) {
			return
		}
	}

	merged, err := MergeConfigJSON(instData, mirrorData)
	if err != nil {
		// 有一侧不是合法 JSON 对象。宁可什么都不做也不能猜：
		// 实例侧那份是用户改过的，覆盖掉就找不回来了。
		logger.Warnf("合并插件配置 %s 失败，保留实例侧原文: %v", instPath, err)
		return
	}

	// 合并前把镜像侧原文留一份，出问题能回溯
	bakPath := filepath.Join(instPlugin, configBackupName)
	if err := os.WriteFile(bakPath, mirrorData, 0644); err != nil {
		logger.Debugf("写配置备份 %s 失败: %v", bakPath, err)
	}

	if err := writeFileAtomic(instPath, merged); err != nil {
		logger.Warnf("写回合并后的插件配置 %s 失败: %v", instPath, err)
	}
}

// writeFileAtomic 先写临时文件再重命名，避免写到一半被打断留下截断的配置。
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// IsProtectedRelPath 判断镜像里的一个相对路径（forward slash）是否属于
// 「插件配置 / 运行期数据」，需要被排除出增量同步的**回写与删除**。
//
// 这是注入能生效的前提，不是可选项：
//   - 不排除删除：实例的 ArkDB.db-wal 在源目录里不存在，会被同步当成多余条目删掉，
//     而这恰好发生在 Rescue 之前 —— 崩溃现场就此消失。
//   - 不排除回写：源版本的主库会被拷进镜像，与镜像里保留的旧 -wal 拼成互不匹配的组合。
//
// SQLite 走文件头识别，所以需要 mirrorDir 才能定位到实际文件。
func IsProtectedRelPath(mirrorDir, relPath string) bool {
	if !strings.HasPrefix(relPath, pluginsRelPath+"/") {
		return false
	}
	rest := strings.TrimPrefix(relPath, pluginsRelPath+"/")
	// 至少要有「插件名/文件名」两段，插件目录本身不算
	if !strings.Contains(rest, "/") {
		return false
	}

	if slashBase(rest) == configFileName {
		return true
	}
	if looksLikeDataFile(slashBase(rest)) {
		return true
	}
	// 扩展名认不出来时按文件头兜底：主库看自己，伴随文件看主库
	if isSQLiteFile(filepath.Join(mirrorDir, filepath.FromSlash(relPath))) {
		return true
	}
	if base := groupKey(relPath); base != relPath {
		return isSQLiteFile(filepath.Join(mirrorDir, filepath.FromSlash(base)))
	}
	return false
}

// slashBase 取 forward slash 路径的最后一段。
// 不用 filepath.Base：它在 Windows 上认的是反斜杠，会把整条路径当成单个文件名。
func slashBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
