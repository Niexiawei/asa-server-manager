package plugindata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pluginInfoFileName 是 ArkApi 插件自带的元数据文件。
const pluginInfoFileName = "PluginInfo.json"

// PluginMeta 是 PluginInfo.json 里对管理有用的字段。
type PluginMeta struct {
	FullName    string
	Description string
	// Version / MinApiVersion 在官方插件里是 JSON 数字（"Version": 1.3）。这里保留原文：
	// 按 float 解析会把 1.10 读成 1.1。字符串形式的版本号原样接受。
	Version       string
	MinApiVersion string
	Dependencies  []string
}

// ReadPluginMeta 读取插件目录里的 PluginInfo.json（文件名不区分大小写，容忍 UTF-8 BOM）。
//
// 注意 FullName 只是展示名，**不一定**等于插件目录名：AsaApi 官方包自带的 Permissions
// 就是 "Ark:SA Permissions"。ArkApi 按 <目录名>/<目录名>.dll 加载插件。
func ReadPluginMeta(pluginDir string) (PluginMeta, error) {
	path, ok := findFileFold(pluginDir, pluginInfoFileName)
	if !ok {
		return PluginMeta{}, fmt.Errorf("缺少 %s", pluginInfoFileName)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return PluginMeta{}, err
	}
	obj, err := parseOrderedObject(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if err != nil {
		return PluginMeta{}, fmt.Errorf("解析 %s 失败: %w", pluginInfoFileName, err)
	}

	var m PluginMeta
	for _, mem := range obj {
		switch {
		case strings.EqualFold(mem.Key, "FullName"):
			m.FullName = scalarText(mem.Raw)
		case strings.EqualFold(mem.Key, "Description"):
			m.Description = scalarText(mem.Raw)
		case strings.EqualFold(mem.Key, "Version"):
			m.Version = scalarText(mem.Raw)
		case strings.EqualFold(mem.Key, "MinApiVersion"):
			m.MinApiVersion = scalarText(mem.Raw)
		case strings.EqualFold(mem.Key, "Dependencies"):
			_ = json.Unmarshal(mem.Raw, &m.Dependencies)
		}
	}
	return m, nil
}

// scalarText 把 JSON 字符串或数字统一成文本；数字保留字面原文（1.10 仍是 "1.10"）。
func scalarText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// findFileFold 在 dir 里找名字等于 name 的普通文件：精确匹配优先，其次不区分大小写。
func findFileFold(dir, name string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var fold string
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if e.Name() == name {
			return filepath.Join(dir, name), true
		}
		if fold == "" && strings.EqualFold(e.Name(), name) {
			fold = e.Name()
		}
	}
	if fold != "" {
		return filepath.Join(dir, fold), true
	}
	return "", false
}
