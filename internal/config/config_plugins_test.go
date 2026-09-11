package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// DisabledArkApiPlugins 必须能往返，且写在 MessageOfTheDay 之前：MOTD 是自由文本、按行解析，
// 必须留在末尾。
func TestDisabledArkApiPluginsRoundTrip(t *testing.T) {
	name := setupTempInstance(t, &InstanceConfig{
		ServerName:            "srv",
		DisabledArkApiPlugins: []string{"Permissions", "TidyDamsASA"},
		MessageOfTheDay:       "welcome, DisabledArkApiPlugins=Evil",
	})
	got, err := LoadInstanceConfig(name)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.DisabledArkApiPlugins, []string{"Permissions", "TidyDamsASA"}) {
		t.Errorf("DisabledArkApiPlugins = %v", got.DisabledArkApiPlugins)
	}
	if got.MessageOfTheDay != "welcome, DisabledArkApiPlugins=Evil" {
		t.Errorf("MessageOfTheDay = %q", got.MessageOfTheDay)
	}
}

func TestSplitPluginList(t *testing.T) {
	if got := splitPluginList(" A, ,B,A ,"); !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("splitPluginList = %v", got)
	}
	if got := splitPluginList(""); got != nil {
		t.Errorf("空串应解析为 nil，实际 %v", got)
	}
}

// 部分更新（基础配置 Tab、快照周期、启用开关）不能碰禁用列表：它不在更新请求里。
func TestPartialUpdateKeepsDisabledPlugins(t *testing.T) {
	name := setupTempInstance(t, &InstanceConfig{ServerName: "srv", DisabledArkApiPlugins: []string{"X"}})
	got := updateFromJSON(t, name, `{"EnableAsaPlugin": true, "DisabledArkApiPlugins": []}`)
	if !slices.Equal(got.DisabledArkApiPlugins, []string{"X"}) {
		t.Errorf("DisabledArkApiPlugins = %v，部分更新不应改动它", got.DisabledArkApiPlugins)
	}
}

// 禁用列表严格按实例，不参与实例间配置同步（方案 §1.1）——勾不勾「同步启用 ASA 插件」都一样。
func TestInstanceSyncLeavesDisabledPluginsAlone(t *testing.T) {
	orig := InstancesDir
	InstancesDir = t.TempDir()
	t.Cleanup(func() { InstancesDir = orig })

	mk := func(name string, disabled []string) {
		dir := filepath.Join(InstancesDir, name, "Config")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "GameUserSettings.ini"), []byte("[ServerSettings]\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := SaveInstanceConfig(name, &InstanceConfig{ServerName: name, EnableAsaPlugin: true, DisabledArkApiPlugins: disabled}); err != nil {
			t.Fatal(err)
		}
	}
	mk("src", []string{"FromSource"})
	mk("dst", []string{"Mine"})

	for _, syncPlugin := range []bool{false, true} {
		if err := SyncInstanceConfigFromSource("src", "dst", WithSyncEnableAsaPlugin(syncPlugin)); err != nil {
			t.Fatal(err)
		}
		got, err := LoadInstanceConfig("dst")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got.DisabledArkApiPlugins, []string{"Mine"}) {
			t.Errorf("syncEnableAsaPlugin=%v：目标实例的禁用列表被改成了 %v", syncPlugin, got.DisabledArkApiPlugins)
		}
	}
}
