// Package arkapimanage 编排 ArkApi 插件的启用/禁用、zip 上传安装、更新与卸载
// （docs/ARKAPI_PLUGIN_INSTALL_PLAN.md）。
//
// 插件目录布局、文件分类、实例级锁这些机制归 internal/plugindata；本包负责「能不能做」
// （实例是否停止、是否已迁移）与「怎么组合」（暂存、校验、多实例 apply、备份）。
//
// 操作范围规则（方案 §1.1）：安装/更新/卸载作用于**调用方显式列出**的实例，接口里不存在
// 「默认全部」；启用/禁用只作用于单个实例。执行完之后各实例彼此独立，没有任何持续的同步关系。
package arkapimanage

import (
	"errors"
	"fmt"
	"slices"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/plugindata"
	procpkg "asa-server/internal/process"
	statepkg "asa-server/internal/state"
)

// ErrLegacyLayout 表示实例还在旧的全局插件布局上（升级时它正在运行），停止后下次启动会自动迁移。
var ErrLegacyLayout = errors.New("该实例尚未迁移到独立插件目录（停止后下次启动时会自动迁移），暂不能管理它的插件")

// instanceBusy 返回实例不能改动插件文件的原因，可以改时返回空串。
//
// 运行中的 ArkApi 直接从实例目录加载 dll，Windows 上 dll 被占用，覆盖、删除、改名所在目录都会失败
// （方案 D2）。状态机与进程存活两个判据都要看：状态可能滞后（崩溃未被发现），进程也可能
// 还没起来（starting 早期）。
//
// 包级变量以便测试替换：真实判据要状态管理器和端口/进程探测。
var instanceBusy = func(instanceName string) string {
	if ok, _ := statepkg.IsOperationAllowed(instanceName, statepkg.OpStart); !ok {
		st := statepkg.GetInstanceStateOrDefault(instanceName)
		return fmt.Sprintf("实例处于 %s 状态，请先停止", st.Status)
	}
	if procpkg.IsInstanceProcessAlive(instanceName) {
		return "实例进程仍在运行，请先停止"
	}
	return ""
}

// SetPluginEnabled 启用或禁用某个实例的一个插件，只作用于这一个实例（方案 §1.1）。
//
// 配置总是先写；实例停止时随即落位（applied=true），运行中或正在启动时只写配置，
// 由下一次 StartServer 落位（applied=false）——运行中的 ArkApi 占着 dll，目录挪不动。
func SetPluginEnabled(instanceName, plugin string, enabled bool) (applied bool, err error) {
	if err := plugindata.ValidatePluginName(plugin); err != nil {
		return false, err
	}
	if !plugindata.IsMigrated(instanceName) {
		return false, ErrLegacyLayout
	}
	if _, _, ok := plugindata.FindInstancePlugin(instanceName, plugin); !ok {
		return false, fmt.Errorf("本实例没有安装插件 %s", plugin)
	}

	if err := cfgpkg.ModifyInstanceConfig(instanceName, func(c *cfgpkg.InstanceConfig) error {
		c.DisabledArkApiPlugins = setMembership(c.DisabledArkApiPlugins, plugin, !enabled)
		return nil
	}); err != nil {
		return false, err
	}

	unlock, ok := plugindata.TryLockInstance(instanceName)
	if !ok {
		return false, nil // 正在启动：它的 PrepareForStart 会（或下一次会）落位
	}
	defer unlock()
	// 必须在拿到锁之后再判断：先判断再拿锁，中间可能插进一次完整的启动
	if instanceBusy(instanceName) != "" {
		return false, nil
	}
	// 落位用锁内重新读到的列表：另一个开关可能刚刚改过配置
	if err := plugindata.ReconcileLocked(instanceName, plugindata.DisabledPlugins(instanceName)); err != nil {
		return false, fmt.Errorf("设置已保存，但整理插件目录失败（下次启动时会重试）: %w", err)
	}
	return true, nil
}

// setMembership 让 list 包含（member=true）或不包含 name，保持其余顺序。
func setMembership(list []string, name string, member bool) []string {
	out := slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == name })
	if member {
		out = append(out, name)
	}
	return out
}
