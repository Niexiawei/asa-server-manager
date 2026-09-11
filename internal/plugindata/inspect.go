package plugindata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	cfgpkg "asa-server/internal/config"
)

// FileInfo 是暴露给 API 的文件描述。
type FileInfo struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	IsSQLite bool      `json:"is_sqlite"`
}

// PluginInfo 描述一个插件在某个实例下的状态。
type PluginInfo struct {
	Name string `json:"name"`
	// 以下四项来自插件自带的 PluginInfo.json，读不到时为空。
	FullName      string `json:"full_name"`
	Version       string `json:"version"`
	Description   string `json:"description"`
	MinApiVersion string `json:"min_api_version"`
	// Enabled 取自实例配置的禁用列表（启用状态的唯一真相）。旧布局下恒为 true。
	Enabled bool `json:"enabled"`
	// Pending 表示开关改过了、但插件目录还没挪到对应位置（实例运行中改的），下次启动时生效。
	Pending bool `json:"pending"`
	// DllMissing 表示插件目录里没有 <目录名>.dll。ArkApi 按这个名字加载插件，
	// 缺了它这个插件实际不会被加载（典型是卸载后残留的配置与数据）。
	DllMissing bool `json:"dll_missing"`
	// HasConfig 表示插件有 config.json（有些插件没有配置文件）。
	HasConfig bool       `json:"has_config"`
	DataFiles []FileInfo `json:"data_files"`
	Snapshots []FileInfo `json:"snapshots"`
	// ExternalDBPath 非空表示用户用 DbPathOverride 把数据库放到了插件目录之外，
	// 管理器不再为它做快照（旧布局下也不做隔离与回收）。前端应当明确提示。
	ExternalDBPath string `json:"external_db_path,omitempty"`
}

// SourcePluginsRelPath 返回 server-files 里 ArkApi 插件目录的相对路径（forward slash），
// ArkApi 与 Plugins 两级按盘上的**实际大小写**给出（手工解压出来的可能是 arkapi/）。
//
// 镜像里 Plugins 那条例外 junction 的键就用它：例外清单按字符串精确匹配 Walk 出来的
// 相对路径，大小写对不上时这条例外永远不命中（方案 §4.2 约束 5）。
func SourcePluginsRelPath() string {
	win64 := filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64RelPath))
	ark := actualChildName(win64, "ArkApi")
	plugins := actualChildName(filepath.Join(win64, ark), "Plugins")
	return win64RelPath + "/" + ark + "/" + plugins
}

// SourcePluginsDir 返回 server-files 里的 ArkApi 插件目录。
//
// 在每实例布局下它不再是「装了哪些插件」的来源：迁移时从这里拷一份给每个实例，
// 所有实例迁完后它被清空（RetireLegacyServerPlugins）。
func SourcePluginsDir() string {
	return filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(SourcePluginsRelPath()))
}

