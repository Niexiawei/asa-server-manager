package plugindata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 插件更新：config.json 旧值优先、新键并入且保序；数据文件整组带过去；其余以新包为准。
func TestCarryOverMergesConfigAndKeepsData(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(oldDir, configFileName), `{"A":"user","B":1}`)
	writeSQLite(t, filepath.Join(oldDir, "ArkDB.db"), "live-db")
	writeFile(t, filepath.Join(oldDir, "ArkDB.db-wal"), "live-wal")
	writeFile(t, filepath.Join(oldDir, "old-only.txt"), "stale")

	writeFile(t, filepath.Join(newDir, configFileName), `{"A":"default","B":2,"C":"new"}`)
	writeSQLite(t, filepath.Join(newDir, "ArkDB.db"), "seed-db")
	writeFile(t, filepath.Join(newDir, "ArkDB.db-shm"), "seed-shm")

	warnings, err := CarryOverPluginState(oldDir, newDir, "P")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("err=%v warnings=%v", err, warnings)
	}

	cfg := readFile(t, filepath.Join(newDir, configFileName))
	iA, iB, iC := strings.Index(cfg, `"A": "user"`), strings.Index(cfg, `"B": 1`), strings.Index(cfg, `"C": "new"`)
	if iA < 0 || iB < 0 || iC < 0 || !(iA < iB && iB < iC) {
		t.Errorf("合并后的配置不对（旧值优先、新键并入、保序）:\n%s", cfg)
	}
	if got := readFile(t, filepath.Join(newDir, "ArkDB.db")); !strings.HasSuffix(got, "live-db") {
		t.Errorf("数据库应是实例正在用的那份，实际 %q", got)
	}
	if got := readFile(t, filepath.Join(newDir, "ArkDB.db-wal")); got != "live-wal" {
		t.Errorf("-wal 必须随主库带过来，实际 %q", got)
	}
	if _, err := os.Stat(filepath.Join(newDir, "ArkDB.db-shm")); !os.IsNotExist(err) {
		t.Error("新包里种子库的 -shm 必须随整组替换删掉")
	}
	if _, err := os.Stat(filepath.Join(newDir, "old-only.txt")); !os.IsNotExist(err) {
		t.Error("旧目录独有的非数据文件不应带进新版本")
	}
}

// 新版本没带来新键时原样保留用户的文件：合并会统一缩进，不能因为一次更新改写用户手排的格式。
func TestCarryOverKeepsConfigBytesWhenNothingNew(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	const userCfg = "{\n    \"A\": \"user\",   \"B\": 1\n}"
	writeFile(t, filepath.Join(oldDir, configFileName), userCfg)
	writeFile(t, filepath.Join(newDir, configFileName), `{"A":"default"}`)

	if _, err := CarryOverPluginState(oldDir, newDir, "P"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(newDir, configFileName)); got != userCfg {
		t.Errorf("没有新键时应逐字节保留旧配置，实际:\n%s", got)
	}
}

func TestCarryOverConfigEdgeCases(t *testing.T) {
	t.Run("新包没带配置", func(t *testing.T) {
		oldDir, newDir := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(oldDir, configFileName), `{"A":1}`)
		if _, err := CarryOverPluginState(oldDir, newDir, "P"); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, filepath.Join(newDir, configFileName)); got != `{"A":1}` {
			t.Errorf("应保留旧配置，实际 %q", got)
		}
	})
	t.Run("旧配置不是合法 JSON", func(t *testing.T) {
		oldDir, newDir := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(oldDir, configFileName), `{"A":1,}`)
		writeFile(t, filepath.Join(newDir, configFileName), `{"A":2}`)
		warnings, err := CarryOverPluginState(oldDir, newDir, "P")
		if err != nil {
			t.Fatal(err)
		}
		if len(warnings) != 1 {
			t.Errorf("合并失败应当警告，warnings=%v", warnings)
		}
		if got := readFile(t, filepath.Join(newDir, configFileName)); got != `{"A":1,}` {
			t.Errorf("合并失败时应保留旧配置原文（那是用户改过的），实际 %q", got)
		}
	})
}
