package arkapimanage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/installer"
	"asa-server/internal/plugindata"
	"asa-server/pkg/fsutil"
	"asa-server/pkg/logger"
)

// 插件的安装 / 更新 / 卸载（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §6.3、§6.4）。
//
// 作用范围只有调用方显式列出的实例（方案 §1.1）——没有「默认全部」。每个目标实例独立执行、
// 各自持实例级锁、逐个返回结果：一个实例失败不影响其他实例。执行完之后各实例彼此独立。

// PluginTarget 描述一个实例相对某个插件的状态，供确认对话框的目标实例表使用。
type PluginTarget struct {
	Instance string `json:"instance"`
	// Running 表示实例运行中或正在启停，不能改动它的插件文件（原因在 Blocked 里）
	Running          bool   `json:"running"`
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version"`
	Enabled          bool   `json:"enabled"`
	// Action 是安装包 apply 到这个实例时的动作：install 或 update。卸载列表里为空
	Action string `json:"action,omitempty"`
	// Blocked 非空表示这个实例不可选，内容是原因
	Blocked string `json:"blocked,omitempty"`
	// BackupAvailable 表示没装该插件、但有上次卸载留下的备份，可以从中恢复配置与数据
	BackupAvailable bool     `json:"backup_available,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

// PluginStage 是上传插件包之后返回的校验报告。校验失败时 Token 为空。
type PluginStage struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	Kind      string    `json:"kind"`
	*PluginReport
	Targets []PluginTarget `json:"targets"`
}

// Result 是一个实例上的执行结果。
type Result struct {
	Instance string   `json:"instance"`
	OK       bool     `json:"ok"`
	Action   string   `json:"action,omitempty"`
	Error    string   `json:"error,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// StagePlugin 接收一个插件包：落进暂存区、解压、校验，返回报告与目标实例表。
// expect 非空时（从某一行点「更新」）包里的插件名必须与之相同。
//
// 包不合格时返回的报告里 Errors 非空、Token 为空，暂存区已经清掉；只有服务端或传输错误才返回 error。
func StagePlugin(src io.Reader, uploadName, expect string) (*PluginStage, error) {
	sp, err := newStage(kindPlugin)
	if err != nil {
		return nil, err
	}
	entries, extractErr, err := sp.receive(src)
	if err != nil {
		sp.remove()
		return nil, err
	}

	var rep *PluginReport
	if extractErr != nil {
		rep = &PluginReport{Files: []FileEntry{}, Errors: []string{extractErr.Error()}, Warnings: []string{}, Dependencies: []string{}}
	} else {
		rep = ValidatePluginPackage(sp.extractDir(), entries, PluginCheck{
			Expect:        expect,
			CoreInstalled: installer.ArkApiInstalled(),
		})
	}
	stage := &PluginStage{Kind: kindPlugin, PluginReport: rep, Targets: []PluginTarget{}}
	if len(rep.Errors) > 0 {
		sp.remove()
		logger.Infof("插件包 %s 未通过校验: %v", uploadName, rep.Errors)
		return stage, nil
	}

	sp.plugin = rep
	stage.Targets = pluginTargets(rep)
	register(sp)
	stage.Token, stage.ExpiresAt = sp.token, sp.expiresAt
	logger.Infof("插件包 %s 已暂存：%s %s", uploadName, rep.Name, rep.Version)
	return stage, nil
}

// ApplyPlugin 把暂存的插件包装进 targets 列出的实例。restoreFromBackup 对「新装」且有备份的实例
// 生效：从上次卸载留下的备份恢复配置与数据。暂存包用过即删，不论成败。
func ApplyPlugin(token string, targets []string, restoreFromBackup bool) ([]Result, error) {
	if len(targets) == 0 {
		return nil, errors.New("没有选择目标实例")
	}
	sp, err := take(token, kindPlugin)
	if err != nil {
		return nil, err
	}
	defer sp.remove()

	results := make([]Result, 0, len(targets))
	for _, inst := range dedupe(targets) {
		r := applyPluginTo(inst, sp.plugin, restoreFromBackup)
		if r.OK {
			logger.Infof("实例 %s：插件 %s %s 完成（%s）", inst, sp.plugin.Name, sp.plugin.Version, r.Action)
		} else {
			logger.Warnf("实例 %s：插件 %s 安装失败: %s", inst, sp.plugin.Name, r.Error)
		}
		results = append(results, r)
	}
	return results, nil
}

func applyPluginTo(instanceName string, rep *PluginReport, restoreFromBackup bool) Result {
	res := Result{Instance: instanceName}
	fail := func(err error) Result {
		res.Error = err.Error()
		return res
	}

	unlock, err := lockForPluginWrite(instanceName)
	if err != nil {
		return fail(err)
	}
	defer unlock()

	plugin := rep.Name
	oldDir, _, installed := plugindata.FindInstancePlugin(instanceName, plugin)
	var finalDir string
	switch {
	case installed:
		finalDir = oldDir // 更新留在原位：被禁用的插件更新后仍是禁用的
	case slices.Contains(plugindata.DisabledPlugins(instanceName), plugin):
		finalDir = filepath.Join(plugindata.InstanceDisabledPluginsDir(instanceName), plugin)
	default:
		finalDir = filepath.Join(plugindata.InstancePluginsDir(instanceName), plugin)
	}

	// 先在同一个卷上组装好最终形态，再 rename 到位。临时目录放在 ArkApi/ 下而不是 Plugins/ 下：
	// 放在 Plugins/ 里的话，中途崩溃留下的半成品会被 ArkApi 当成一个插件去加载。
	tmp, err := os.MkdirTemp(plugindata.InstanceArkApiDir(instanceName), ".install-"+plugin+"-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(tmp) // rename 成功后它已不存在，这里是失败路径的清理
	staged := filepath.Join(tmp, plugin)
	if err := fsutil.CopyDir(rep.root, staged); err != nil {
		return fail(fmt.Errorf("复制插件文件失败: %w", err))
	}

	carryFrom := ""
	if installed {
		res.Action = "update"
		carryFrom = oldDir
	} else {
		res.Action = "install"
		if restoreFromBackup {
			carryFrom = latestBackup(instanceName, plugin)
		}
	}
	if carryFrom != "" {
		warnings, err := plugindata.CarryOverPluginState(carryFrom, staged, plugin)
		res.Warnings = warnings
		if err != nil {
			return fail(err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(finalDir), 0755); err != nil {
		return fail(err)
	}
	if installed {
		bak, err := newBackupPath(instanceName, plugin)
		if err != nil {
			return fail(err)
		}
		if err := os.Rename(oldDir, bak); err != nil {
			return fail(fmt.Errorf("挪走旧版本失败（文件可能被占用）: %w", err))
		}
		if err := os.Rename(staged, finalDir); err != nil {
			if rbErr := os.Rename(bak, oldDir); rbErr != nil {
				return fail(fmt.Errorf("新版本落位失败: %w；旧版本也未能还原，它在 %s", err, bak))
			}
			return fail(fmt.Errorf("新版本落位失败，已还原旧版本: %w", err))
		}
		pruneBackups(instanceName, plugin)
	} else if err := os.Rename(staged, finalDir); err != nil {
		return fail(fmt.Errorf("插件落位失败: %w", err))
	}

	res.OK = true
	return res
}

// PluginInstances 列出装有插件 plugin 的全部实例（包括处于禁用状态的），供卸载对话框使用。
func PluginInstances(plugin string) ([]PluginTarget, error) {
	if err := plugindata.ValidatePluginName(plugin); err != nil {
		return nil, err
	}
	out := []PluginTarget{}
	for _, inst := range instanceNames() {
		if t := describeTarget(inst, plugin); t.Installed {
			out = append(out, t)
		}
	}
	return out, nil
}

// UninstallPlugin 从 targets 列出的实例卸载插件：插件目录整个挪进该实例的备份，配置与数据随之
// 保留；同时从该实例的禁用列表里移除它。
func UninstallPlugin(plugin string, targets []string) ([]Result, error) {
	if err := plugindata.ValidatePluginName(plugin); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, errors.New("没有选择目标实例")
	}
	results := make([]Result, 0, len(targets))
	for _, inst := range dedupe(targets) {
		r := uninstallFrom(inst, plugin)
		if r.OK {
			logger.Infof("实例 %s：插件 %s 已卸载", inst, plugin)
		} else {
			logger.Warnf("实例 %s：卸载插件 %s 失败: %s", inst, plugin, r.Error)
		}
		results = append(results, r)
	}
	return results, nil
}

func uninstallFrom(instanceName, plugin string) Result {
	res := Result{Instance: instanceName, Action: "uninstall"}
	fail := func(err error) Result {
		res.Error = err.Error()
		return res
	}

	unlock, err := lockForPluginWrite(instanceName)
	if err != nil {
		return fail(err)
	}
	defer unlock()

	dir, _, ok := plugindata.FindInstancePlugin(instanceName, plugin)
	if !ok {
		return fail(fmt.Errorf("该实例没有安装插件 %s", plugin))
	}
	bak, err := newBackupPath(instanceName, plugin)
	if err != nil {
		return fail(err)
	}
	if err := os.Rename(dir, bak); err != nil {
		return fail(fmt.Errorf("移走插件目录失败（文件可能被占用）: %w", err))
	}
	pruneBackups(instanceName, plugin)

	if err := cfgpkg.ModifyInstanceConfig(instanceName, func(c *cfgpkg.InstanceConfig) error {
		c.DisabledArkApiPlugins = setMembership(c.DisabledArkApiPlugins, plugin, false)
		return nil
	}); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("插件已卸载，但从禁用列表移除它失败: %v", err))
	}
	res.OK = true
	return res
}

// lockForPluginWrite 拿实例级锁并确认可以改动这个实例的插件文件。成功时必须调用返回的 unlock。
//
// 检查必须在拿到锁**之后**：先检查再拿锁，中间可能插进一次完整的启动，插件文件就在游戏
// 运行时被换掉了。反过来，锁在我们手里时启动会等在 PrepareForStart 上，直到我们做完。
func lockForPluginWrite(instanceName string) (func(), error) {
	if !instanceExists(instanceName) {
		return nil, fmt.Errorf("实例 %s 不存在", instanceName)
	}
	unlock, ok := plugindata.TryLockInstance(instanceName)
	if !ok {
		return nil, errors.New("实例正在启动，或有另一个插件操作正在进行，请稍后再试")
	}
	if busy := instanceBusy(instanceName); busy != "" {
		unlock()
		return nil, errors.New(busy)
	}
	if !plugindata.IsMigrated(instanceName) {
		unlock()
		return nil, ErrLegacyLayout
	}
	return unlock, nil
}

// pluginTargets 为插件包生成目标实例表：已装该插件的排在前面（从某一行点「更新」时方便勾选），
// 其余按实例名。这里只描述，不勾选——默认勾选当前实例是前端的事。
func pluginTargets(rep *PluginReport) []PluginTarget {
	out := []PluginTarget{}
	for _, inst := range instanceNames() {
		t := describeTarget(inst, rep.Name)
		if t.Installed {
			t.Action = "update"
			if rep.Version != "" && t.InstalledVersion != "" && compareVersions(rep.Version, t.InstalledVersion) <= 0 {
				t.Warnings = append(t.Warnings, fmt.Sprintf("已装版本 %s，将以 %s 重装或降级", t.InstalledVersion, rep.Version))
			}
		} else {
			t.Action = "install"
			t.BackupAvailable = latestBackup(inst, rep.Name) != ""
		}
		for _, dep := range rep.Dependencies {
			if _, _, ok := plugindata.FindInstancePlugin(inst, dep); !ok {
				t.Warnings = append(t.Warnings, fmt.Sprintf("依赖的插件 %s 在该实例上没有安装", dep))
			}
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Installed && !out[j].Installed })
	return out
}

func describeTarget(instanceName, plugin string) PluginTarget {
	t := PluginTarget{Instance: instanceName}
	if busy := instanceBusy(instanceName); busy != "" {
		t.Running = true
		t.Blocked = busy
	} else if !plugindata.IsMigrated(instanceName) {
		t.Blocked = ErrLegacyLayout.Error()
	}
	if dir, _, ok := plugindata.FindInstancePlugin(instanceName, plugin); ok {
		t.Installed = true
		if meta, err := plugindata.ReadPluginMeta(dir); err == nil {
			t.InstalledVersion = meta.Version
		}
	}
	t.Enabled = !slices.Contains(plugindata.DisabledPlugins(instanceName), plugin)
	return t
}

// instanceNames 列出所有实例（按名字排序）。实例目录下没有 instance_config.ini 的不是实例。
func instanceNames() []string {
	names, err := cfgpkg.GetAvailableInstances()
	if err != nil {
		logger.Warnf("列出实例失败: %v", err)
		return nil
	}
	out := slices.DeleteFunc(slices.Clone(names), func(n string) bool { return !instanceExists(n) })
	slices.Sort(out)
	return out
}

func instanceExists(instanceName string) bool {
	_, err := os.Stat(filepath.Join(cfgpkg.InstancesDir, instanceName, "instance_config.ini"))
	return err == nil
}

func dedupe(names []string) []string {
	var out []string
	for _, n := range names {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}
