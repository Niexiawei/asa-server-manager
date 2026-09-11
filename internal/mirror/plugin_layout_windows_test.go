//go:build windows

package mirror

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/plugindata"
)

// 这些用例都在 Windows 上用**真 NTFS junction** 跑（createJunction）：junction 与 symlink
// 在 Lstat/Mode 上的表现不同，只在 symlink 上测会漏掉只在 Windows 上出现的问题。
// 见 docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §12。

const layoutInst = "layoutinst"

// setupArkApiLayout 造一个装了 ArkApi 主程序与一个插件的 server-files，以及一个实例。
func setupArkApiLayout(t *testing.T) *cfgpkg.InstanceConfig {
	t.Helper()
	root := t.TempDir()
	origBase, origServerFiles, origInstances := cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir
	t.Cleanup(func() {
		cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir = origBase, origServerFiles, origInstances
	})
	cfgpkg.BaseDir = root
	cfgpkg.ServerFilesDir = filepath.Join(root, "server-files")
	cfgpkg.InstancesDir = filepath.Join(root, "instances")

	win64 := serverWin64()
	writeAt(t, filepath.Join(win64, "ArkAscendedServer.exe"), "exe")
	writeAt(t, filepath.Join(win64, asaApiLoaderName), "loader")
	writeAt(t, filepath.Join(win64, "ArkApi", "AsaApi.dll"), "api v1")
	writeAt(t, filepath.Join(win64, "ArkApi", "Plugins", "Permissions", "Permissions.dll"), "MZ v1")
	writeAt(t, filepath.Join(cfgpkg.InstancesDir, layoutInst, "Config", "GameUserSettings.ini"), "[x]")
	return &cfgpkg.InstanceConfig{}
}

func serverWin64() string {
	return filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64RelPath))
}

func mirrorPluginsPath() string {
	return filepath.Join(InstanceMirrorDir(layoutInst), filepath.FromSlash(win64RelPath), "ArkApi", "Plugins")
}

// migrateAndSync 走一遍启动路径上的顺序：先迁移，再同步。
func migrateAndSync(t *testing.T, cfg *cfgpkg.InstanceConfig) {
	t.Helper()
	if err := plugindata.MigrateInstance(layoutInst, InstanceMirrorDir(layoutInst)); err != nil {
		t.Fatalf("MigrateInstance: %v", err)
	}
	if _, err := SyncInstanceMirror(layoutInst, cfg); err != nil {
		t.Fatalf("SyncInstanceMirror: %v", err)
	}
}

// snapshotTree 把一棵目录树读成「相对路径 → 内容」，用来做逐文件不变的比对。
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out[rel] = readAt(t, p)
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s: %v", root, err)
	}
	return out
}

func hasEntry(entries []mirrorEntry, rel string, typ int) bool {
	return slices.ContainsFunc(entries, func(e mirrorEntry) bool { return e.RelPath == rel && e.EntryType == typ })
}

