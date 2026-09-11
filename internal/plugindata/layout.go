package plugindata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/fsutil"
	"asa-server/pkg/logger"
)

// 每实例插件目录（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §4）。
//
// 插件的一切（dll、pdb、PluginInfo.json、config.json、运行期数据）都放在实例目录下，
// 镜像里的 Win64/ArkApi/Plugins 是指向它的 junction（Linux 上是 symlink）：
//
//	instances/{name}/ArkApi/
//	├── Plugins/          ← junction 目标，ArkApi 实际加载这里
//	├── PluginsDisabled/  ← 被禁用的插件（不在 junction 之下，ArkApi 看不到，见 enable.go）
//	├── PluginSnapshots/  ← SQLite 在线快照
//	├── Backups/          ← 被更新或卸载替换下来的插件目录
//	└── .plugin-layout    ← 迁移标记
//
// 插件直接读写实例目录，不再需要启停搬运（plugindata.go），也就没有崩溃窗口。
// 从旧布局（全局插件 + instances/{name}/plugins/ 里的配置与数据）过来要走一次 MigrateInstance。

const (
	instanceArkApiDirName    = "ArkApi"
	instancePluginsDirName   = "Plugins"
	instanceDisabledDirName  = "PluginsDisabled"
	instanceSnapshotsDirName = "PluginSnapshots"
	instanceBackupsDirName   = "Backups"
	migratingDirName         = "Plugins.migrating"

	layoutMarkerName    = ".plugin-layout"
	layoutMarkerContent = "per-instance-v1\n"

	legacyRetiredPrefix = legacyPluginsDirName + ".legacy-"
)

// 布局取值，供 API 告诉前端这个实例处在哪种布局。
const (
	LayoutInstance = "instance"
	LayoutLegacy   = "legacy"
)

// instanceLocks 串行化同一个实例上的插件目录变更：迁移、写配置、启用/禁用落位、安装/更新/卸载。
// StartServer 在「迁移 → 落位 → 同步镜像」期间持有它（PrepareForStart），插件操作用
// TryLockInstance，拿不到就报「实例正在启动」（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §4.6）。
var instanceLocks sync.Map

