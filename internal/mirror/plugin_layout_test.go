package mirror

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/plugindata"
	"asa-server/pkg/arkcache"
)

// 这些用例两个平台都跑，链接都由 createJunction 建出：Windows 上是**真 NTFS junction**，
// Linux 上是 symlink（WSL2 下用 `wsl -e zsh -lc 'cd /mnt/d/golang/asa-server && go test ./internal/mirror/'`）。
// 两者在 Lstat/Mode 上的表现不同，只在一边测会漏掉只在另一边出现的问题。
// 「盘上是不是链接」的判断走 isLinkOnDisk（linkattr_{windows,linux}_test.go），不经过被测对象。
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
	if !isLinkOnDisk(t, mp) {
		t.Fatal("镜像里的 ArkApi/Plugins 应是 junction")
	}
	arkDir := filepath.Dir(mp)
	if isLinkOnDisk(t, arkDir) {
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
	if !isLinkOnDisk(t, mp) {
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
// 迁移 + 同步之后它变成 junction，数据一样不少地进了实例目录；
// 而镜像独有的**非数据**文件与旧流程一样被丢弃，不会被带进实例目录。
func TestLegacyMirrorPluginsMigratedIntoInstanceDir(t *testing.T) {
	cfg := setupArkApiLayout(t)
	if err := ensureInstanceDirs(layoutInst); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath)), 0755); err != nil {
		t.Fatal(err)
	}
	// 复刻真实数据里的情形：Chat 插件上次同步时还在，之后用户从 server-files 删掉了它的 dll
	srcChat := filepath.Join(serverWin64(), "ArkApi", "Plugins", "Chat")
	writeAt(t, filepath.Join(srcChat, "Chat.dll"), "MZ chat")
	writeAt(t, filepath.Join(srcChat, "PluginInfo.json"), `{"FullName":"Chat"}`)

	// 模拟旧版本：建镜像时还没有 Plugins 这条例外
	legacyTargets := buildExceptionTargets(layoutInst, cfg)
	delete(legacyTargets, win64RelPath+"/ArkApi/Plugins")
	mirrorDir := InstanceMirrorDir(layoutInst)
	if _, err := createInstanceMirror(layoutInst, mirrorDir, legacyTargets); err != nil {
		t.Fatal(err)
	}
	mp := mirrorPluginsPath()
	if isLinkOnDisk(t, mp) {
		t.Fatal("用例前提不成立：旧镜像里的 Plugins 应是真实目录")
	}
	writeAt(t, filepath.Join(mp, "Permissions", "ArkDB.db"), sqliteHeader+"live-from-last-run")
	writeAt(t, filepath.Join(mp, "Permissions", "ArkDB.db-wal"), "unflushed")
	writeAt(t, filepath.Join(mp, "Permissions", "stray.txt"), "runtime-note")
	writeAt(t, filepath.Join(cfgpkg.InstancesDir, layoutInst, "plugins", "Permissions", "config.json"), `{"UseMysql":true}`)
	if err := os.Remove(filepath.Join(srcChat, "Chat.dll")); err != nil {
		t.Fatal(err)
	}

	migrateAndSync(t, cfg)

	if !isLinkOnDisk(t, mp) {
		t.Fatal("同步后镜像里的 Plugins 应已换成 junction")
	}
	inst := filepath.Join(plugindata.InstancePluginsDir(layoutInst), "Permissions")
	for name, want := range map[string]string{
		"ArkDB.db":        sqliteHeader + "live-from-last-run",
		"ArkDB.db-wal":    "unflushed",
		"config.json":     `{"UseMysql":true}`,
		"Permissions.dll": "MZ v1",
	} {
		if got := readAt(t, filepath.Join(inst, name)); got != want {
			t.Errorf("%s = %q，期望 %q", name, got, want)
		}
	}
	// 与旧流程一致：镜像独有的非数据文件在旧流程里会被同步当成多余条目删掉，这里不能被带进实例目录
	if _, err := os.Stat(filepath.Join(inst, "stray.txt")); !os.IsNotExist(err) {
		t.Error("镜像独有的非数据文件被带进了实例目录（旧流程会删掉它）")
	}
	instChat := filepath.Join(plugindata.InstancePluginsDir(layoutInst), "Chat")
	if _, err := os.Stat(filepath.Join(instChat, "Chat.dll")); !os.IsNotExist(err) {
		t.Error("server-files 里已删除的 Chat.dll 被旧镜像复活进了实例目录——迁移后这个插件会重新被加载")
	}
	if got := readAt(t, filepath.Join(instChat, "PluginInfo.json")); got != `{"FullName":"Chat"}` {
		t.Errorf("Chat 的其余文件应照 server-files 迁移，实际 %q", got)
	}
}

