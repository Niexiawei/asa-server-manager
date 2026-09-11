package plugindata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cfgpkg "asa-server/internal/config"
)

// setupLayoutEnv 把 cfgpkg 的三个目录变量指到临时目录，返回根目录。
func setupLayoutEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origBase, origServerFiles, origInstances := cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir
	t.Cleanup(func() {
		cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir = origBase, origServerFiles, origInstances
	})
	cfgpkg.BaseDir = root
	cfgpkg.ServerFilesDir = filepath.Join(root, "server-files")
	cfgpkg.InstancesDir = filepath.Join(root, "instances")
	return root
}

func srcPluginDir(plugin string) string {
	return filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(pluginsRelPath), plugin)
}

func mustMigrate(t *testing.T, inst, mirrorDir string) {
	t.Helper()
	if err := MigrateInstance(inst, mirrorDir); err != nil {
		t.Fatalf("MigrateInstance: %v", err)
	}
}

func retiredLegacyDirs(t *testing.T, inst string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(cfgpkg.InstancesDir, inst, legacyRetiredPrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// 迁移后每个实例加载的插件与数据必须和迁移前完全一致（方案 D5）：
// server-files 的全局插件拷一份，再用实例自己的配置与数据**整组**覆盖。
func TestMigrateOverlaysInstanceDataOnGlobalPlugins(t *testing.T) {
	setupLayoutEnv(t)
	const inst = "alpha"

	src := srcPluginDir("Permissions")
	writeFile(t, filepath.Join(src, "Permissions.dll"), "MZ v1")
	writeFile(t, filepath.Join(src, configFileName), `{"UseMysql":false}`)
	writeSQLite(t, filepath.Join(src, "ArkDB.db"), "shipped-seed")
	writeFile(t, filepath.Join(src, "ArkDB.db-shm"), "seed-shm")

	legacy := filepath.Join(legacyPluginsDir(inst), "Permissions")
	writeFile(t, filepath.Join(legacy, configFileName), `{"UseMysql":true}`)
	writeSQLite(t, filepath.Join(legacy, "ArkDB.db"), "instance-db")
	writeFile(t, filepath.Join(legacy, "ArkDB.db-wal"), "instance-wal")
	writeSQLite(t, filepath.Join(legacy, snapshotsDirName, "ArkDB.db"), "instance-snapshot")

	mustMigrate(t, inst, "")

	dst := filepath.Join(InstancePluginsDir(inst), "Permissions")
	if got := readFile(t, filepath.Join(dst, "Permissions.dll")); got != "MZ v1" {
		t.Errorf("插件二进制应从 server-files 拷来，实际 %q", got)
	}
	if got := readFile(t, filepath.Join(dst, configFileName)); got != `{"UseMysql":true}` {
		t.Errorf("配置应是实例自己的那份，实际 %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "ArkDB.db")); !strings.HasSuffix(got, "instance-db") {
		t.Errorf("权限库应是实例自己的那份，实际 %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "ArkDB.db-wal")); got != "instance-wal" {
		t.Errorf("-wal 必须随主库一起迁移（数据大多压在里面），实际 %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "ArkDB.db-shm")); !os.IsNotExist(err) {
		t.Error("种子库的 -shm 必须随整组替换删掉，否则会与实例的主库拼成互不匹配的组合")
	}
	snap := filepath.Join(InstanceSnapshotsDir(inst), "Permissions", "ArkDB.db")
	if got := readFile(t, snap); !strings.HasSuffix(got, "instance-snapshot") {
		t.Errorf("快照应迁到 PluginSnapshots，实际 %q", got)
	}

	if !IsMigrated(inst) {
		t.Error("迁移完成后应写上标记")
	}
	if _, err := os.Stat(legacyPluginsDir(inst)); !os.IsNotExist(err) {
		t.Error("旧的 plugins/ 应改名保留，而不是原地留着")
	}
	if dirs := retiredLegacyDirs(t, inst); len(dirs) != 1 {
		t.Errorf("应恰好有一个 plugins.legacy-* 目录，实际 %v", dirs)
	}
	if got := readFile(t, filepath.Join(src, "Permissions.dll")); got != "MZ v1" {
		t.Errorf("迁移不得改动 server-files，实际 %q", got)
	}
}

// 上一轮崩溃退出时，镜像里留着的才是最新数据。迁移必须先按既有规则把它抢救回来，
// 否则会用旧目录里的陈旧副本静默盖掉崩溃前的数据。
func TestMigrateTakesNewerDataLeftInMirror(t *testing.T) {
	root := setupLayoutEnv(t)
	const inst = "alpha"

	writeFile(t, filepath.Join(srcPluginDir("Permissions"), "Permissions.dll"), "MZ v1")

	legacyDB := filepath.Join(legacyPluginsDir(inst), "Permissions", "ArkDB.db")
	writeSQLite(t, legacyDB, "old-instance-copy")
	touch(t, legacyDB, time.Now().Add(-2*time.Hour))

	mirrorDir := filepath.Join(root, "server-files-tmp-"+inst)
	mirrorPlugin := filepath.Join(MirrorPluginsDir(mirrorDir), "Permissions")
	writeSQLite(t, filepath.Join(mirrorPlugin, "ArkDB.db"), "crashed-main")
	writeFile(t, filepath.Join(mirrorPlugin, "ArkDB.db-wal"), "crashed-wal")

	mustMigrate(t, inst, mirrorDir)

	dst := filepath.Join(InstancePluginsDir(inst), "Permissions")
	if got := readFile(t, filepath.Join(dst, "ArkDB.db")); !strings.HasSuffix(got, "crashed-main") {
		t.Errorf("镜像里的崩溃现场更新，应以它为准，实际 %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "ArkDB.db-wal")); got != "crashed-wal" {
		t.Errorf("-wal 必须随主库一起迁移，实际 %q", got)
	}
}

// 迁移可能在任意一步被打断，重跑必须得到正确结果。
func TestMigrateResumesAfterInterruption(t *testing.T) {
	setupLayoutEnv(t)
	const inst = "alpha"
	writeFile(t, filepath.Join(srcPluginDir("Permissions"), "Permissions.dll"), "MZ v1")

	// 上一次在组装中途被打断
	writeFile(t, filepath.Join(InstanceArkApiDir(inst), migratingDirName, "Half", "junk"), "x")
	// 镜像同步抢先建出了空的 junction 目标——这正是不能拿「目录存在」当迁移标记的原因
	if err := os.MkdirAll(InstancePluginsDir(inst), 0755); err != nil {
		t.Fatal(err)
	}

	mustMigrate(t, inst, "")

	if _, err := os.Stat(filepath.Join(InstanceArkApiDir(inst), migratingDirName)); !os.IsNotExist(err) {
		t.Error("组装目录应在提交后消失")
	}
	if _, err := os.Stat(filepath.Join(InstancePluginsDir(inst), "Half")); !os.IsNotExist(err) {
		t.Error("上一次中断留下的半成品不应混进迁移结果")
	}
	if got := readFile(t, filepath.Join(InstancePluginsDir(inst), "Permissions", "Permissions.dll")); got != "MZ v1" {
		t.Errorf("迁移结果不完整: %q", got)
	}
}

// 目标目录里有意料之外的内容（没有标记）时，宁可改名保留也不能删。
func TestMigrateMovesAsideUnexpectedContent(t *testing.T) {
	setupLayoutEnv(t)
	const inst = "alpha"
	writeFile(t, filepath.Join(srcPluginDir("Permissions"), "Permissions.dll"), "MZ v1")
	writeFile(t, filepath.Join(InstancePluginsDir(inst), "Stray", "stray.txt"), "keep-me")

	mustMigrate(t, inst, "")

	aside, _ := filepath.Glob(InstancePluginsDir(inst) + ".pre-migration-*")
	if len(aside) != 1 {
		t.Fatalf("意料之外的内容应被改名保留，实际 %v", aside)
	}
	if got := readFile(t, filepath.Join(aside[0], "Stray", "stray.txt")); got != "keep-me" {
		t.Errorf("保留的内容被改动了: %q", got)
	}
	if _, err := os.Stat(filepath.Join(InstancePluginsDir(inst), "Permissions", "Permissions.dll")); err != nil {
		t.Errorf("迁移结果应已落位: %v", err)
	}
}

// 已迁移的实例再调用只是 stat 一下标记，绝不能重新从 server-files 拷一遍覆盖。
func TestMigrateIsIdempotent(t *testing.T) {
	setupLayoutEnv(t)
	const inst = "alpha"
	writeFile(t, filepath.Join(srcPluginDir("Permissions"), "Permissions.dll"), "MZ v1")
	mustMigrate(t, inst, "")

	live := filepath.Join(InstancePluginsDir(inst), "Permissions", "Permissions.dll")
	writeFile(t, live, "MZ v2-installed-per-instance")
	mustMigrate(t, inst, "")

	if got := readFile(t, live); got != "MZ v2-installed-per-instance" {
		t.Errorf("已迁移的实例被重新迁移覆盖了: %q", got)
	}
}

// 只在旧目录里有、server-files 里已卸载的插件：二进制没了，不迁移，数据随旧目录保留。
func TestMigrateSkipsPluginsNoLongerInstalled(t *testing.T) {
	setupLayoutEnv(t)
	const inst = "alpha"
	writeFile(t, filepath.Join(srcPluginDir("Permissions"), "Permissions.dll"), "MZ v1")
	writeSQLite(t, filepath.Join(legacyPluginsDir(inst), "Gone", "ArkDB.db"), "orphan")

	mustMigrate(t, inst, "")

	if _, err := os.Stat(filepath.Join(InstancePluginsDir(inst), "Gone")); !os.IsNotExist(err) {
		t.Error("已卸载的插件不该出现在迁移结果里（那会是一个没有 dll 的插件）")
	}
	dirs := retiredLegacyDirs(t, inst)
	if len(dirs) != 1 {
		t.Fatalf("旧目录应改名保留，实际 %v", dirs)
	}
	if got := readFile(t, filepath.Join(dirs[0], "Gone", "ArkDB.db")); !strings.HasSuffix(got, "orphan") {
		t.Errorf("已卸载插件的数据应随旧目录保留，实际 %q", got)
	}
}

// 旧方案推荐过把 DbPathOverride 指向实例目录。旧目录改名后那个路径悬空，
// 插件会在原处新建空库——权限静默清零。迁移必须改写它，且保持键顺序。
func TestMigrateRewritesDbPathOverridePointingIntoLegacyDir(t *testing.T) {
	setupLayoutEnv(t)
	const inst = "alpha"
	external := filepath.Join(t.TempDir(), "elsewhere")

	cases := []struct {
		plugin, override, want string
	}{
		{"PermA", filepath.Join(legacyPluginsDir(inst), "PermA"), ""},
		{"PermB", filepath.Join(legacyPluginsDir(inst), "PermB", "db"), filepath.Join(InstancePluginsDir(inst), "PermB", "db")},
		{"PermC", external, external},
	}
	for _, c := range cases {
		writeFile(t, filepath.Join(srcPluginDir(c.plugin), c.plugin+".dll"), "MZ")
		override, _ := json.Marshal(c.override)
		writeFile(t, filepath.Join(legacyPluginsDir(inst), c.plugin, configFileName),
			`{"UseMysql":false,"DbPathOverride":`+string(override)+`,"ClusterSyncTime":60}`)
	}

	mustMigrate(t, inst, "")

	for _, c := range cases {
		data := readFile(t, filepath.Join(InstancePluginsDir(inst), c.plugin, configFileName))
		obj, err := parseOrderedObject([]byte(data))
		if err != nil {
			t.Fatalf("%s: 迁移后的配置不是合法 JSON 对象: %v", c.plugin, err)
		}
		var keys []string
		for _, m := range obj {
			keys = append(keys, m.Key)
		}
		if strings.Join(keys, ",") != "UseMysql,DbPathOverride,ClusterSyncTime" {
			t.Errorf("%s: 键顺序被打乱: %v", c.plugin, keys)
		}
		var got string
		if err := json.Unmarshal(obj[1].Raw, &got); err != nil {
			t.Fatalf("%s: DbPathOverride 不是字符串: %v", c.plugin, err)
		}
		if got != c.want {
			t.Errorf("%s: DbPathOverride = %q，期望 %q", c.plugin, got, c.want)
		}
	}
}

// 既没有全局插件也没有旧数据：迁移只是建出空目录并写上标记。
func TestMigrateWithNothingToMigrateCreatesEmptyLayout(t *testing.T) {
	setupLayoutEnv(t)
	const inst = "alpha"
	mustMigrate(t, inst, "")

	entries, err := os.ReadDir(InstancePluginsDir(inst))
	if err != nil {
		t.Fatalf("插件目录应已建出: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("插件目录应为空，实际 %d 项", len(entries))
	}
	if LayoutOf(inst) != LayoutInstance {
		t.Errorf("LayoutOf = %q，期望 %q", LayoutOf(inst), LayoutInstance)
	}
}

// 新建实例直接采用新布局；但实例目录里已有旧数据时不能抢先打标记，否则那些数据再也迁不进来。
func TestInitInstanceLayout(t *testing.T) {
	setupLayoutEnv(t)

	if err := InitInstanceLayout("fresh"); err != nil {
		t.Fatal(err)
	}
	if !IsMigrated("fresh") || !isDir(InstancePluginsDir("fresh")) {
		t.Error("新实例应直接采用每实例插件目录")
	}

	writeFile(t, filepath.Join(legacyPluginsDir("old"), "Permissions", configFileName), `{}`)
	if err := InitInstanceLayout("old"); err != nil {
		t.Fatal(err)
	}
	if IsMigrated("old") {
		t.Error("带着旧数据的实例不能被直接标记为已迁移")
	}
}

// 迁移之后搬运必须彻底关断：哪怕镜像里还是真实目录、旧目录里还残留着东西。
func TestShuttleRetiredAfterMigration(t *testing.T) {
	root := setupLayoutEnv(t)
	const inst = "alpha"
	mustMigrate(t, inst, "")

	mirrorDir := filepath.Join(root, "server-files-tmp-"+inst)
	mirrorDB := filepath.Join(MirrorPluginsDir(mirrorDir), "Permissions", "ArkDB.db")
	writeSQLite(t, mirrorDB, "mirror-data")
	legacyDB := filepath.Join(legacyPluginsDir(inst), "Permissions", "ArkDB.db")
	writeSQLite(t, legacyDB, "stale")
	touch(t, legacyDB, time.Now().Add(-2*time.Hour))

	Rescue(inst, mirrorDir)
	Reclaim(inst, mirrorDir)
	if got := readFile(t, legacyDB); !strings.HasSuffix(got, "stale") {
		t.Errorf("已迁移实例的镜像内容被收回了旧目录: %q", got)
	}

	Inject(inst, mirrorDir)
	if got := readFile(t, mirrorDB); !strings.HasSuffix(got, "mirror-data") {
		t.Errorf("已迁移实例被注入了旧目录里的内容: %q", got)
	}
}

// 已迁移实例的列表以实例目录为准，并带上 PluginInfo.json 的元数据。
func TestListInstancePluginsReadsInstanceDir(t *testing.T) {
	setupLayoutEnv(t)
	const inst = "alpha"
	mustMigrate(t, inst, "")

	dir := InstancePluginsDir(inst)
	writeFile(t, filepath.Join(dir, "TidyDamsASA", "TidyDamsASA.dll"), "MZ")
	writeFile(t, filepath.Join(dir, "TidyDamsASA", pluginInfoFileName),
		`{"FullName":"TidyDamsASA","Description":"No more only wood in beaver dams!","Version":1.10,"MinApiVersion":2}`)
	writeFile(t, filepath.Join(InstanceSnapshotsDir(inst), "TidyDamsASA", "ArkDB.db"), "snap")
	writeFile(t, filepath.Join(dir, "Leftover", configFileName), `{}`)

	got, err := ListInstancePlugins(inst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Leftover" || got[1].Name != "TidyDamsASA" {
		t.Fatalf("列表 = %+v", got)
	}
	tidy := got[1]
	if tidy.Version != "1.10" || tidy.MinApiVersion != "2" || tidy.FullName != "TidyDamsASA" {
		t.Errorf("元数据读取有误: %+v", tidy)
	}
	if tidy.DllMissing {
		t.Error("TidyDamsASA.dll 存在，不应标记为缺失")
	}
	if len(tidy.Snapshots) != 1 {
		t.Errorf("快照应从 PluginSnapshots 读取，实际 %+v", tidy.Snapshots)
	}
	if !got[0].DllMissing || !got[0].HasConfig {
		t.Errorf("没有 dll 的残留目录应标记 dll_missing: %+v", got[0])
	}
}

func TestPluginConfigReadWriteInInstanceDir(t *testing.T) {
	setupLayoutEnv(t)
	const inst = "alpha"
	mustMigrate(t, inst, "")
	dir := filepath.Join(InstancePluginsDir(inst), "Permissions")
	writeFile(t, filepath.Join(dir, "Permissions.dll"), "MZ")
	writeFile(t, filepath.Join(dir, configFileName), `{"a":1}`)

	content, seeded, err := ReadPluginConfig(inst, "Permissions")
	if err != nil || content != `{"a":1}` || !seeded {
		t.Fatalf("ReadPluginConfig = %q, %v, %v", content, seeded, err)
	}
	if err := WritePluginConfig(inst, "Permissions", `{"a":2}`); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, configFileName)); got != `{"a":2}` {
		t.Errorf("配置应直接写进实例插件目录，实际 %q", got)
	}

	if err := WritePluginConfig(inst, "Missing", `{}`); err == nil {
		t.Error("给没安装的插件写配置应当报错")
	}
	if _, err := os.Stat(filepath.Join(InstancePluginsDir(inst), "Missing")); !os.IsNotExist(err) {
		t.Error("不该替没安装的插件建目录")
	}
}

func TestRetireLegacyServerPluginsMovesAndKeepsEmptyDir(t *testing.T) {
	root := setupLayoutEnv(t)
	writeFile(t, filepath.Join(srcPluginDir("Permissions"), "Permissions.dll"), "MZ v1")

	RetireLegacyServerPlugins()

	entries, err := os.ReadDir(SourcePluginsDir())
	if err != nil {
		t.Fatalf("server-files 的插件目录应保留（镜像例外要求源侧存在）: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("server-files 的插件目录应被清空，实际 %d 项", len(entries))
	}
	moved, _ := filepath.Glob(filepath.Join(root, "arkapi", "backups", "legacy-server-plugins-*", "Permissions", "Permissions.dll"))
	if len(moved) != 1 {
		t.Errorf("全局插件应移入备份目录，实际 %v", moved)
	}
}

// 手工解压出来的目录大小写可能与常量不同，路径必须按盘上的实际大小写给出。
func TestSourcePluginsRelPathUsesOnDiskCase(t *testing.T) {
	setupLayoutEnv(t)
	writeFile(t, filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame", "Binaries", "Win64", "arkapi", "plugins", "X", "X.dll"), "MZ")

	if got, want := SourcePluginsRelPath(), win64RelPath+"/arkapi/plugins"; got != want {
		t.Errorf("SourcePluginsRelPath = %q，期望 %q", got, want)
	}
}
