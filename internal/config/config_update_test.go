package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// setupTempInstance 在临时的 InstancesDir 下建一个实例并写入 cfg。
// LoadInstanceConfig 会顺带改写 GameUserSettings.ini 的 MOTD 段，文件不存在就报错，所以一并建好。
func setupTempInstance(t *testing.T, cfg *InstanceConfig) string {
	t.Helper()
	UseTempDirsForTest(t)

	const name = "partial-update"
	configDir := filepath.Join(InstancesDir, name, "Config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "GameUserSettings.ini"), []byte("[ServerSettings]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SaveInstanceConfig(name, cfg); err != nil {
		t.Fatal(err)
	}
	return name
}

func updateFromJSON(t *testing.T, name, body string) *InstanceConfig {
	t.Helper()
	// 走 JSON 解码而不是直接构造结构体：要验证的正是前端只发部分字段时，
	// 「没传」能被解码成 nil 而「传了空串」不是。
	var req UpdateInstanceConfigRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if err := UpdateInstanceConfig(name, req); err != nil {
		t.Fatal(err)
	}
	got, err := LoadInstanceConfig(name)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// 只提交部分字段时，没传的字段必须保持原值。ServerPassword / ModIDs 曾被无条件赋值，
// 只改快照周期或「启用ASA插件」就会把服务器密码和 Mod 列表清空。
func TestUpdateInstanceConfigPartialKeepsUnsentFields(t *testing.T) {
	name := setupTempInstance(t, &InstanceConfig{
		ServerName:     "srv",
		ServerPassword: "secret",
		ModIDs:         "111,222",
		MaxPlayers:     70,
		Port:           7777,
		RCONPort:       27020,
	})

	got := updateFromJSON(t, name, `{"EnableAsaPlugin": true}`)
	if !got.EnableAsaPlugin {
		t.Errorf("EnableAsaPlugin = false, want true")
	}
	if got.ServerPassword != "secret" {
		t.Errorf("ServerPassword = %q, want %q (not sent, must be kept)", got.ServerPassword, "secret")
	}
	if got.ModIDs != "111,222" {
		t.Errorf("ModIDs = %q, want %q (not sent, must be kept)", got.ModIDs, "111,222")
	}

	got = updateFromJSON(t, name, `{"PluginSnapshotInterval": 10}`)
	if got.PluginSnapshotInterval != 10 || got.ServerPassword != "secret" || got.ModIDs != "111,222" || !got.EnableAsaPlugin {
		t.Errorf("snapshot-only update touched other fields: %+v", got)
	}
}

// 显式传空串必须仍能清空密码与 Mod 列表——这是它们用指针而不是 omitempty 字符串的原因。
func TestUpdateInstanceConfigCanClearPasswordAndMods(t *testing.T) {
	name := setupTempInstance(t, &InstanceConfig{
		ServerName:      "srv",
		ServerPassword:  "secret",
		ModIDs:          "111,222",
		EnableAsaPlugin: true,
	})

	got := updateFromJSON(t, name, `{"ServerPassword": "", "ModIDs": ""}`)
	if got.ServerPassword != "" {
		t.Errorf("ServerPassword = %q, want empty", got.ServerPassword)
	}
	if got.ModIDs != "" {
		t.Errorf("ModIDs = %q, want empty", got.ModIDs)
	}
	if !got.EnableAsaPlugin {
		t.Errorf("EnableAsaPlugin was cleared by an update that did not send it")
	}
}

// UseTempDirsForTest 五个目录变量一起换、一起还原（docs/TEST_ENV_COUPLING_PLAN.md T8）。
func TestUseTempDirsForTest(t *testing.T) {
	before := [5]string{BaseDir, InstancesDir, ServerFilesDir, SteamCmdDir, BackupsDir}
	t.Run("inner", func(t *testing.T) {
		root := UseTempDirsForTest(t)
		got := [5]string{BaseDir, InstancesDir, ServerFilesDir, SteamCmdDir, BackupsDir}
		want := [5]string{root, filepath.Join(root, "instances"), filepath.Join(root, "server-files"),
			filepath.Join(root, "steamcmd"), filepath.Join(root, "backups")}
		if got != want {
			t.Errorf("目录变量 = %v，期望 %v", got, want)
		}
		if _, err := os.Stat(InstancesDir); !os.IsNotExist(err) {
			t.Errorf("只设变量、不应建目录: %v", err)
		}
	})
	if after := [5]string{BaseDir, InstancesDir, ServerFilesDir, SteamCmdDir, BackupsDir}; after != before {
		t.Errorf("用例结束后应原样还原：之前 %v，之后 %v", before, after)
	}
}