func instanceLock(instanceName string) *sync.Mutex {
	v, _ := instanceLocks.LoadOrStore(instanceName, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// InstanceArkApiDir 返回 {BaseDir}/instances/{name}/ArkApi。
func InstanceArkApiDir(instanceName string) string {
	return filepath.Join(cfgpkg.InstancesDir, instanceName, instanceArkApiDirName)
}

// InstancePluginsDir 返回实例的插件目录，也就是镜像里 ArkApi/Plugins 那条 junction 的目标。
func InstancePluginsDir(instanceName string) string {
	return filepath.Join(InstanceArkApiDir(instanceName), instancePluginsDirName)
}

// InstanceSnapshotsDir 返回实例的插件数据库快照目录（每个插件一个子目录）。
func InstanceSnapshotsDir(instanceName string) string {
	return filepath.Join(InstanceArkApiDir(instanceName), instanceSnapshotsDirName)
}

// LayoutMarkerPath 返回迁移标记文件的路径。
func LayoutMarkerPath(instanceName string) string {
	return filepath.Join(InstanceArkApiDir(instanceName), layoutMarkerName)
}

// IsMigrated 报告实例是否已采用每实例插件目录。
//
// 判据是标记文件，**不是** Plugins 目录存在与否：镜像同步会先把 junction 的目标目录
// 建出来，拿目录当判据会让迁移被永久跳过，旧数据再也迁不进来。
func IsMigrated(instanceName string) bool {
	_, err := os.Stat(LayoutMarkerPath(instanceName))
	return err == nil
}

// LayoutOf 返回实例当前的插件布局：LayoutInstance 或 LayoutLegacy。
func LayoutOf(instanceName string) string {
	if IsMigrated(instanceName) {
		return LayoutInstance
	}
	return LayoutLegacy
}

// InitInstanceLayout 为**新建**的实例直接建出空的插件目录并写上标记，
// 于是它永远不会走迁移——新实例默认没有插件（方案 D6）。
//
// 实例目录里已经有旧布局数据时什么都不做，留给迁移处理：把它标记成已迁移
// 等于丢掉那些数据。
func InitInstanceLayout(instanceName string) error {
	mu := instanceLock(instanceName)
	mu.Lock()
	defer mu.Unlock()

	if IsMigrated(instanceName) || isDir(legacyPluginsDir(instanceName)) {
		return nil
	}
	if err := os.MkdirAll(InstancePluginsDir(instanceName), 0755); err != nil {
		return err
	}
	return writeFileAtomic(LayoutMarkerPath(instanceName), []byte(layoutMarkerContent))
}

// MigrateInstance 把一个实例从旧布局迁移到每实例插件目录（方案 §4.4）。已迁移时只补做收尾。
//
// **调用方必须保证实例已停止**：运行中实例的活数据在镜像的真实 Plugins 目录里，
// 运行中复制 SQLite 会复制出互相撕裂的文件组。
//
// 迁移后每个实例加载的插件与数据和迁移前完全一致（方案 D5）：server-files 里的全局插件
// 各拷一份，再用该实例自己隔离出来的配置与数据整组覆盖。
//
// 全程幂等、可在任意一步中断：组装在 Plugins.migrating 里进行，rename 到位是提交点，
// 之后才写标记、才动旧目录；旧目录与 server-files 都只改名不删除。
func MigrateInstance(instanceName, mirrorDir string) error {
	mu := instanceLock(instanceName)
	mu.Lock()
	defer mu.Unlock()
	return migrateInstance(instanceName, mirrorDir)
}

// migrateInstance 是 MigrateInstance 的本体，调用方持有实例级锁。
func migrateInstance(instanceName, mirrorDir string) error {
	if IsMigrated(instanceName) {
		retireLegacyInstanceDir(instanceName) // 上一次在「写标记」与「旧目录改名」之间中断
		return nil
	}

	staging := filepath.Join(InstanceArkApiDir(instanceName), migratingDirName)
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("清理上一次中断留下的 %s 失败: %w", staging, err)
	}
	if err := os.MkdirAll(staging, 0755); err != nil {
		return fmt.Errorf("创建 %s 失败: %w", staging, err)
	}

	// 第 1 步：上一轮若是崩溃退出，镜像里留着的才是最新数据，先按既有规则抢救回旧目录。
	// 判断「是真实目录」必须先排除链接：os.Lstat 对 junction 报 IsDir()==false。
	if mirrorDir != "" {
		if mp := MirrorPluginsDir(mirrorDir); !fsutil.IsLink(mp) && isDir(mp) {
			harvest(instanceName, mirrorDir, false)
		}
	}

	// 第 2 步：组装。
	legacy := legacyPluginsDir(instanceName)
	srcRoot := SourcePluginsDir()
	entries, err := os.ReadDir(srcRoot)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取 %s 失败: %w", srcRoot, err)
	}
	migrated := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		plugin := e.Name()
		dst := filepath.Join(staging, plugin)
		if err := fsutil.CopyDir(filepath.Join(srcRoot, plugin), dst); err != nil {
			return fmt.Errorf("复制插件 %s 失败: %w", plugin, err)
		}
		legacyPlugin := filepath.Join(legacy, plugin)
		if isDir(legacyPlugin) {
			if err := overlayLegacyPlugin(legacyPlugin, dst, plugin); err != nil {
				return err
			}
			if snaps := filepath.Join(legacyPlugin, snapshotsDirName); isDir(snaps) {
				if err := fsutil.CopyDir(snaps, filepath.Join(InstanceSnapshotsDir(instanceName), plugin)); err != nil {
					logger.Warnf("迁移插件 %s 的快照失败（不影响迁移）: %v", plugin, err)
				}
			}
		}
		finalDir := filepath.Join(InstancePluginsDir(instanceName), plugin)
		if err := rewriteLegacyDbPathOverride(filepath.Join(dst, configFileName), legacyPlugin, finalDir); err != nil {
			return fmt.Errorf("改写插件 %s 的 DbPathOverride 失败: %w", plugin, err)
		}
		migrated[plugin] = true
	}
	if legacyEntries, err := os.ReadDir(legacy); err == nil {
		for _, e := range legacyEntries {
			if e.IsDir() && !migrated[e.Name()] {
				logger.Infof("插件 %s 只在实例 %s 的旧数据目录里有（server-files 里已卸载），不迁移，数据随旧目录保留",
					e.Name(), instanceName)
			}
		}
	}

	// 第 3 步：提交。rename 到位之后才写标记——中断在两者之间时下次会整轮重来，
	// 那时已到位的目录会被 moveAsideIfPresent 挪开保留，不会丢。
	target := InstancePluginsDir(instanceName)
	if err := moveAsideIfPresent(target); err != nil {
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		return fmt.Errorf("迁移结果落位失败: %w", err)
	}
	if err := writeFileAtomic(LayoutMarkerPath(instanceName), []byte(layoutMarkerContent)); err != nil {
		return fmt.Errorf("写迁移标记失败: %w", err)
	}
	logger.Infof("实例 %s 的 ArkApi 插件已迁移到独立目录 %s（%d 个插件）", instanceName, target, len(migrated))

	// 第 4 步：旧目录改名保留。标记已经写好，搬运已被 shuttleRetired 关断，不会再有人写它。
	retireLegacyInstanceDir(instanceName)
	return nil
}

