package plugindata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/logger"
)

// 本包会写 Debug/Warn 日志，GetLogger() 未初始化时返回 nil 会 panic。
func TestMain(m *testing.M) {
	logger.InitLoggerWithBaseDir(os.TempDir())
	os.Exit(m.Run())
}

// ---------- 测试脚手架 ----------

// setupEnv 把 cfgpkg 的目录变量指到临时目录，返回 (instanceName, mirrorDir)。
func setupEnv(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()

	origInstances := cfgpkg.InstancesDir
	t.Cleanup(func() { cfgpkg.InstancesDir = origInstances })
	cfgpkg.InstancesDir = filepath.Join(root, "instances")

	mirrorDir := filepath.Join(root, "server-files-tmp-test")
	if err := os.MkdirAll(MirrorPluginsDir(mirrorDir), 0755); err != nil {
		t.Fatalf("建镜像插件目录: %v", err)
	}
	return "testinst", mirrorDir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("建目录 %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写文件 %s: %v", path, err)
	}
}

// writeSQLite 写一个带合法 SQLite 文件头的假库。
// 识别走的是魔数而不是扩展名，所以这里不需要真的建库。
func writeSQLite(t *testing.T, path, payload string) {
	t.Helper()
	writeFile(t, path, string(sqliteMagic)+payload)
}

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("设置 mtime %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件 %s: %v", path, err)
	}
	return string(b)
}

// ---------- 文件分类 ----------

// SQLite 必须按文件头识别：插件把库命名成 .dat、.bin 都有可能，
// 按扩展名判定会漏，而漏掉的库既不搬运也不快照，是静默丢数据。
func TestScanRecognizesSQLiteByMagicNotExtension(t *testing.T) {
	dir := t.TempDir()
	writeSQLite(t, filepath.Join(dir, "store.dat"), "payload")
	writeFile(t, filepath.Join(dir, "Permissions.dll"), "MZ binary")
	writeFile(t, filepath.Join(dir, "notes.txt"), "readme")

	groups := scanPluginDir(dir, "Permissions")

	var bases []string
	for _, g := range groups {
		bases = append(bases, g.Base)
		if g.Base == "store.dat" && !g.IsSQLite {
			t.Error("store.dat 有 SQLite 文件头，应被识别为 SQLite 库")
		}
	}
	if len(bases) != 1 || bases[0] != "store.dat" {
		t.Errorf("应只搬运 store.dat，实际得到 %v（dll/txt 不该进搬运范围）", bases)
	}
}

// -wal / -shm 必须与主库归为一组：实测静止状态下主库只有 4 KB，
// 而 -wal 有 1.9 MB —— 只搬主库等于丢掉几乎全部数据。
func TestScanGroupsCompanionFilesWithMainDB(t *testing.T) {
	dir := t.TempDir()
	writeSQLite(t, filepath.Join(dir, "ArkDB.db"), "main")
	writeFile(t, filepath.Join(dir, "ArkDB.db-wal"), "walwalwal")
	writeFile(t, filepath.Join(dir, "ArkDB.db-shm"), "shm")

	groups := scanPluginDir(dir, "Permissions")
	if len(groups) != 1 {
		t.Fatalf("三个文件应聚成 1 组，实际 %d 组: %+v", len(groups), groups)
	}
	if len(groups[0].Members) != 3 {
		t.Errorf("组内应有 3 个成员，实际 %v", groups[0].Members)
	}
	if !groups[0].IsSQLite {
		t.Error("主库有 SQLite 文件头，组应标记为 SQLite")
	}
}

// config.json 是配置；同目录的 config_help.json / commented_config.jsonc 是
// 插件作者给的说明文档，不是配置，不能搬。
func TestScanTreatsOnlyConfigJSONAsConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.json"), `{"a":1}`)
	writeFile(t, filepath.Join(dir, "config_help.json"), `{"help":true}`)

	var configs []string
	for _, g := range scanPluginDir(dir, "CrosschatAscended") {
		if g.IsConfig {
			configs = append(configs, g.Base)
		}
	}
	if len(configs) != 1 || configs[0] != configFileName {
		t.Errorf("只有 config.json 该被当作配置，实际 %v", configs)
	}
}

// ---------- 整组替换 ----------