// 主程序已安装时，镜像里的 ArkApi/Plugins 是指向实例目录的 junction，ArkApi/ 本身仍是真实目录。
func TestArkApiPluginsLinkedToInstanceDir(t *testing.T) {
	cfg := setupArkApiLayout(t)
	migrateAndSync(t, cfg)

	mp := mirrorPluginsPath()
	if !hasReparsePointAttr(t, mp) {
		t.Fatal("镜像里的 ArkApi/Plugins 应是 junction")
	}
	arkDir := filepath.Dir(mp)
	if hasReparsePointAttr(t, arkDir) {
		t.Error("ArkApi 目录本身应是真实目录：主程序文件仍从 server-files 复制，全局一份")
	}
	if got := readAt(t, filepath.Join(arkDir, "AsaApi.dll")); got != "api v1" {
		t.Errorf("主程序文件应从 server-files 复制，实际 %q", got)
	}

	// 迁移把全局插件拷进了实例目录，经 junction 能读到；经 junction 写入的落在实例目录里
	if got := readAt(t, filepath.Join(mp, "Permissions", "Permissions.dll")); got != "MZ v1" {
		t.Errorf("经 junction 读到的插件 = %q", got)
	}
	writeAt(t, filepath.Join(mp, "Permissions", "ArkDB.db"), sqliteHeader+"live")
	instDB := filepath.Join(plugindata.InstancePluginsDir(layoutInst), "Permissions", "ArkDB.db")
	if got := readAt(t, instDB); got != sqliteHeader+"live" {
		t.Errorf("经 junction 写入的数据应落在实例目录，实际 %q", got)
	}

	// 第二轮同步：源侧目录存在，两侧都把它收成链接条目，diff 不会先删再建
	if _, err := SyncInstanceMirror(layoutInst, cfg); err != nil {
		t.Fatal(err)
	}
	if !hasReparsePointAttr(t, mp) {
		t.Fatal("第二轮同步后 junction 不见了")
	}
	rel := win64RelPath + "/ArkApi/Plugins"
	src, err := collectSourceEntries(cfgpkg.ServerFilesDir, buildExceptionTargets(layoutInst, cfg))
	if err != nil {
		t.Fatal(err)
	}
	mir, err := collectMirrorEntries(InstanceMirrorDir(layoutInst))
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(src, rel, EntryTypeSymlink) || !hasEntry(mir, rel, EntryTypeSymlink) {
		t.Errorf("两侧都应把 %s 收成链接条目，否则每轮同步都会删掉重建 junction", rel)
	}

	// Linux 降权靠这份清单给 junction 目标补权限
	if !slices.Contains(ExceptionTargets(layoutInst, cfg), plugindata.InstancePluginsDir(layoutInst)) {
		t.Error("ExceptionTargets 应包含实例插件目录")
	}
}

// 旧版本建出来的镜像里 Plugins 是真实目录，且留着上一轮的活数据：
// 迁移 + 同步之后它变成 junction，数据一样不少地进了实例目录。
func TestLegacyMirrorPluginsMigratedIntoInstanceDir(t *testing.T) {
	cfg := setupArkApiLayout(t)
	if err := ensureInstanceDirs(layoutInst); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath)), 0755); err != nil {
		t.Fatal(err)
	}

	// 模拟旧版本：建镜像时还没有 Plugins 这条例外
	legacyTargets := buildExceptionTargets(layoutInst, cfg)
	delete(legacyTargets, win64RelPath+"/ArkApi/Plugins")
	mirrorDir := InstanceMirrorDir(layoutInst)
	if _, err := createInstanceMirror(layoutInst, mirrorDir, legacyTargets); err != nil {
		t.Fatal(err)
	}
	mp := mirrorPluginsPath()
	if hasReparsePointAttr(t, mp) {
		t.Fatal("用例前提不成立：旧镜像里的 Plugins 应是真实目录")
	}
	writeAt(t, filepath.Join(mp, "Permissions", "ArkDB.db"), sqliteHeader+"live-from-last-run")
	writeAt(t, filepath.Join(mp, "Permissions", "ArkDB.db-wal"), "unflushed")
	writeAt(t, filepath.Join(mp, "Permissions", "stray.txt"), "runtime-note")
	writeAt(t, filepath.Join(cfgpkg.InstancesDir, layoutInst, "plugins", "Permissions", "config.json"), `{"UseMysql":true}`)

	migrateAndSync(t, cfg)

	if !hasReparsePointAttr(t, mp) {
		t.Fatal("同步后镜像里的 Plugins 应已换成 junction")
	}
	inst := filepath.Join(plugindata.InstancePluginsDir(layoutInst), "Permissions")
	for name, want := range map[string]string{
		"ArkDB.db":        sqliteHeader + "live-from-last-run",
		"ArkDB.db-wal":    "unflushed",
		"config.json":     `{"UseMysql":true}`,
		"stray.txt":       "runtime-note",
		"Permissions.dll": "MZ v1",
	} {
		if got := readAt(t, filepath.Join(inst, name)); got != want {
			t.Errorf("%s = %q，期望 %q", name, got, want)
		}
	}
}

