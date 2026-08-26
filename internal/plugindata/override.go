package plugindata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// dbPathOverrideKeys 是已知的「让插件把数据库写到别处」的配置键。
// 目前只有 Permissions 插件的 DbPathOverride（已验证：它接受的是**目录**）。
var dbPathOverrideKeys = []string{"DbPathOverride"}

// hasExternalDBPath 判断某个插件的数据库路径是否已被用户手工接管到实例插件目录之外。
//
// 为什么要识别它，却又不拿它当机制：
//
// 把 DbPathOverride 指向实例目录，确实能让 SQLite 直接写实例目录、彻底消除搬运的崩溃窗口。
// 但它是**某个插件的可选字段** —— 别的插件有没有、叫什么、语义如何都不保证。
// 拿它当底座会得到一个按插件分叉的系统，所以搬运才是唯一机制。
//
// 可一旦用户自己设了它，我们的搬运就会对着一个空目录做无用功，
// 真实数据在别处不受保护，而且**不报任何错**。所以必须识别出来并让路 + 明确提示。
//
// 优先读实例侧配置（那是真相），实例侧还没有时退回镜像侧。
// 返回 (是否指向外部, 该路径原文)。
func hasExternalDBPath(instPluginDir, mirrorPluginDir string) (bool, string) {
	raw := readOverridePath(filepath.Join(instPluginDir, configFileName))
	if raw == "" {
		raw = readOverridePath(filepath.Join(mirrorPluginDir, configFileName))
	}
	if raw == "" {
		return false, "" // 默认值，走正常搬运
	}

	abs, err := filepath.Abs(raw)
	if err != nil {
		return true, raw // 路径都解析不了，更不该由我们接管
	}
	instAbs, err := filepath.Abs(instPluginDir)
	if err != nil {
		return true, raw
	}

	// 指向实例插件目录之内 → 等价形态，正常搬运
	if pathWithin(abs, instAbs) {
		return false, raw
	}
	return true, raw
}

// readOverridePath 从一份 config.json 里读出数据库路径覆盖项，读不到返回空串。
// 这里只关心一个顶层字符串键，用 map 解析即可 —— 不涉及写回，无需保序。
func readOverridePath(configPath string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return ""
	}
	for _, key := range dbPathOverrideKeys {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// pathWithin 判断 p 是否位于 root 之内（含 root 自身）。
//
// 两边都已是绝对路径，比较前先经 pathCompareKey 折叠——Windows 上 NTFS 大小写不敏感，
// 用户手填的路径盘符与目录名大小写与我们拼出来的十有八九对不上，filepath.Rel 是逐字节
// 比较的，直接用会把「其实就在实例目录里」误判成外部路径；Linux 上大多数文件系统
// （ext4/xfs 等）大小写敏感，折叠反而会把 `/a/DB` 和 `/a/db` 错误地判成同一路径，
// 见 docs/LINUX_COMPATIBILITY_PLAN.md §5.12 表格第 2 条，两平台分别实现见
// override_windows.go / override_linux.go。
func pathWithin(p, root string) bool {
	pc := pathCompareKey(filepath.Clean(p))
	rc := pathCompareKey(filepath.Clean(root))
	return pc == rc || strings.HasPrefix(pc, rc+"/")
}