// overlayLegacyPlugin 用旧目录里该实例自己的配置与数据，**整组**覆盖从 server-files 拷来的那一份。
//
// 必须整组：server-files 里可能带着种子库（主库 + -shm），逐文件覆盖会拼出
// 「实例的主库 + 种子的 -shm」这种互不匹配的组合。replaceGroup 先删目标侧该组的全部路径再拷。
func overlayLegacyPlugin(legacyPlugin, dst, plugin string) error {
	for _, g := range scanPluginDir(legacyPlugin, plugin) {
		if err := replaceGroup(legacyPlugin, dst, g); err != nil {
			return fmt.Errorf("迁移插件 %s 的 %s 失败: %w", plugin, g.Base, err)
		}
	}
	return nil
}

// rewriteLegacyDbPathOverride 改写指向旧数据目录的 DbPathOverride（方案 §4.4 第 2 步第 5 项）。
//
// 旧方案（ARKAPI_PLUGIN_DATA_PLAN.md §4.8）把「指向实例插件目录内」当作等价形态，还推荐它作逃生路径，
// 所以可能有配置指着 instances/{name}/plugins/X。旧目录改名后这个路径就悬空了，
// 插件会在原路径新建一个空库——权限静默清零。改写规则：
//
//	指向旧 plugins/X 本身 → 清空（默认位置就是插件目录，也就是新的实例插件目录）
//	指向它的子目录       → 前缀改写到新目录（数据已随整组复制过来）
//	指向别处 / 相对路径   → 不动
//
// 改写保持键顺序（configmerge.go 的保序表示）。
func rewriteLegacyDbPathOverride(configPath, legacyPluginDir, newPluginDir string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	obj, err := parseOrderedObject(data)
	if err != nil {
		return nil // 不是合法对象就不碰，插件加载时自己会报
	}
	legacyAbs, err := filepath.Abs(legacyPluginDir)
	if err != nil {
		return err
	}
	newAbs, err := filepath.Abs(newPluginDir)
	if err != nil {
		return err
	}

	changed := false
	for i, m := range obj {
		if !slices.Contains(dbPathOverrideKeys, m.Key) {
			continue
		}
		var s string
		if json.Unmarshal(m.Raw, &s) != nil {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" || !filepath.IsAbs(s) || !pathWithin(s, legacyAbs) {
			continue
		}
		newVal := ""
		if rest := relUnder(s, legacyAbs); rest != "" {
			newVal = filepath.Join(newAbs, rest)
		}
		raw, err := json.Marshal(newVal)
		if err != nil {
			return err
		}
		obj[i].Raw = raw
		changed = true
		logger.Infof("插件配置 %s 的 %s 指向旧数据目录，已改写为 %q", configPath, m.Key, newVal)
	}
	if !changed {
		return nil
	}
	out, err := encodeOrdered(obj)
	if err != nil {
		return err
	}
	return writeFileAtomic(configPath, out)
}

