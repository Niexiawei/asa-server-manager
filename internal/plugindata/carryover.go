package plugindata

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// CarryOverPluginState 把 oldDir 里的配置与运行期数据带进 newDir（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §6.3）。
//
// oldDir 是该实例正在用的插件目录（更新），或上次卸载留下的备份（重装时从备份恢复）；
// newDir 是已经放好新包全部文件的临时目录。规则：
//
//	config.json   MergeConfigJSON(旧, 新)：旧值优先，新版本新增的键并入，保序。
//	              这一份就是实例正在用的配置，所以新增的配置键会立刻出现在用户面前。
//	              新包没带 config.json 时原样保留旧的；任一侧不是 JSON 对象时保留旧的原文并警告
//	数据文件组    SQLite 文件组与其他可识别的数据文件，整组用旧的替换新包里的同名组
//	              （新包可能带着种子库，逐文件覆盖会拼出互不匹配的主库与 -wal）
//	其余文件      一律以新包为准；旧目录里多出来的随旧目录一起进备份
func CarryOverPluginState(oldDir, newDir, plugin string) (warnings []string, err error) {
	for _, g := range scanPluginDir(oldDir, plugin) {
		if g.IsConfig {
			if w, err := carryConfig(oldDir, newDir, g.Base); err != nil {
				return warnings, err
			} else if w != "" {
				warnings = append(warnings, w)
			}
			continue
		}
		if err := replaceGroup(oldDir, newDir, g); err != nil {
			return warnings, fmt.Errorf("保留插件 %s 的数据 %s 失败: %w", plugin, g.Base, err)
		}
	}
	return warnings, nil
}

func carryConfig(oldDir, newDir, rel string) (warning string, err error) {
	oldPath := filepath.Join(oldDir, filepath.FromSlash(rel))
	newPath := filepath.Join(newDir, filepath.FromSlash(rel))
	oldData, err := os.ReadFile(oldPath)
	if err != nil {
		return "", err
	}
	newData, err := os.ReadFile(newPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		return "", writeFileAtomicMkdir(newPath, oldData)
	}

	merged, err := MergeConfigJSON(oldData, newData)
	if err != nil {
		return fmt.Sprintf("%s 无法合并，已保留原配置、未并入新版本的默认项：%v", rel, err),
			writeFileAtomic(newPath, oldData)
	}
	// 新版本没带来新键时原样保留旧文件：合并会统一缩进，用户手排的格式不该因为一次更新被改写
	if obj, err := parseOrderedObject(oldData); err == nil {
		if reencoded, err := encodeOrdered(obj); err == nil && bytes.Equal(reencoded, merged) {
			merged = oldData
		}
	}
	return "", writeFileAtomic(newPath, merged)
}

func writeFileAtomicMkdir(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}