// ListInstancePlugins 列出某个实例的插件。
//
// 已迁移的实例以它自己的插件目录为准（Plugins 与 PluginsDisabled 两处）；尚未迁移的
// （升级时正在运行）仍按旧布局，以 server-files 里的全局插件为准。
func ListInstancePlugins(instanceName string) ([]PluginInfo, error) {
	if !IsMigrated(instanceName) {
		return listLegacyPlugins(instanceName)
	}

	disabled := DisabledPlugins(instanceName)
	out := []PluginInfo{}
	seen := map[string]bool{}
	for _, loc := range []struct {
		root      string
		inEnabled bool
	}{
		{InstancePluginsDir(instanceName), true},
		{InstanceDisabledPluginsDir(instanceName), false},
	} {
		for _, plugin := range subdirNames(loc.root) {
			if seen[plugin] {
				continue // 两处都有同名目录：落位时会报错让用户处理，列表里只列 Plugins 那份
			}
			seen[plugin] = true
			info := describeInstancePlugin(instanceName, plugin, filepath.Join(loc.root, plugin))
			info.Enabled = !slices.Contains(disabled, plugin)
			info.Pending = info.Enabled != loc.inEnabled
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func listLegacyPlugins(instanceName string) ([]PluginInfo, error) {
	entries, err := os.ReadDir(SourcePluginsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []PluginInfo{}, nil
		}
		return nil, err
	}
	out := make([]PluginInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, describeLegacyPlugin(instanceName, e.Name()))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func describeInstancePlugin(instanceName, plugin, dir string) PluginInfo {
	info := newPluginInfo(plugin, dir)
	if external, path := hasExternalDBPath(dir, dir); external {
		info.ExternalDBPath = path
	}
	info.DataFiles = dataFilesOf(dir, plugin)
	info.Snapshots = listSnapshotFiles(filepath.Join(InstanceSnapshotsDir(instanceName), plugin))
	return info
}

// describeLegacyPlugin 是旧布局（尚未迁移）下的描述：二进制与元数据看 server-files，
// 配置与数据看实例目录下的 plugins/。
func describeLegacyPlugin(instanceName, plugin string) PluginInfo {
	srcPlugin := filepath.Join(SourcePluginsDir(), plugin)
	instPlugin := filepath.Join(legacyPluginsDir(instanceName), plugin)

	info := newPluginInfo(plugin, srcPlugin)
	info.Enabled = true
	if _, err := os.Stat(filepath.Join(instPlugin, configFileName)); err == nil {
		info.HasConfig = true
	}
	if external, path := hasExternalDBPath(instPlugin, srcPlugin); external {
		info.ExternalDBPath = path
	}
	info.DataFiles = dataFilesOf(instPlugin, plugin)
	info.Snapshots = listSnapshotFiles(filepath.Join(instPlugin, snapshotsDirName))
	return info
}

// newPluginInfo 填入从插件目录本身能看出来的部分：元数据、有没有 dll、有没有配置。
func newPluginInfo(plugin, dir string) PluginInfo {
	info := PluginInfo{Name: plugin, DataFiles: []FileInfo{}, Snapshots: []FileInfo{}}
	if meta, err := ReadPluginMeta(dir); err == nil {
		info.FullName = meta.FullName
		info.Version = meta.Version
		info.Description = meta.Description
		info.MinApiVersion = meta.MinApiVersion
	}
	if fi, err := os.Stat(filepath.Join(dir, plugin+".dll")); err != nil || !fi.Mode().IsRegular() {
		info.DllMissing = true
	}
	if _, err := os.Stat(filepath.Join(dir, configFileName)); err == nil {
		info.HasConfig = true
	}
	return info
}

func dataFilesOf(dir, plugin string) []FileInfo {
	out := []FileInfo{}
	for _, g := range scanPluginDir(dir, plugin) {
		if g.IsConfig {
			continue
		}
		for _, m := range g.Members {
			if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(m))); err == nil {
				out = append(out, FileInfo{Name: m, Size: fi.Size(), Modified: fi.ModTime(), IsSQLite: g.IsSQLite})
			}
		}
	}
	return out
}

func listSnapshotFiles(dir string) []FileInfo {
	out := []FileInfo{}
	snaps, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, s := range snaps {
		fi, err := s.Info()
		if err != nil || s.IsDir() {
			continue
		}
		out = append(out, FileInfo{
			Name: s.Name(), Size: fi.Size(), Modified: fi.ModTime(),
			IsSQLite: isSQLiteFile(filepath.Join(dir, s.Name())),
		})
	}
	return out
}

// ReadPluginConfig 读取某个插件在该实例下的配置。
//
// 已迁移的实例直接读实例插件目录里的 config.json，seeded 恒为 true。
// 旧布局下实例侧还没有（首次启动之前）时回落到源服务端自带的那一份，并以 seeded=false
// 告知调用方：展示出来的是默认值，还没有成为这个实例的配置。
func ReadPluginConfig(instanceName, plugin string) (content string, seeded bool, err error) {
	if err := ValidatePluginName(plugin); err != nil {
		return "", false, err
	}

	if IsMigrated(instanceName) {
		dir, _, ok := FindInstancePlugin(instanceName, plugin)
		if !ok {
			return "", false, fmt.Errorf("本实例没有安装插件 %s", plugin)
		}
		data, err := os.ReadFile(filepath.Join(dir, configFileName))
		if err != nil {
			return "", false, fmt.Errorf("插件 %s 没有配置文件: %w", plugin, err)
		}
		return string(data), true, nil
	}

	instPath := filepath.Join(legacyPluginsDir(instanceName), plugin, configFileName)
	if data, err := os.ReadFile(instPath); err == nil {
		return string(data), true, nil
	}

	srcPath := filepath.Join(SourcePluginsDir(), plugin, configFileName)
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", false, fmt.Errorf("插件 %s 没有配置文件: %w", plugin, err)
	}
	return string(data), false, nil
}

// WritePluginConfig 写入某个插件在该实例下的配置。
//
// 已迁移的实例直接写实例插件目录——那就是插件实际读的文件。旧布局下写进旧的
// instances/{name}/plugins/，下次启动迁移时带过去。两种情况都不写镜像：镜像随时会被重建。
//
// 内容必须是合法的 JSON 对象 —— 写坏了插件会加载失败，而那要到开服时才发现。
func WritePluginConfig(instanceName, plugin, content string) error {
	if err := ValidatePluginName(plugin); err != nil {
		return err
	}
	if !json.Valid([]byte(content)) {
		return fmt.Errorf("内容不是合法的 JSON")
	}
	if _, err := parseOrderedObject([]byte(content)); err != nil {
		return fmt.Errorf("配置必须是一个 JSON 对象: %w", err)
	}

	mu := instanceLock(instanceName)
	mu.Lock()
	defer mu.Unlock()

	if IsMigrated(instanceName) {
		// 不替不存在的插件建目录：那会凭空多出一个没有 dll 的「插件」。
		// 被禁用的插件同样可以改配置，改的是 PluginsDisabled 里那份，启用时随目录一起回去。
		dir, _, ok := FindInstancePlugin(instanceName, plugin)
		if !ok {
			return fmt.Errorf("本实例没有安装插件 %s", plugin)
		}
		return writeFileAtomic(filepath.Join(dir, configFileName), []byte(content))
	}

	dir := filepath.Join(legacyPluginsDir(instanceName), plugin)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建实例插件目录失败: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, configFileName), []byte(content))
}

// ValidatePluginName 校验插件目录名。插件名直接来自 URL 或上传的包，所以首先要挡住路径穿越；
// 另外不许含逗号（禁用列表在 instance_config.ini 里是逗号分隔的一行）、不许以 . 开头
// （实例插件目录下 .xxx 是本程序的临时目录）。
func ValidatePluginName(plugin string) error {
	if plugin == "" {
		return fmt.Errorf("插件名不能为空")
	}
	if strings.ContainsAny(plugin, `/\:,`) || strings.Contains(plugin, "..") || strings.HasPrefix(plugin, ".") {
		return fmt.Errorf("非法的插件名: %q", plugin)
	}
	return nil
}