// relUnder 返回 p 相对 root 的剩余部分（调用方已确认 p 位于 root 之内）。
// 不用 filepath.Rel：Windows 上两边大小写可能不同（pathWithin 是不区分大小写判定的），Rel 逐字节比较。
func relUnder(p, root string) string {
	cp, cr := filepath.Clean(p), filepath.Clean(root)
	if len(cp) <= len(cr) {
		return ""
	}
	return strings.TrimLeft(cp[len(cr):], `\/`)
}

// moveAsideIfPresent 给迁移结果腾位置：目标不存在就什么都不做；是空目录（镜像同步抢先建出的
// junction 目标）就删掉；有内容就改名保留——那是意料之外的东西，宁可多留也不能删。
func moveAsideIfPresent(target string) error {
	entries, err := os.ReadDir(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", target, err)
	}
	if len(entries) == 0 {
		return os.Remove(target)
	}
	aside := target + ".pre-migration-" + stamp()
	logger.Warnf("实例插件目录 %s 在迁移前已有内容，改名为 %s 保留", target, aside)
	if err := os.Rename(target, aside); err != nil {
		return fmt.Errorf("把 %s 改名保留失败: %w", target, err)
	}
	return nil
}

// retireLegacyInstanceDir 把旧的 instances/{name}/plugins 改名为 plugins.legacy-<时间戳>。
// 只改名不删：迁移出了问题时它是手工回退的依据。
func retireLegacyInstanceDir(instanceName string) {
	legacy := legacyPluginsDir(instanceName)
	if !isDir(legacy) {
		return
	}
	dst := filepath.Join(cfgpkg.InstancesDir, instanceName, legacyRetiredPrefix+stamp())
	if err := os.Rename(legacy, dst); err != nil {
		logger.Warnf("旧插件数据目录 %s 改名失败（不影响使用，下次启动再试）: %v", legacy, err)
		return
	}
	logger.Infof("旧插件数据目录已改名保留: %s", dst)
}

// RetireLegacyServerPlugins 把 server-files 里那份全局插件移走。
//
// **调用方负责确认所有实例都已迁移**：没迁移的实例迁移时还要从这里拷插件。
// 只移不删，移进 {BaseDir}/arkapi/backups/legacy-server-plugins-<时间戳>/。
// 留下一个空的 Plugins 目录：镜像同步要求例外 junction 的源侧存在（方案 §4.2 约束 2）。
func RetireLegacyServerPlugins() {
	src := SourcePluginsDir()
	entries, err := os.ReadDir(src)
	if err != nil || len(entries) == 0 {
		return
	}
	dst := filepath.Join(cfgpkg.BaseDir, "arkapi", "backups", "legacy-server-plugins-"+stamp())
	if err := os.MkdirAll(dst, 0755); err != nil {
		logger.Warnf("创建 %s 失败，暂不退役全局插件: %v", dst, err)
		return
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			logger.Warnf("移走全局插件 %s 失败: %v", e.Name(), err)
		}
	}
	logger.Warnf("server-files 里的全局 ArkApi 插件已全部迁入各实例，原内容移至 %s。"+
		"插件现在按实例存放在 instances/<实例名>/ArkApi/Plugins，放进 server-files 的插件不会再被加载", dst)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func stamp() string {
	return time.Now().Format("20060102-150405")
}
