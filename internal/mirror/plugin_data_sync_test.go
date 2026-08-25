//go:build windows

package mirror

import (
	"os"
	"path/filepath"
	"testing"

	cfgpkg "asa-server/internal/config"
)

const pluginRelDir = win64RelPath + "/ArkApi/Plugins/Permissions"

// 增量同步必须把插件的配置与运行期数据排除在**回写与删除**之外。
//
// 这是整件事的原始 bug：reconcileEntry 对真实文件做 MD5 比对，不一致就从源拷回来。
// 实例运行期写进去的权限库与源版本 MD5 必然不同，于是每次重启权限都被重置回源目录那一份。
// 删除那一半同样致命：ArkDB.db-wal 在源目录里根本不存在，会被当成多余条目删掉，
// 而数据大部分就压在 -wal 里（实测主库 4 KB，-wal 1.9 MB）。
//
// 插件二进制则必须照常同步，否则插件更新永远生效不了 —— 用它做对照组。
func TestSyncKeepsPluginDataButStillUpdatesBinaries(t *testing.T) {
	mirrorDir, exceptionTargets := setupPluginMirror(t)

	mirrorPluginDir := filepath.Join(mirrorDir, filepath.FromSlash(pluginRelDir))
	srcPluginDir := filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(pluginRelDir))

	// 模拟一轮运行：实例改了配置、写了权限库，并留下未 checkpoint 的 -wal
	writeAt(t, filepath.Join(mirrorPluginDir, "config.json"), `{"ClusterSyncTime":300}`)
	writeAt(t, filepath.Join(mirrorPluginDir, "ArkDB.db"), sqliteHeader+"instance-permissions")
	writeAt(t, filepath.Join(mirrorPluginDir, "ArkDB.db-wal"), "unflushed-write-ahead-log")
	// 对照组：插件二进制在源侧被更新了（模拟用户升级插件）
	writeAt(t, filepath.Join(srcPluginDir, "Permissions.dll"), "MZ v2")

	// 连跑两轮，覆盖「镜像已存在」的稳定态
	for i := range 2 {
		if err := syncMirrorEntries(mirrorDir, exceptionTargets); err != nil {
			t.Fatalf("第 %d 轮同步失败: %v", i+1, err)
		}
	}

	if got := readAt(t, filepath.Join(mirrorPluginDir, "config.json")); got != `{"ClusterSyncTime":300}` {
		t.Errorf("插件配置被源版本回写覆盖了: %q", got)
	}
	if got := readAt(t, filepath.Join(mirrorPluginDir, "ArkDB.db")); got != sqliteHeader+"instance-permissions" {
		t.Errorf("权限库被源版本回写覆盖了（这正是「每次重启权限被重置」的成因）: %q", got)
	}
	if _, err := os.Stat(filepath.Join(mirrorPluginDir, "ArkDB.db-wal")); err != nil {
		t.Errorf("源目录里没有 -wal，被当成多余条目删掉了 —— 数据大部分在里面: %v", err)
	}
	if got := readAt(t, filepath.Join(mirrorPluginDir, "Permissions.dll")); got != "MZ v2" {
		t.Errorf("插件二进制必须照常同步，否则插件更新生效不了，实际 %q", got)
	}
}

// 清理镜像之前必须先抢救插件数据 —— 正常停止走 Reclaim，
// 但强杀、同步失败重建这些路径只会走到 CleanupInstanceMirror。
// 把抢救放进它的开头而不是散在 7 个调用点上，是因为漏掉任何一个都会静默丢数据。
func TestCleanupRescuesPluginDataFirst(t *testing.T) {
	mirrorDir, _ := setupPluginMirror(t)
	mirrorPluginDir := filepath.Join(mirrorDir, filepath.FromSlash(pluginRelDir))

	writeAt(t, filepath.Join(mirrorPluginDir, "ArkDB.db"), sqliteHeader+"about-to-be-destroyed")
	writeAt(t, filepath.Join(mirrorPluginDir, "ArkDB.db-wal"), "crash-payload")

	if err := CleanupInstanceMirror("pluginst"); err != nil {
		t.Fatalf("清理镜像: %v", err)
	}

	rescued := filepath.Join(cfgpkg.InstancesDir, "pluginst", "plugins", "Permissions")
	if got := readAt(t, filepath.Join(rescued, "ArkDB.db")); got != sqliteHeader+"about-to-be-destroyed" {
		t.Errorf("清理前应先把权限库抢救到实例目录，实际 %q", got)
	}
	if got := readAt(t, filepath.Join(rescued, "ArkDB.db-wal")); got != "crash-payload" {
		t.Errorf("-wal 必须随主库一起抢救，实际 %q", got)
	}
}

// ---------- 脚手架 ----------

// sqliteHeader 是 SQLite 数据库文件头。识别走的是魔数而不是扩展名，
// 所以测试里不需要真的建库。
const sqliteHeader = "SQLite format 3\x00"

// setupPluginMirror 造一个装了 ArkApi 插件的源目录并建好镜像。
func setupPluginMirror(t *testing.T) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()

	origBase, origServerFiles, origInstances := cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir
	t.Cleanup(func() {
		cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir = origBase, origServerFiles, origInstances
	})
	cfgpkg.BaseDir = root
	cfgpkg.ServerFilesDir = filepath.Join(root, "server-files")
	cfgpkg.InstancesDir = filepath.Join(root, "instances")

	srcPluginDir := filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(pluginRelDir))
	writeAt(t, filepath.Join(srcPluginDir, "config.json"), `{"ClusterSyncTime":60}`)
	writeAt(t, filepath.Join(srcPluginDir, "ArkDB.db"), sqliteHeader+"shipped-default")
	writeAt(t, filepath.Join(srcPluginDir, "Permissions.dll"), "MZ v1")
	writeAt(t, filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame", "Binaries", "Win64", "ArkAscendedServer.exe"), "exe")

	instName := "pluginst"
	instCfg := filepath.Join(cfgpkg.InstancesDir, instName, "Config")
	if err := os.MkdirAll(instCfg, 0755); err != nil {
		t.Fatalf("建实例目录: %v", err)
	}
	writeAt(t, filepath.Join(instCfg, "GameUserSettings.ini"), "[x]")
	if err := os.MkdirAll(filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath)), 0755); err != nil {
		t.Fatalf("建共享 mods 目录: %v", err)
	}

	exceptionTargets := map[string]string{
		"ShooterGame/Saved/Config/WindowsServer": instCfg,
		win64SharedRelPath:                       filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath)),
	}

	mirrorDir := InstanceMirrorDir(instName)
	if _, err := createInstanceMirror(instName, mirrorDir, exceptionTargets); err != nil {
		t.Fatalf("创建镜像失败: %v", err)
	}
	return mirrorDir, exceptionTargets
}

func writeAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("建目录 %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写文件 %s: %v", path, err)
	}
}

func readAt(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件 %s: %v", path, err)
	}
	return string(b)
}
