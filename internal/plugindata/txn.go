package plugindata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"asa-server/pkg/logger"
)

// pluginTxnName 是插件更新的事务日志：ArkApi/.plugin-txn.json。
//
// 更新一个已装的插件是两次 rename：旧版本挪进备份，新版本落位。两次之间崩溃，
// 插件目录就不在了 —— 面板显示「未安装」，旧版本连同配置与数据躺在 Backups 里，
// 只能手工恢复（docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §7.2 P1-22）。多一次
// rename 也消不掉这个窗口，所以改为先落一份日志，下次拿实例锁时按日志补完。
// 启动路径（PrepareForStart）也经过这把锁，崩溃后第一次启动前一定会被补完。
const pluginTxnName = ".plugin-txn.json"

// pluginTxn 里的路径都相对 InstanceArkApiDir：数据目录整体搬家后日志仍然有效。
type pluginTxn struct {
	Plugin string `json:"plugin"`
	Final  string `json:"final"`  // 插件目录（Plugins/<N> 或 PluginsDisabled/<N>）
	Backup string `json:"backup"` // 旧版本挪去的备份目录
	Staged string `json:"staged"` // 组装好的新版本
	Tmp    string `json:"tmp"`    // staged 所在的临时目录，收尾时删除
}

func pluginTxnPath(instanceName string) string {
	return filepath.Join(InstanceArkApiDir(instanceName), pluginTxnName)
}

// BeginPluginTxn 在第一次 rename 之前写下事务日志。调用方持实例锁；四个路径都必须
// 位于该实例的 ArkApi 目录之下。两次 rename 都成功（或已回滚）后调用 EndPluginTxn。
// 日志还在时 tmp 不能删：staged 就在里面，恢复要用它。
func BeginPluginTxn(instanceName, plugin, final, backup, staged, tmp string) error {
	root := InstanceArkApiDir(instanceName)
	txn := pluginTxn{Plugin: plugin}
	for _, f := range []struct {
		abs string
		rel *string
	}{{final, &txn.Final}, {backup, &txn.Backup}, {staged, &txn.Staged}, {tmp, &txn.Tmp}} {
		rel, err := filepath.Rel(root, f.abs)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("%s 不在实例的 ArkApi 目录下", f.abs)
		}
		*f.rel = filepath.ToSlash(rel)
	}
	data, err := json.Marshal(txn)
	if err != nil {
		return err
	}
	return writeFileAtomic(pluginTxnPath(instanceName), data)
}

// EndPluginTxn 删除事务日志。
func EndPluginTxn(instanceName string) error {
	if err := os.Remove(pluginTxnPath(instanceName)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// PluginTxnPending 报告实例是否有未结束的插件更新事务。
func PluginTxnPending(instanceName string) bool {
	return exists(pluginTxnPath(instanceName))
}

// recoverPluginTxn 补完上一次中断的插件更新。调用方持实例锁。
//
//	插件目录在     → 两次 rename 都做完了，或第一次还没做（旧版本原地未动）：只收尾
//	不在，新版本在 → 完成更新
//	都不在，备份在 → 新版本丢了，回滚到更新前
//
// rename 失败时保留日志，下次拿锁再试。
func recoverPluginTxn(instanceName string) {
	path := pluginTxnPath(instanceName)
	data, err := os.ReadFile(path)
	if err != nil {
		return // 没有中断的事务（绝大多数情况）
	}
	var txn pluginTxn
	if err := json.Unmarshal(data, &txn); err != nil {
		logger.Warnf("实例 %s 的插件事务日志 %s 已损坏，忽略: %v", instanceName, path, err)
		_ = os.Remove(path)
		return
	}
	root := InstanceArkApiDir(instanceName)
	var final, backup, staged, tmp string
	for _, f := range []struct {
		rel string
		abs *string
	}{{txn.Final, &final}, {txn.Backup, &backup}, {txn.Staged, &staged}, {txn.Tmp, &tmp}} {
		rel := filepath.FromSlash(f.rel)
		if !filepath.IsLocal(rel) {
			logger.Warnf("实例 %s 的插件事务日志 %s 含非法路径 %q，忽略", instanceName, path, f.rel)
			_ = os.Remove(path)
			return
		}
		*f.abs = filepath.Join(root, rel)
	}

	switch {
	case exists(final):
		logger.Infof("实例 %s：清理插件 %s 上次更新留下的临时文件", instanceName, txn.Plugin)
	case exists(staged):
		if err := os.Rename(staged, final); err != nil {
			logger.Warnf("实例 %s：补完插件 %s 的更新失败，下次再试: %v", instanceName, txn.Plugin, err)
			return
		}
		logger.Infof("实例 %s：插件 %s 上次更新在中途被打断，已补完（新版本已落位，旧版本在 %s）",
			instanceName, txn.Plugin, backup)
	case exists(backup):
		if err := os.Rename(backup, final); err != nil {
			logger.Warnf("实例 %s：回滚插件 %s 失败，下次再试: %v", instanceName, txn.Plugin, err)
			return
		}
		logger.Infof("实例 %s：插件 %s 上次更新在中途被打断且新版本已丢失，已回滚到更新前的版本",
			instanceName, txn.Plugin)
	default:
		logger.Warnf("实例 %s：插件 %s 上次更新在中途被打断，新旧版本都找不到（%s / %s），无法自动恢复",
			instanceName, txn.Plugin, staged, backup)
	}
	_ = os.RemoveAll(tmp)
	_ = os.Remove(path)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
