package plugindata

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfgpkg "asa-server/internal/config"
)

// newInstance 建一个已采用每实例布局的实例，配置里带上禁用列表。
// LoadInstanceConfig 会改写 GameUserSettings.ini，文件不存在就报错，所以一并建好。
func newInstance(t *testing.T, name string, disabled ...string) {
	t.Helper()
	writeFile(t, filepath.Join(cfgpkg.InstancesDir, name, "Config", "GameUserSettings.ini"), "[ServerSettings]\n")
	if err := cfgpkg.SaveInstanceConfig(name, &cfgpkg.InstanceConfig{ServerName: name, DisabledArkApiPlugins: disabled}); err != nil {
		t.Fatal(err)
	}
	if err := InitInstanceLayout(name); err != nil {
		t.Fatal(err)
	}
}

func putPlugin(t *testing.T, root, plugin string) {
	t.Helper()
	writeFile(t, filepath.Join(root, plugin, plugin+".dll"), "MZ")
	writeFile(t, filepath.Join(root, plugin, configFileName), `{"k":1}`)
}

func TestReconcileMovesBothWays(t *testing.T) {
	setupLayoutEnv(t)
	newInstance(t, "a")
	putPlugin(t, InstancePluginsDir("a"), "On")
	putPlugin(t, InstancePluginsDir("a"), "ToDisable")
	putPlugin(t, InstanceDisabledPluginsDir("a"), "ToEnable")

	if err := ReconcileLocked("a", []string{"ToDisable"}); err != nil {
		t.Fatal(err)
	}
	for plugin, wantEnabled := range map[string]bool{"On": true, "ToDisable": false, "ToEnable": true} {
		_, enabled, ok := FindInstancePlugin("a", plugin)
		if !ok || enabled != wantEnabled {
			t.Errorf("%s: ok=%v enabled=%v，want enabled=%v", plugin, ok, enabled, wantEnabled)
		}
	}
	// 数据随目录一起走
	if got := readFile(t, filepath.Join(InstanceDisabledPluginsDir("a"), "ToDisable", configFileName)); got != `{"k":1}` {
		t.Errorf("禁用后配置应随目录保留，实际 %q", got)
	}
}

// 两处都有同名目录时不猜，报错；不能静默选一份把另一份覆盖掉。
func TestReconcileConflictReportsError(t *testing.T) {
	setupLayoutEnv(t)
	newInstance(t, "a")
	putPlugin(t, InstancePluginsDir("a"), "Dup")
	putPlugin(t, InstanceDisabledPluginsDir("a"), "Dup")

	err := ReconcileLocked("a", []string{"Dup"})
	if err == nil || !strings.Contains(err.Error(), "都有") {
		t.Fatalf("err = %v", err)
	}
	if !isDir(filepath.Join(InstancePluginsDir("a"), "Dup")) || !isDir(filepath.Join(InstanceDisabledPluginsDir("a"), "Dup")) {
		t.Error("冲突时两份都必须原样保留")
	}
}

// 列表同时列出启用与禁用的插件；运行中改过开关、目录还没落位的标为 pending。
func TestListIncludesDisabledAndPending(t *testing.T) {
	setupLayoutEnv(t)
	newInstance(t, "a", "Off", "PendingOff")
	putPlugin(t, InstancePluginsDir("a"), "On")
	putPlugin(t, InstanceDisabledPluginsDir("a"), "Off")
	putPlugin(t, InstancePluginsDir("a"), "PendingOff") // 配置说禁用，目录还在 Plugins
	putPlugin(t, InstanceDisabledPluginsDir("a"), "PendingOn")

	list, err := ListInstancePlugins("a")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]bool{}
	for _, p := range list {
		got[p.Name] = [2]bool{p.Enabled, p.Pending}
	}
	want := map[string][2]bool{
		"On":         {true, false},
		"Off":        {false, false},
		"PendingOff": {false, true},
		"PendingOn":  {true, true},
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: enabled,pending = %v，want %v", name, got[name], w)
		}
	}
}

// 启动前的顺序：迁移 → 落位 → 同步镜像，全程持实例级锁（安装/卸载在这期间拿不到锁）。
func TestPrepareForStartReconcilesThenSyncsUnderLock(t *testing.T) {
	setupLayoutEnv(t)
	newInstance(t, "a")
	putPlugin(t, InstancePluginsDir("a"), "P")

	synced := false
	err := PrepareForStart("a", "", []string{"P"}, func() error {
		synced = true
		if _, enabled, _ := FindInstancePlugin("a", "P"); enabled {
			t.Error("同步镜像之前就应当已经落位")
		}
		if unlock, ok := TryLockInstance("a"); ok {
			unlock()
			t.Error("同步镜像期间实例级锁必须被持有")
		}
		return nil
	})
	if err != nil || !synced {
		t.Fatalf("err=%v synced=%v", err, synced)
	}
	unlock, ok := TryLockInstance("a")
	if !ok {
		t.Fatal("PrepareForStart 返回后锁必须释放")
	}
	unlock()
}

func TestPrepareForStartAbortsWhenReconcileFails(t *testing.T) {
	setupLayoutEnv(t)
	newInstance(t, "a")
	putPlugin(t, InstancePluginsDir("a"), "Dup")
	putPlugin(t, InstanceDisabledPluginsDir("a"), "Dup")

	err := PrepareForStart("a", "", []string{"Dup"}, func() error {
		t.Error("落位失败时不应继续同步镜像")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "中止启动") {
		t.Fatalf("err = %v", err)
	}

	// syncMirror 的错误原样返回（换一个没有冲突的实例）
	syncErr := errors.New("sync boom")
	newInstance(t, "b")
	if err := PrepareForStart("b", "", nil, func() error { return syncErr }); !errors.Is(err, syncErr) {
		t.Errorf("syncMirror 的错误应原样返回，实际 %v", err)
	}
}

// 被禁用的插件同样可以查看、编辑配置（改的是 PluginsDisabled 里那份）。
func TestConfigOfDisabledPlugin(t *testing.T) {
	setupLayoutEnv(t)
	newInstance(t, "a", "Off")
	putPlugin(t, InstanceDisabledPluginsDir("a"), "Off")

	if err := WritePluginConfig("a", "Off", `{"k":2}`); err != nil {
		t.Fatal(err)
	}
	content, _, err := ReadPluginConfig("a", "Off")
	if err != nil || content != `{"k":2}` {
		t.Fatalf("content=%q err=%v", content, err)
	}
	if _, err := os.Stat(filepath.Join(InstancePluginsDir("a"), "Off")); !os.IsNotExist(err) {
		t.Error("写禁用插件的配置不应在 Plugins 下凭空建出目录")
	}
}

func TestValidatePluginName(t *testing.T) {
	for _, bad := range []string{"", "a/b", `a\b`, "..", "a..b", "C:x", "A,B", ".hidden"} {
		if ValidatePluginName(bad) == nil {
			t.Errorf("%q 应被拒", bad)
		}
	}
	for _, ok := range []string{"Permissions", "TidyDamsASA", "Ark SA-Plugin_2"} {
		if err := ValidatePluginName(ok); err != nil {
			t.Errorf("%q 应通过: %v", ok, err)
		}
	}
}
