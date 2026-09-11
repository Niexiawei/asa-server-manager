package arkapimanage

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/plugindata"
)

const sqliteMagic = "SQLite format 3\x00"

// setupEnv 把目录变量指到临时目录、装好主程序、建出实例（已采用每实例布局），
// 并把「实例是否在运行」换成一张可控的表。
func setupEnv(t *testing.T, instances ...string) map[string]string {
	t.Helper()
	root := t.TempDir()
	origBase, origSF, origInst := cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir
	origBusy := instanceBusy
	t.Cleanup(func() {
		cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir = origBase, origSF, origInst
		instanceBusy = origBusy
	})
	cfgpkg.BaseDir = root
	cfgpkg.ServerFilesDir = filepath.Join(root, "server-files")
	cfgpkg.InstancesDir = filepath.Join(root, "instances")
	write(t, filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame", "Binaries", "Win64", "AsaApiLoader.exe"), amd64PE)

	for _, inst := range instances {
		newTestInstance(t, inst)
		if err := plugindata.InitInstanceLayout(inst); err != nil {
			t.Fatal(err)
		}
	}

	busy := map[string]string{}
	instanceBusy = func(name string) string { return busy[name] }
	return busy
}

func newTestInstance(t *testing.T, name string) {
	t.Helper()
	write(t, filepath.Join(cfgpkg.InstancesDir, name, "Config", "GameUserSettings.ini"), "[ServerSettings]\n")
	if err := cfgpkg.SaveInstanceConfig(name, &cfgpkg.InstanceConfig{ServerName: name}); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// pluginZip 构造一个 TidyDams 结构的插件包；extra 的键相对插件目录。
func pluginZip(t *testing.T, name, version string, extra map[string]string) io.Reader {
	t.Helper()
	files := map[string]string{
		"PluginInfo.json": `{"FullName":"` + name + `","Version":` + version + `}`,
		name + ".dll":     amd64PE,
		name + ".pdb":     "pdb",
	}
	for k, v := range extra {
		files[k] = v
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for rel, content := range files {
		w, err := zw.Create(name + "/" + rel)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func mustStage(t *testing.T, src io.Reader, expect string) *PluginStage {
	t.Helper()
	st, err := StagePlugin(src, "test.zip", expect)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Errors) > 0 || st.Token == "" {
		t.Fatalf("校验未通过: %v", st.Errors)
	}
	return st
}

func mustApply(t *testing.T, st *PluginStage, targets []string, restore bool) []Result {
	t.Helper()
	res, err := ApplyPlugin(st.Token, targets, restore)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func pluginDir(inst, plugin string) string {
	return filepath.Join(plugindata.InstancePluginsDir(inst), plugin)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// 只装进显式列出的实例；没列出的实例一个文件都不动（接口不存在「默认全部」）。
func TestInstallOnlyIntoListedInstances(t *testing.T) {
	setupEnv(t, "a", "b", "c")
	st := mustStage(t, pluginZip(t, "Tidy", "1.3", nil), "")
	if len(st.Targets) != 3 {
		t.Fatalf("目标实例表应列出全部 3 个实例，实际 %d", len(st.Targets))
	}

	for _, r := range mustApply(t, st, []string{"a", "b"}, false) {
		if !r.OK || r.Action != "install" {
			t.Errorf("%+v", r)
		}
	}
	for _, inst := range []string{"a", "b"} {
		if !exists(filepath.Join(pluginDir(inst, "Tidy"), "Tidy.dll")) {
			t.Errorf("实例 %s 应装上插件", inst)
		}
	}
	if entries, _ := os.ReadDir(plugindata.InstancePluginsDir("c")); len(entries) != 0 {
		t.Errorf("没勾选的实例 c 不应被操作，实际多了 %d 项", len(entries))
	}
	if entries, _ := os.ReadDir(stagingRoot()); len(entries) != 0 {
		t.Errorf("apply 之后暂存目录应被清掉，还剩 %d 项", len(entries))
	}
	// 临时组装目录不能留在实例的 ArkApi/ 下
	entries, _ := os.ReadDir(plugindata.InstanceArkApiDir("a"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".install-") {
			t.Errorf("临时目录 %s 没有清理", e.Name())
		}
	}
}

// 一个实例失败不影响其他实例；运行中的实例被拒且不被改动。
func TestApplyFailureIsolatedPerInstance(t *testing.T) {
	busy := setupEnv(t, "a", "b")
	busy["b"] = "实例运行中，请先停止"

	st := mustStage(t, pluginZip(t, "Tidy", "1.3", nil), "")
	for _, tg := range st.Targets {
		if tg.Instance == "b" && (tg.Blocked == "" || !tg.Running) {
			t.Errorf("运行中的实例在目标表里应不可选: %+v", tg)
		}
	}

	res := mustApply(t, st, []string{"a", "b", "ghost"}, false)
	byInst := map[string]Result{}
	for _, r := range res {
		byInst[r.Instance] = r
	}
	if !byInst["a"].OK {
		t.Errorf("a 应成功: %+v", byInst["a"])
	}
	if byInst["b"].OK || !strings.Contains(byInst["b"].Error, "运行中") {
		t.Errorf("b 应被拒: %+v", byInst["b"])
	}
	if byInst["ghost"].OK || !strings.Contains(byInst["ghost"].Error, "不存在") {
		t.Errorf("不存在的实例应报错: %+v", byInst["ghost"])
	}
	if exists(pluginDir("b", "Tidy")) {
		t.Error("被拒的实例不应装上插件")
	}
}

// 更新：配置旧值保留、新键出现；数据文件保留；旧的多余文件被清走（随旧目录进备份）。
func TestUpdateCarriesConfigAndData(t *testing.T) {
	setupEnv(t, "a")
	mustApply(t, mustStage(t, pluginZip(t, "Perm", "1.1", map[string]string{"config.json": `{"A":"default","B":1}`}), ""), []string{"a"}, false)

	dir := pluginDir("a", "Perm")
	write(t, filepath.Join(dir, "config.json"), `{"A":"user","B":1}`)
	write(t, filepath.Join(dir, "ArkDB.db"), sqliteMagic+"live")
	write(t, filepath.Join(dir, "ArkDB.db-wal"), "live-wal")
	write(t, filepath.Join(dir, "stale.txt"), "old version file")

	st := mustStage(t, pluginZip(t, "Perm", "1.2", map[string]string{
		"config.json":  `{"A":"v2-default","B":2,"C":"new"}`,
		"ArkDB.db":     sqliteMagic + "seed",
		"ArkDB.db-shm": "seed-shm",
	}), "Perm")
	res := mustApply(t, st, []string{"a"}, false)
	if !res[0].OK || res[0].Action != "update" {
		t.Fatalf("%+v", res[0])
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(dir, "config.json"))), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["A"] != "user" || cfg["B"] != float64(1) || cfg["C"] != "new" {
		t.Errorf("配置合并不对: %v", cfg)
	}
	if got := read(t, filepath.Join(dir, "ArkDB.db")); got != sqliteMagic+"live" {
		t.Errorf("数据库应保留实例的那份，实际 %q", got)
	}
	if got := read(t, filepath.Join(dir, "ArkDB.db-wal")); got != "live-wal" {
		t.Errorf("-wal 应保留，实际 %q", got)
	}
	if exists(filepath.Join(dir, "ArkDB.db-shm")) {
		t.Error("新包的种子 -shm 不应与实例的主库拼在一起")
	}
	if exists(filepath.Join(dir, "stale.txt")) {
		t.Error("旧版本独有的文件应被清走")
	}
	if meta, _ := plugindata.ReadPluginMeta(dir); meta.Version != "1.2" {
		t.Errorf("版本应更新为 1.2，实际 %q", meta.Version)
	}
	b := pluginBackups("a", "Perm")
	if len(b) != 1 || read(t, filepath.Join(b[0], "stale.txt")) != "old version file" {
		t.Errorf("旧版本应整个进备份: %v", b)
	}
}

func TestUpdateKeepsDisabledPluginDisabled(t *testing.T) {
	setupEnv(t, "a")
	mustApply(t, mustStage(t, pluginZip(t, "P", "1", nil), ""), []string{"a"}, false)
	if applied, err := SetPluginEnabled("a", "P", false); err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}

	mustApply(t, mustStage(t, pluginZip(t, "P", "2", nil), ""), []string{"a"}, false)
	_, enabled, ok := plugindata.FindInstancePlugin("a", "P")
	if !ok || enabled {
		t.Errorf("被禁用的插件更新后应仍在 PluginsDisabled：ok=%v enabled=%v", ok, enabled)
	}
}

// 安装一个处于禁用列表里的插件：装进 PluginsDisabled，保持禁用。
func TestInstallPluginListedAsDisabled(t *testing.T) {
	setupEnv(t, "a")
	if err := cfgpkg.ModifyInstanceConfig("a", func(c *cfgpkg.InstanceConfig) error {
		c.DisabledArkApiPlugins = []string{"P"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mustApply(t, mustStage(t, pluginZip(t, "P", "1", nil), ""), []string{"a"}, false)
	if _, enabled, ok := plugindata.FindInstancePlugin("a", "P"); !ok || enabled {
		t.Errorf("ok=%v enabled=%v", ok, enabled)
	}
}

// 卸载只动勾选的实例；配置与数据进备份，从禁用列表移除；重装时可以从备份恢复。
func TestUninstallThenRestoreFromBackup(t *testing.T) {
	setupEnv(t, "a", "b")
	mustApply(t, mustStage(t, pluginZip(t, "P", "1", map[string]string{"config.json": `{"A":"default"}`}), ""), []string{"a", "b"}, false)
	write(t, filepath.Join(pluginDir("a", "P"), "config.json"), `{"A":"mine"}`)
	write(t, filepath.Join(pluginDir("a", "P"), "ArkDB.db"), sqliteMagic+"perms")
	if _, err := SetPluginEnabled("a", "P", false); err != nil {
		t.Fatal(err)
	}
	bDllBefore := read(t, filepath.Join(pluginDir("b", "P"), "P.dll"))

	res, err := UninstallPlugin("P", []string{"a"})
	if err != nil || !res[0].OK {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if _, _, ok := plugindata.FindInstancePlugin("a", "P"); ok {
		t.Error("a 上的插件应被卸载")
	}
	if slices.Contains(plugindata.DisabledPlugins("a"), "P") {
		t.Error("卸载后应从禁用列表移除")
	}
	if got := read(t, filepath.Join(pluginDir("b", "P"), "P.dll")); got != bDllBefore {
		t.Error("没勾选的实例 b 不应被改动")
	}

	st := mustStage(t, pluginZip(t, "P", "1", map[string]string{"config.json": `{"A":"default","New":1}`}), "")
	var ta PluginTarget
	for _, tg := range st.Targets {
		if tg.Instance == "a" {
			ta = tg
		}
	}
	if ta.Action != "install" || !ta.BackupAvailable {
		t.Fatalf("a 应是新装且有备份可恢复: %+v", ta)
	}
	mustApply(t, st, []string{"a"}, true)
	dir := pluginDir("a", "P")
	if got := read(t, filepath.Join(dir, "ArkDB.db")); got != sqliteMagic+"perms" {
		t.Errorf("应从备份恢复数据，实际 %q", got)
	}
	if cfg := read(t, filepath.Join(dir, "config.json")); !strings.Contains(cfg, `"mine"`) || !strings.Contains(cfg, `"New"`) {
		t.Errorf("应从备份恢复配置并并入新键，实际 %s", cfg)
	}
}

func TestUninstallRejectsRunningInstance(t *testing.T) {
	busy := setupEnv(t, "a")
	mustApply(t, mustStage(t, pluginZip(t, "P", "1", nil), ""), []string{"a"}, false)
	busy["a"] = "实例运行中"

	res, err := UninstallPlugin("P", []string{"a"})
	if err != nil || res[0].OK {
		t.Fatalf("运行中的实例应被拒: err=%v res=%+v", err, res)
	}
	if !exists(pluginDir("a", "P")) {
		t.Error("被拒时插件目录不应被改动")
	}
}

// 实例正在启动（PrepareForStart 持锁）时插件操作拿不到锁，直接报错而不是排队。
func TestApplyRejectedWhileInstanceLocked(t *testing.T) {
	setupEnv(t, "a")
	st := mustStage(t, pluginZip(t, "P", "1", nil), "")
	unlock, ok := plugindata.TryLockInstance("a")
	if !ok {
		t.Fatal("拿不到锁")
	}
	res := mustApply(t, st, []string{"a"}, false)
	unlock()
	if res[0].OK || !strings.Contains(res[0].Error, "正在启动") {
		t.Errorf("%+v", res[0])
	}
}

func TestStagingTokenLifecycle(t *testing.T) {
	setupEnv(t, "a")
	st := mustStage(t, pluginZip(t, "P", "1", nil), "")

	if _, err := ApplyPlugin(st.Token, nil, false); err == nil {
		t.Fatal("没有目标实例应当报错")
	}
	// 上面那次在取出暂存包之前就失败了，token 应仍然有效
	mustApply(t, st, []string{"a"}, false)
	if _, err := ApplyPlugin(st.Token, []string{"a"}, false); !errors.Is(err, errStageGone) {
		t.Errorf("token 只能用一次，第二次 err=%v", err)
	}

	st2 := mustStage(t, pluginZip(t, "P", "2", nil), "")
	Discard(st2.Token)
	if _, err := ApplyPlugin(st2.Token, []string{"a"}, false); !errors.Is(err, errStageGone) {
		t.Errorf("丢弃后不能再 apply，err=%v", err)
	}
	if entries, _ := os.ReadDir(stagingRoot()); len(entries) != 0 {
		t.Errorf("暂存区应已清空，还剩 %d 项", len(entries))
	}
}

func TestStageRejectsBadPackage(t *testing.T) {
	setupEnv(t, "a")
	st, err := StagePlugin(pluginZip(t, "P", "1", nil), "p.zip", "Other")
	if err != nil {
		t.Fatal(err)
	}
	if st.Token != "" || len(st.Errors) == 0 {
		t.Fatalf("expect 不匹配应被拒: %+v", st)
	}
	st, err = StagePlugin(strings.NewReader("not a zip"), "p.zip", "")
	if err != nil || st.Token != "" || !strings.Contains(strings.Join(st.Errors, ""), "zip") {
		t.Fatalf("err=%v st=%+v", err, st)
	}
	if entries, _ := os.ReadDir(stagingRoot()); len(entries) != 0 {
		t.Errorf("校验失败的包不应留在暂存区，还剩 %d 项", len(entries))
	}
}

// 每个插件只留最近 3 份备份；清理时不能把名字恰好以「插件名-」开头的别的插件的备份删掉。
//
// 别的插件特意取名 P-1：它的备份 P-1-20260101-000000 按名字排在 P 的所有备份之前（'1' < '2'），
// 只看前缀的话它就是「最旧的一份」、第一个被删。换成 P-Bar 这种排在数字之后的名字，
// 前缀匹配的实现也能通过——变异验证 M8 实测过。
func TestBackupsPrunedWithoutTouchingOtherPlugins(t *testing.T) {
	setupEnv(t, "a")
	other := filepath.Join(plugindata.InstanceBackupsDir("a"), "P-1-20260101-000000")
	write(t, filepath.Join(other, "P-1.dll"), "x")

	for v := 1; v <= 5; v++ {
		mustApply(t, mustStage(t, pluginZip(t, "P", "1."+string(rune('0'+v)), nil), ""), []string{"a"}, false)
	}
	if n := len(pluginBackups("a", "P")); n != keepBackups {
		t.Errorf("应保留 %d 份备份，实际 %d", keepBackups, n)
	}
	if !exists(other) {
		t.Error("插件 P-1 的备份被当成 P 的删掉了")
	}
}

func TestSetPluginEnabled(t *testing.T) {
	busy := setupEnv(t, "a")
	mustApply(t, mustStage(t, pluginZip(t, "P", "1", nil), ""), []string{"a"}, false)

	// 已停止：立即落位
	if applied, err := SetPluginEnabled("a", "P", false); err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if _, enabled, _ := plugindata.FindInstancePlugin("a", "P"); enabled {
		t.Error("停止时禁用应立即挪进 PluginsDisabled")
	}
	if !slices.Equal(plugindata.DisabledPlugins("a"), []string{"P"}) {
		t.Errorf("禁用列表 = %v", plugindata.DisabledPlugins("a"))
	}
	if applied, err := SetPluginEnabled("a", "P", true); err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if _, enabled, _ := plugindata.FindInstancePlugin("a", "P"); !enabled {
		t.Error("重新启用应挪回 Plugins")
	}

	// 运行中：只写配置，列表里标为 pending，下次启动落位
	busy["a"] = "running"
	if applied, err := SetPluginEnabled("a", "P", false); err != nil || applied {
		t.Fatalf("运行中应只写配置: applied=%v err=%v", applied, err)
	}
	if _, enabled, _ := plugindata.FindInstancePlugin("a", "P"); !enabled {
		t.Error("运行中不应挪目录")
	}
	list, _ := plugindata.ListInstancePlugins("a")
	if len(list) != 1 || list[0].Enabled || !list[0].Pending {
		t.Errorf("列表应显示已禁用、待生效: %+v", list)
	}
	delete(busy, "a")
	if err := plugindata.PrepareForStart("a", "", plugindata.DisabledPlugins("a"), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, enabled, _ := plugindata.FindInstancePlugin("a", "P"); enabled {
		t.Error("下次启动时应落位")
	}

	// 正在启动（锁被 PrepareForStart 持有）：同样只写配置
	unlock, _ := plugindata.TryLockInstance("a")
	applied, err := SetPluginEnabled("a", "P", true)
	unlock()
	if err != nil || applied {
		t.Errorf("持锁期间应只写配置: applied=%v err=%v", applied, err)
	}

	if _, err := SetPluginEnabled("a", "Nope", false); err == nil {
		t.Error("没装的插件应报错")
	}
}

func TestLegacyInstanceRejected(t *testing.T) {
	setupEnv(t)
	newTestInstance(t, "old") // 没有迁移标记：旧布局
	if _, err := SetPluginEnabled("old", "P", false); !errors.Is(err, ErrLegacyLayout) {
		t.Errorf("err = %v", err)
	}
	st := mustStage(t, pluginZip(t, "P", "1", nil), "")
	if st.Targets[0].Blocked == "" {
		t.Errorf("旧布局实例在目标表里应不可选: %+v", st.Targets[0])
	}
	if res := mustApply(t, st, []string{"old"}, false); res[0].OK {
		t.Error("旧布局实例不应被安装")
	}
}

func TestPluginInstancesListsInstalledOnly(t *testing.T) {
	setupEnv(t, "a", "b", "c")
	mustApply(t, mustStage(t, pluginZip(t, "P", "1.3", nil), ""), []string{"a", "b"}, false)
	if _, err := SetPluginEnabled("b", "P", false); err != nil {
		t.Fatal(err)
	}
	list, err := PluginInstances("P")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Instance != "a" || list[1].Instance != "b" {
		t.Fatalf("list = %+v", list)
	}
	if !list[0].Enabled || list[1].Enabled || list[0].InstalledVersion != "1.3" {
		t.Errorf("list = %+v", list)
	}
}