// 未迁移的实例（正常启动路径不会这样同步，这里防的是任何绕过了迁移的同步）必须维持旧的镜像行为：
// 不建 Plugins junction，镜像真实目录里的插件数据照旧受同步保护。
// 若在这种状态下建了 junction，目标是个空目录，镜像里的活数据会随真实目录一起被删掉。
func TestUnmigratedInstanceKeepsLegacyMirrorBehavior(t *testing.T) {
	cfg := setupArkApiLayout(t)
	if _, err := SyncInstanceMirror(layoutInst, cfg); err != nil {
		t.Fatal(err)
	}
	mp := mirrorPluginsPath()
	if isLinkOnDisk(t, mp) {
		t.Fatal("未迁移的实例不能建 Plugins junction：它的数据还在镜像的真实目录里")
	}

	db := filepath.Join(mp, "Permissions", "ArkDB.db")
	wal := filepath.Join(mp, "Permissions", "ArkDB.db-wal")
	writeAt(t, db, sqliteHeader+"live")
	writeAt(t, wal, "wal")
	if _, err := SyncInstanceMirror(layoutInst, cfg); err != nil {
		t.Fatal(err)
	}
	if got := readAt(t, db); got != sqliteHeader+"live" {
		t.Errorf("未迁移实例镜像里的插件数据被同步改动了: %q", got)
	}
	if _, err := os.Stat(wal); err != nil {
		t.Errorf("未迁移实例镜像里的 -wal 被同步删掉了: %v", err)
	}
	if _, err := os.Stat(plugindata.InstancePluginsDir(layoutInst)); !os.IsNotExist(err) {
		t.Error("未迁移的实例不该被建出新布局的插件目录")
	}
	if slices.Contains(ExceptionTargets(layoutInst, cfg), plugindata.InstancePluginsDir(layoutInst)) {
		t.Error("未迁移的实例的 ExceptionTargets 不该包含新布局的插件目录")
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
	if !isLinkOnDisk(t, mirrorPluginsPath()) {
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

// Plugins 链接在场时，ArkApi/Cache 的两条既有规则都不能变：接管后源目录是权威、要对账；
// 没接管时 Cache 里是 ArkApi 运行期自己写的东西、不删不比对（arkapi_cache_sync_test.go 的两个用例
// 用的是手工拼的例外清单，没有这条链接，覆盖不到这里）。
// Win64/ArkApi 从「Win64 下的普通真实目录」变成了「有例外子路径的中间目录」，两种身份下
// 它都是真实目录，Cache 的相对路径判定也不变——这里用结果钉住。
func TestArkApiCacheRulesUnaffectedByPluginsJunction(t *testing.T) {
	t.Run("managed", func(t *testing.T) {
		cfg := setupArkApiLayout(t)
		hash, genRel := seedSourceArkApiCache(t)
		migrateAndSync(t, cfg)

		mirrorCache := filepath.Join(InstanceMirrorDir(layoutInst), filepath.FromSlash(arkApiCacheDirRel))
		staleHash := strings.Repeat("a", 64)
		staleGen := fmt.Sprintf("generations/%s-1-1-0", staleHash)
		writeAt(t, filepath.Join(mirrorCache, filepath.FromSlash(staleGen), "cached_offsets.cache"), "old")
		writeAt(t, filepath.Join(mirrorCache, keyFileName), fmt.Sprintf(
			`{"version":1,"executable_hash":%q,"last_modified":"LM-old","cache_directory":%q}`, staleHash, staleGen))

		if _, err := SyncInstanceMirror(layoutInst, cfg); err != nil {
			t.Fatal(err)
		}
		if res, err := arkcache.Inspect(mirrorCache, hash); err != nil || !res.Ready || res.Generation != genRel {
			t.Errorf("接管后镜像里的 cached_key.cache 应被回写: %v %+v", err, res)
		}
		if _, err := os.Stat(filepath.Join(mirrorCache, filepath.FromSlash(staleGen))); err == nil {
			t.Error("接管后镜像里的旧 generation 应被删掉")
		}
		if !isLinkOnDisk(t, mirrorPluginsPath()) {
			t.Error("Cache 对账之后 Plugins junction 不见了")
		}
	})

	t.Run("unmanaged", func(t *testing.T) {
		cfg := setupArkApiLayout(t)
		writeAt(t, filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(arkApiCacheDirRel), keyFileName), "source-side-key")
		migrateAndSync(t, cfg)

		mirrorCache := filepath.Join(InstanceMirrorDir(layoutInst), filepath.FromSlash(arkApiCacheDirRel))
		runtimeFile := filepath.Join(mirrorCache, "generations", "runtime-gen", "cached_offsets.cache")
		writeAt(t, runtimeFile, "downloaded-by-arkapi")
		writeAt(t, filepath.Join(mirrorCache, keyFileName), "written-by-arkapi")

		if _, err := SyncInstanceMirror(layoutInst, cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(runtimeFile); err != nil {
			t.Errorf("未接管时 ArkApi 运行期写入的文件被删了: %v", err)
		}
		if got := readAt(t, filepath.Join(mirrorCache, keyFileName)); got != "written-by-arkapi" {
			t.Errorf("未接管时 ArkApi 自己的 cached_key.cache 被源版本覆盖了: %q", got)
		}
	})
}

// 结构性关断的「镜像里是链接」这一条判据，单独拎出来测：去掉迁移标记，只剩它在起作用。
//
// 这是 §12 要求的变异验证点：把 shuttleRetired 里的 fsutil.IsLink 换成 ModeSymlink 判定，
// 或者干脆去掉这条判据，本用例都必须失败——Inject 会拿旧目录里的过期副本穿过 junction
// 覆盖活数据，Rescue/Reclaim 会把活数据搬进已退役的旧目录。
// 注意「换成 ModeSymlink」只在 Windows 上会失败：Linux 上链接是 symlink，ModeSymlink 本来就对，
// 这正是这个 bug 只在 Windows 上出现的原因；「去掉判据」则两个平台都会失败。
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
