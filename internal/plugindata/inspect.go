package plugindata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// PluginInfo 描述一个插件在某个实例下的隔离状态。
type PluginInfo struct {
	Name string `json:"name"`
	// HasConfig 表示源服务端的插件目录里有 config.json（有些插件没有配置文件）。
	HasConfig bool `json:"has_config"`
	// Isolated 表示实例侧已经有这个插件的独立配置/数据；
	// 首次启动之前为 false，此时展示的是源服务端自带的那一份。
	Isolated  bool       `json:"isolated"`
	DataFiles []FileInfo `json:"data_files"`
	Snapshots []FileInfo `json:"snapshots"`
	// ExternalDBPath 非空表示用户用 DbPathOverride 把数据库接管到了实例目录之外，
	// 管理器不再为该插件做隔离、回收与快照。前端应当明确提示。
	ExternalDBPath string `json:"external_db_path,omitempty"`
}

// SourcePluginsDir 返回源服务端的 ArkApi 插件目录。
// 它是「装了哪些插件」的权威来源 —— 镜像只在实例运行期存在，
// 实例插件目录则要到首次启动之后才有内容。
func SourcePluginsDir() string {
	return filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(pluginsRelPath))
}

// ListInstancePlugins 列出某个实例的插件隔离状态。
func ListInstancePlugins(instanceName string) ([]PluginInfo, error) {
	entries, err := os.ReadDir(SourcePluginsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []PluginInfo{}, nil // 未安装 ArkApi
		}
		return nil, err
	}

	out := make([]PluginInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, describePlugin(instanceName, e.Name()))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func describePlugin(instanceName, plugin string) PluginInfo {
	srcPlugin := filepath.Join(SourcePluginsDir(), plugin)
	instPlugin := filepath.Join(InstancePluginsDir(instanceName), plugin)

	info := PluginInfo{Name: plugin}
	if _, err := os.Stat(filepath.Join(srcPlugin, configFileName)); err == nil {
		info.HasConfig = true
	}
	if _, err := os.Stat(instPlugin); err == nil {
		info.Isolated = true
	}
	if external, path := hasExternalDBPath(instPlugin, srcPlugin); external {
		info.ExternalDBPath = path
	}

	for _, g := range scanPluginDir(instPlugin, plugin) {
		if g.IsConfig {
			info.HasConfig = true
			continue
		}
		for _, m := range g.Members {
			if fi, err := os.Stat(filepath.Join(instPlugin, filepath.FromSlash(m))); err == nil {
				info.DataFiles = append(info.DataFiles, FileInfo{
					Name: m, Size: fi.Size(), Modified: fi.ModTime(), IsSQLite: g.IsSQLite,
				})
			}
		}
	}

	snapDir := filepath.Join(instPlugin, snapshotsDirName)
	if snaps, err := os.ReadDir(snapDir); err == nil {
		for _, s := range snaps {
			fi, err := s.Info()
			if err != nil || s.IsDir() {
				continue
			}
			info.Snapshots = append(info.Snapshots, FileInfo{
				Name: s.Name(), Size: fi.Size(), Modified: fi.ModTime(),
				IsSQLite: isSQLiteFile(filepath.Join(snapDir, s.Name())),
			})
		}
	}

	if info.DataFiles == nil {
		info.DataFiles = []FileInfo{}
	}
	if info.Snapshots == nil {
		info.Snapshots = []FileInfo{}
	}
	return info
}

// ReadPluginConfig 读取某个插件在该实例下的配置。
//
// 实例侧还没有（首次启动之前）时回落到源服务端自带的那一份，并以 seeded=false 告知调用方：
// 展示出来的是默认值，还没有成为这个实例的配置。
func ReadPluginConfig(instanceName, plugin string) (content string, seeded bool, err error) {
	if err := validatePluginName(plugin); err != nil {
		return "", false, err
	}

	instPath := filepath.Join(InstancePluginsDir(instanceName), plugin, configFileName)
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
// 写进的是**实例目录**而不是镜像：镜像随时会被重建，写进去等于白写。
// 实例目录里的这一份会在下次启动时注入镜像。
//
// 内容必须是合法的 JSON 对象 —— 写坏了插件会加载失败，而那要到开服时才发现。
func WritePluginConfig(instanceName, plugin, content string) error {
	if err := validatePluginName(plugin); err != nil {
		return err
	}
	if !json.Valid([]byte(content)) {
		return fmt.Errorf("内容不是合法的 JSON")
	}
	if _, err := parseOrderedObject([]byte(content)); err != nil {
		return fmt.Errorf("配置必须是一个 JSON 对象: %w", err)
	}

	dir := filepath.Join(InstancePluginsDir(instanceName), plugin)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建实例插件目录失败: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, configFileName), []byte(content))
}

// validatePluginName 挡住路径穿越：插件名直接来自 URL。
func validatePluginName(plugin string) error {
	if plugin == "" {
		return fmt.Errorf("插件名不能为空")
	}
	if strings.ContainsAny(plugin, `/\:`) || strings.Contains(plugin, "..") {
		return fmt.Errorf("非法的插件名: %q", plugin)
	}
	return nil
}