// 「整组替换」不是「逐文件覆盖」：目标侧残留的旧 -wal 必须先删掉。
// 留着它就会拼出「新主库 + 旧 WAL」，SQLite 打开时拿旧 WAL 去重放，比不搬还糟。
func TestReplaceGroupRemovesStaleCompanions(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// 源侧已 checkpoint，只剩主库
	writeSQLite(t, filepath.Join(src, "ArkDB.db"), "fresh")
	// 目标侧还留着上一轮的主库 + WAL
	writeSQLite(t, filepath.Join(dst, "ArkDB.db"), "stale")
	writeFile(t, filepath.Join(dst, "ArkDB.db-wal"), "stale-wal")

	groups := scanPluginDir(src, "Permissions")
	if len(groups) != 1 {
		t.Fatalf("源侧应有 1 组，实际 %d", len(groups))
	}
	if err := replaceGroup(src, dst, groups[0]); err != nil {
		t.Fatalf("replaceGroup: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, "ArkDB.db-wal")); !os.IsNotExist(err) {
		t.Error("目标侧残留的旧 -wal 必须被删掉，否则会与新主库拼成互不匹配的组合")
	}
	if got := readFile(t, filepath.Join(dst, "ArkDB.db")); !strings.HasSuffix(got, "fresh") {
		t.Errorf("主库未被替换: %q", got)
	}
}

// ---------- 抢救规则（方案的成败所在） ----------

// 上一轮崩溃退出 → 回收没执行 → 镜像里的才是最新数据。
// 启动时必须先 Rescue 把它抢回实例目录，再 Inject；顺序颠倒就会用陈旧的
// 实例副本静默盖掉崩溃前的数据。
func TestRescueRecoversCrashedRunData(t *testing.T) {
	inst, mirrorDir := setupEnv(t)
	mirrorPlugin := filepath.Join(MirrorPluginsDir(mirrorDir), "Permissions")
	instPlugin := filepath.Join(InstancePluginsDir(inst), "Permissions")

	old := time.Now().Add(-2 * time.Hour)
	writeSQLite(t, filepath.Join(instPlugin, "ArkDB.db"), "old-instance-copy")
	touch(t, filepath.Join(instPlugin, "ArkDB.db"), old)

	// 崩溃现场：镜像里有更新的数据，且大头压在未 checkpoint 的 -wal 里
	writeSQLite(t, filepath.Join(mirrorPlugin, "ArkDB.db"), "crashed-main")
	writeFile(t, filepath.Join(mirrorPlugin, "ArkDB.db-wal"), "crashed-wal-payload")

	Rescue(inst, mirrorDir)

	if got := readFile(t, filepath.Join(instPlugin, "ArkDB.db")); !strings.HasSuffix(got, "crashed-main") {
		t.Errorf("崩溃后镜像侧更新，实例侧应被抢救更新，实际 %q", got)
	}
	if got := readFile(t, filepath.Join(instPlugin, "ArkDB.db-wal")); got != "crashed-wal-payload" {
		t.Errorf("-wal 必须随主库一起搬（数据都在里面），实际 %q", got)
	}

	Inject(inst, mirrorDir)
	if got := readFile(t, filepath.Join(mirrorPlugin, "ArkDB.db")); !strings.HasSuffix(got, "crashed-main") {
		t.Errorf("Rescue 之后的 Inject 不该把旧副本推回镜像，实际 %q", got)
	}
}

// 反向必须守住：实例侧更新时（例如刚从备份恢复），Rescue 不能拿镜像里的旧数据覆盖它。
func TestRescueKeepsNewerInstanceData(t *testing.T) {
	inst, mirrorDir := setupEnv(t)
	mirrorPlugin := filepath.Join(MirrorPluginsDir(mirrorDir), "Permissions")
	instPlugin := filepath.Join(InstancePluginsDir(inst), "Permissions")

	old := time.Now().Add(-2 * time.Hour)
	writeSQLite(t, filepath.Join(mirrorPlugin, "ArkDB.db"), "old-mirror")
	touch(t, filepath.Join(mirrorPlugin, "ArkDB.db"), old)
	writeSQLite(t, filepath.Join(instPlugin, "ArkDB.db"), "restored-backup")

	Rescue(inst, mirrorDir)

	if got := readFile(t, filepath.Join(instPlugin, "ArkDB.db")); !strings.HasSuffix(got, "restored-backup") {
		t.Errorf("镜像侧更旧时不得回收覆盖实例侧，实际 %q", got)
	}
}

// 首次启动：实例目录下没有该插件 → 以镜像（源服务端自带的那一份）为初值播种。
func TestFirstRunSeedsFromMirror(t *testing.T) {
	inst, mirrorDir := setupEnv(t)
	mirrorPlugin := filepath.Join(MirrorPluginsDir(mirrorDir), "Permissions")
	instPlugin := filepath.Join(InstancePluginsDir(inst), "Permissions")

	writeSQLite(t, filepath.Join(mirrorPlugin, "ArkDB.db"), "shipped-default")
	writeFile(t, filepath.Join(mirrorPlugin, configFileName), `{"UseMysql":false}`)

	Rescue(inst, mirrorDir)

	if got := readFile(t, filepath.Join(instPlugin, "ArkDB.db")); !strings.HasSuffix(got, "shipped-default") {
		t.Errorf("首次启动应从镜像播种数据库，实际 %q", got)
	}
	if got := readFile(t, filepath.Join(instPlugin, configFileName)); !strings.Contains(got, "UseMysql") {
		t.Errorf("首次启动应从镜像播种配置，实际 %q", got)
	}
}

// 两个实例各自持有独立的数据，互不影响 —— 这正是整件事要解决的问题。
func TestInstancesAreIsolated(t *testing.T) {
	root := t.TempDir()
	origInstances := cfgpkg.InstancesDir
	t.Cleanup(func() { cfgpkg.InstancesDir = origInstances })
	cfgpkg.InstancesDir = filepath.Join(root, "instances")

	for _, tc := range []struct{ inst, payload string }{{"a", "perm-a"}, {"b", "perm-b"}} {
		mirrorDir := filepath.Join(root, "server-files-tmp-"+tc.inst)
		mirrorPlugin := filepath.Join(MirrorPluginsDir(mirrorDir), "Permissions")
		writeSQLite(t, filepath.Join(mirrorPlugin, "ArkDB.db"), tc.payload)
		Reclaim(tc.inst, mirrorDir)
	}

	for _, tc := range []struct{ inst, payload string }{{"a", "perm-a"}, {"b", "perm-b"}} {
		p := filepath.Join(InstancePluginsDir(tc.inst), "Permissions", "ArkDB.db")
		if got := readFile(t, p); !strings.HasSuffix(got, tc.payload) {
			t.Errorf("实例 %s 的数据被串了: %q", tc.inst, got)
		}
	}
}

// ---------- DbPathOverride ----------

// 用户手工把数据库指到别处后，我们的搬运会对着空目录做无用功，
// 真实数据在别处不受保护且不报错。必须识别出来并让路。
func TestExternalDBPathSkipsDataTransfer(t *testing.T) {
	inst, mirrorDir := setupEnv(t)
	mirrorPlugin := filepath.Join(MirrorPluginsDir(mirrorDir), "Permissions")
	instPlugin := filepath.Join(InstancePluginsDir(inst), "Permissions")

	external := filepath.Join(t.TempDir(), "elsewhere")
	writeFile(t, filepath.Join(instPlugin, configFileName),
		`{"DbPathOverride":"`+filepath.ToSlash(external)+`"}`)
	writeSQLite(t, filepath.Join(mirrorPlugin, "ArkDB.db"), "should-not-be-harvested")

	Reclaim(inst, mirrorDir)

	if _, err := os.Stat(filepath.Join(instPlugin, "ArkDB.db")); !os.IsNotExist(err) {
		t.Error("用户接管数据库路径后，管理器不该再回收该插件的数据文件")
	}
}

// 指向实例插件目录之内是等价形态，仍然正常搬运。
func TestOverrideInsideInstanceDirStillTransfers(t *testing.T) {
	inst, mirrorDir := setupEnv(t)
	mirrorPlugin := filepath.Join(MirrorPluginsDir(mirrorDir), "Permissions")
	instPlugin := filepath.Join(InstancePluginsDir(inst), "Permissions")

	writeFile(t, filepath.Join(instPlugin, configFileName),
		`{"DbPathOverride":"`+strings.ReplaceAll(instPlugin, `\`, `\\`)+`"}`)
	writeSQLite(t, filepath.Join(mirrorPlugin, "ArkDB.db"), "harvest-me")

	Reclaim(inst, mirrorDir)

	if got := readFile(t, filepath.Join(instPlugin, "ArkDB.db")); !strings.HasSuffix(got, "harvest-me") {
		t.Errorf("override 指向实例目录内属等价形态，应正常搬运，实际 %q", got)
	}
}

// ---------- 同步例外的判定 ----------

func TestIsProtectedRelPath(t *testing.T) {
	mirrorDir := t.TempDir()
	base := pluginsRelPath + "/Permissions/"

	writeSQLite(t, filepath.Join(mirrorDir, filepath.FromSlash(base+"store.dat")), "x")
	writeFile(t, filepath.Join(mirrorDir, filepath.FromSlash(base+"store.dat-wal")), "x")
	writeFile(t, filepath.Join(mirrorDir, filepath.FromSlash(base+"Permissions.dll")), "MZ")

	cases := []struct {
		rel  string
		want bool
		why  string
	}{
		{base + "config.json", true, "插件配置必须排除出回写，否则注入进去的配置会被源版本盖掉"},
		{base + "ArkDB.db", true, "主库按扩展名就能认出来"},
		{base + "ArkDB.db-wal", true, "源目录里没有 -wal，不排除就会被当成多余条目删掉"},
		{base + "store.dat", true, "扩展名认不出，靠文件头兜底"},
		{base + "store.dat-wal", true, "伴随文件看主库的文件头"},
		{base + "Permissions.dll", false, "插件二进制照常同步，插件更新才能生效"},
		{pluginsRelPath + "/Permissions", false, "插件目录本身不是数据文件"},
		{"ShooterGame/Binaries/Win64/steamclient64.dll", false, "插件目录之外一概不受影响"},
	}
	for _, c := range cases {
		if got := IsProtectedRelPath(mirrorDir, c.rel); got != c.want {
			t.Errorf("IsProtectedRelPath(%q) = %v，期望 %v —— %s", c.rel, got, c.want, c.why)
		}
	}
}