// 主程序卸载后，镜像里内含 junction 的 Win64/ArkApi 被整个删除——实例插件目录必须一个文件都不少。
// 主程序装回来后 junction 自动恢复，插件原样可用。
func TestUninstallingCoreKeepsInstancePlugins(t *testing.T) {
	cfg := setupArkApiLayout(t)
	migrateAndSync(t, cfg)

	instDir := plugindata.InstancePluginsDir(layoutInst)
	writeAt(t, filepath.Join(instDir, "Permissions", "ArkDB.db"), sqliteHeader+"keep-me")
	before := snapshotTree(t, instDir)

	win64 := serverWin64()
	if err := os.RemoveAll(filepath.Join(win64, "ArkApi")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(win64, asaApiLoaderName)); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncInstanceMirror(layoutInst, cfg); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(filepath.Dir(mirrorPluginsPath())); !os.IsNotExist(err) {
		t.Error("主程序卸载后镜像里的 ArkApi 应被移除")
	}
	if after := snapshotTree(t, instDir); !maps.Equal(before, after) {
		t.Errorf("实例插件目录被改动了：\n  before %v\n  after  %v", before, after)
	}

	writeAt(t, filepath.Join(win64, asaApiLoaderName), "loader")
	writeAt(t, filepath.Join(win64, "ArkApi", "AsaApi.dll"), "api v2")
	if _, err := SyncInstanceMirror(layoutInst, cfg); err != nil {
		t.Fatal(err)
	}
	if !hasReparsePointAttr(t, mirrorPluginsPath()) {
		t.Fatal("主程序装回后 junction 应恢复")
	}
	if got := readAt(t, filepath.Join(mirrorPluginsPath(), "Permissions", "ArkDB.db")); got != sqliteHeader+"keep-me" {
		t.Errorf("装回后读到的插件数据 = %q", got)
	}
}

// 清理镜像只摘链接：实例插件目录不受影响，已退役的旧目录也不会被抢救逻辑重新建出来。
func TestCleanupLeavesInstancePluginsAlone(t *testing.T) {
	cfg := setupArkApiLayout(t)
	migrateAndSync(t, cfg)

	instDir := plugindata.InstancePluginsDir(layoutInst)
	writeAt(t, filepath.Join(instDir, "Permissions", "ArkDB.db"), sqliteHeader+"keep-me")
	before := snapshotTree(t, instDir)

	if err := CleanupInstanceMirror(layoutInst); err != nil {
		t.Fatal(err)
	}

	if after := snapshotTree(t, instDir); !maps.Equal(before, after) {
		t.Errorf("清理镜像改动了实例插件目录：\n  before %v\n  after  %v", before, after)
	}
	if _, err := os.Stat(filepath.Join(cfgpkg.InstancesDir, layoutInst, "plugins")); !os.IsNotExist(err) {
		t.Error("清理前的抢救穿过 junction 把活数据搬进了旧目录")
	}
}

// 结构性关断的「镜像里是链接」这一条判据，单独拎出来测：去掉迁移标记，只剩它在起作用。
//
// 这是 §12 要求的变异验证点：把 shuttleRetired 里的 fsutil.IsLink 换成 ModeSymlink 判定，
// 或者干脆去掉这条判据，本用例都必须失败——Inject 会拿旧目录里的过期副本穿过 junction
// 覆盖活数据，Rescue/Reclaim 会把活数据搬进已退役的旧目录。
func TestShuttleDoesNotRunThroughPluginsJunction(t *testing.T) {
	cfg := setupArkApiLayout(t)
	migrateAndSync(t, cfg)
	if err := os.Remove(plugindata.LayoutMarkerPath(layoutInst)); err != nil {
		t.Fatal(err)
	}

	liveDB := filepath.Join(plugindata.InstancePluginsDir(layoutInst), "Permissions", "ArkDB.db")
	writeAt(t, liveDB, sqliteHeader+"live")
	staleDB := filepath.Join(cfgpkg.InstancesDir, layoutInst, "plugins", "Permissions", "ArkDB.db")
	writeAt(t, staleDB, sqliteHeader+"stale")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(staleDB, old, old); err != nil {
		t.Fatal(err)
	}

	mirrorDir := InstanceMirrorDir(layoutInst)
	plugindata.Inject(layoutInst, mirrorDir)
	if got := readAt(t, liveDB); got != sqliteHeader+"live" {
		t.Errorf("Inject 穿过 junction 用旧副本覆盖了活数据: %q", got)
	}

	plugindata.Rescue(layoutInst, mirrorDir)
	plugindata.Reclaim(layoutInst, mirrorDir)
	if got := readAt(t, staleDB); got != sqliteHeader+"stale" {
		t.Errorf("Rescue/Reclaim 穿过 junction 把活数据搬进了已退役的旧目录: %q", got)
	}
}
