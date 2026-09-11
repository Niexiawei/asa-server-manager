package arkapimanage

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"asa-server/internal/installer"
)

// 主程序的安装 / 更新 / 卸载（方案 §6.1、§6.2、§12「主程序」）。

const (
	gameMsvcp   = "msvcp game"
	arkApiMsvcp = "msvcp arkapi"
	arkConfig   = `{"settings":{"AutomaticPluginReloading":true,"AutomaticCacheDownload":{"Enable":true}}}`
)

// setupCoreEnv 同 setupEnv，但 server-files 里只有游戏本体：没装主程序，Win64 里有游戏自带的
// msvcp140.dll（主程序包里有同名文件，方案 D3）。
func setupCoreEnv(t *testing.T, instances ...string) {
	t.Helper()
	setupEnv(t, instances...)
	if err := os.Remove(win64Path("AsaApiLoader.exe")); err != nil {
		t.Fatal(err)
	}
	write(t, win64Path("ArkAscendedServer.exe"), "game exe")
	write(t, win64Path("msvcp140.dll"), gameMsvcp)
	write(t, win64Path("vcruntime140.dll"), "vcruntime game")
	t.Cleanup(func() { coreFaultHook = nil })
}

// coreFiles 是 AsaApi_2.03.zip 的结构：文件直接平铺在 zip 根（方案 §3.3）。tag 区分版本；
// override 的值为 "" 表示从包里去掉这个文件。
func coreFiles(tag, config string, override map[string]string) map[string]string {
	files := map[string]string{
		"AsaApiLoader.exe":      amd64PE,
		"AsaApiLoader.pdb":      "loader pdb " + tag,
		"config.json":           config,
		"libcrypto-3-x64.dll":   "crypto " + tag,
		"libssl-3-x64.dll":      "ssl " + tag,
		"msdia140.dll":          "msdia " + tag,
		"msvcp140.dll":          arkApiMsvcp,
		"ArkApi/AsaApi.dll":     amd64PE + tag,
		"ArkApi/AsaApi.pdb":     "api pdb " + tag,
		"ArkApi/pdbignores.txt": "ignores",
		"Lib/AsaApi.lib":        "lib " + tag,
	}
	for k, v := range override {
		if v == "" {
			delete(files, k)
		} else {
			files[k] = v
		}
	}
	return files
}

func permissionsBundled() map[string]string {
	return map[string]string{
		"ArkApi/Plugins/Permissions/PluginInfo.json": `{"FullName":"Ark:SA Permissions","Version":1.1}`,
		"ArkApi/Plugins/Permissions/Permissions.dll": amd64PE,
		"ArkApi/Plugins/Permissions/Permissions.pdb": "pdb",
		"ArkApi/Plugins/Permissions/config.json":     `{}`,
	}
}

func zipOf(t *testing.T, files map[string]string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[n])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func mustStageCore(t *testing.T, files map[string]string, uploadName string) *CoreStage {
	t.Helper()
	st, err := StageCore(zipOf(t, files), uploadName)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Errors) > 0 || st.Token == "" {
		t.Fatalf("主程序包校验未通过: %v", st.Errors)
	}
	return st
}

func mustApplyCore(t *testing.T, st *CoreStage, bundled map[string][]string) *CoreResult {
	t.Helper()
	res, err := ApplyCore(st.Token, nil, bundled, false)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// snapshotTree 记下目录树里每个文件的内容与每个目录（以 / 结尾），用于逐文件比对。
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." {
				out[rel+"/"] = ""
			}
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertTreeEqual(t *testing.T, want, got map[string]string, what string) {
	t.Helper()
	var diffs []string
	for k, v := range want {
		if g, ok := got[k]; !ok {
			diffs = append(diffs, "缺少 "+k)
		} else if g != v {
			diffs = append(diffs, "内容不同 "+k)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			diffs = append(diffs, "多出 "+k)
		}
	}
	if len(diffs) > 0 {
		slices.Sort(diffs)
		t.Errorf("%s：\n  %s", what, strings.Join(diffs, "\n  "))
	}
}

func containsText(list []string, sub string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.Contains(s, sub) })
}

// 覆盖游戏自带的 msvcp140.dll 之前先存原件，卸载时还原；卸载后 Win64 与安装前逐文件一致（方案 D3）。
func TestCoreInstallBacksUpGameFileAndUninstallRestoresIt(t *testing.T) {
	setupCoreEnv(t)
	before := snapshotTree(t, serverWin64Dir())

	st := mustStageCore(t, coreFiles("v1", arkConfig, nil), "AsaApi_2.03.zip")
	if st.Action != "install" || st.Version != "2.03" {
		t.Fatalf("action=%s version=%s", st.Action, st.Version)
	}
	if !slices.Equal(st.Overwrites, []string{"msvcp140.dll"}) {
		t.Errorf("报告应列出会被覆盖的游戏文件，实际 %v", st.Overwrites)
	}

	res := mustApplyCore(t, st, nil)
	if res.Action != "install" || res.Backup != "" {
		t.Errorf("首次安装：action=%s，没有换下任何 ArkApi 文件，备份应为空，实际 %q", res.Action, res.Backup)
	}
	if !installer.ArkApiInstalled() {
		t.Fatal("安装后应能检测到主程序")
	}
	if got := read(t, win64Path("msvcp140.dll")); got != arkApiMsvcp {
		t.Errorf("msvcp140.dll 应被包里的版本覆盖，实际 %q", got)
	}
	if got := read(t, filepath.Join(originalsDir(), "msvcp140.dll")); got != gameMsvcp {
		t.Errorf("游戏原件应存进 originals，实际 %q", got)
	}

	stat, err := Status()
	if err != nil {
		t.Fatal(err)
	}
	if !stat.Managed || stat.Version != "2.03" || !slices.Equal(stat.Overwritten, []string{"msvcp140.dll"}) ||
		len(stat.ModifiedFiles)+len(stat.MissingFiles) != 0 {
		t.Errorf("状态 = %+v", stat)
	}

	un, err := UninstallCore()
	if err != nil {
		t.Fatal(err)
	}
	if !un.Managed || !slices.Equal(un.Restored, []string{"msvcp140.dll"}) {
		t.Errorf("卸载结果 = %+v", un)
	}
	assertTreeEqual(t, before, snapshotTree(t, serverWin64Dir()), "卸载后的 Win64 应与安装前逐文件一致")
	if exists(manifestPath()) || exists(originalsDir()) {
		t.Error("卸载后清单与 originals 都应清掉")
	}
	if got := read(t, filepath.Join(un.Backup, "previous", "AsaApiLoader.exe")); got != amd64PE {
		t.Error("卸载掉的文件应在备份里")
	}
}

// 更新：config.json 旧值优先、新键并入；旧版本有、新版本没有的文件移入备份；
// msvcp140.dll 的原件仍是第一次安装前的那一份。
func TestCoreUpdateMergesConfigAndRetiresDroppedFiles(t *testing.T) {
	setupCoreEnv(t)
	mustApplyCore(t, mustStageCore(t,
		coreFiles("v1", `{"settings":{"A":1}}`, map[string]string{"ArkApi/old.txt": "old"}), "AsaApi_2.03.zip"), nil)
	write(t, win64Path("config.json"), `{"settings":{"A":5}}`) // 用户改过

	st := mustStageCore(t, coreFiles("v2", `{"settings":{"A":1,"B":2}}`, nil), "AsaApi_2.04.zip")
	if st.Action != "update" || len(st.Overwrites) != 0 {
		t.Errorf("更新：action=%s，msvcp140.dll 的原件已经存过，不应再列为覆盖，实际 %v", st.Action, st.Overwrites)
	}
	res := mustApplyCore(t, st, nil)

	var cfg struct {
		Settings map[string]int `json:"settings"`
	}
	if err := json.Unmarshal([]byte(read(t, win64Path("config.json"))), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Settings["A"] != 5 || cfg.Settings["B"] != 2 {
		t.Errorf("config.json 应保留用户的 A=5 并加入新键 B=2，实际 %v", cfg.Settings)
	}
	if exists(win64Path("ArkApi/old.txt")) {
		t.Error("新版本没有的文件应被移走")
	}
	if got := read(t, filepath.Join(res.Backup, "previous", "ArkApi", "old.txt")); got != "old" {
		t.Errorf("被移走的文件应在备份里，实际 %q", got)
	}
	if got := read(t, win64Path("ArkApi/AsaApi.dll")); got != amd64PE+"v2" {
		t.Error("AsaApi.dll 应是新版本")
	}
	m, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.Core.Version != "2.04" || m.Core.Overwritten["msvcp140.dll"] == "" {
		t.Errorf("清单 = %+v", m.Core)
	}
	if _, ok := m.Core.Files["ArkApi/old.txt"]; ok {
		t.Error("清单不应再记录已移走的文件")
	}
	if st, _ := Status(); len(st.ModifiedFiles) != 0 {
		t.Errorf("config.json 是给用户改的，不应报告为被改动：%v", st.ModifiedFiles)
	}

	if _, err := UninstallCore(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, win64Path("msvcp140.dll")); got != gameMsvcp {
		t.Errorf("卸载后应还原第一次安装前的游戏原件，实际 %q", got)
	}
}

// 任何一步失败都回滚：server-files 与清单逐文件回到操作前。
func TestCoreInstallRollsBackOnFailure(t *testing.T) {
	t.Run("全部文件都已换位、最后写清单失败", func(t *testing.T) {
		setupCoreEnv(t)
		before := snapshotTree(t, serverWin64Dir())
		st := mustStageCore(t, coreFiles("v1", arkConfig, nil), "AsaApi_2.03.zip")
		// 清单的临时文件位置放一个目录：写清单是最后一步，此前所有文件都已换位
		if err := os.MkdirAll(manifestPath()+".tmp", 0755); err != nil {
			t.Fatal(err)
		}

		_, err := ApplyCore(st.Token, nil, nil, false)
		if err == nil || !strings.Contains(err.Error(), "已回滚") {
			t.Fatalf("应报告失败并已回滚，实际 %v", err)
		}
		assertTreeEqual(t, before, snapshotTree(t, serverWin64Dir()), "回滚后的 Win64 应与安装前逐文件一致")
		if exists(originalsDir()) {
			t.Error("回滚后 originals 应被撤掉")
		}
	})

	t.Run("更新到一半失败", func(t *testing.T) {
		setupCoreEnv(t)
		mustApplyCore(t, mustStageCore(t, coreFiles("v1", arkConfig, nil), "AsaApi_2.03.zip"), nil)
		before := snapshotTree(t, serverWin64Dir())
		manifestBefore := read(t, manifestPath())

		st := mustStageCore(t, coreFiles("v2", `{"settings":{"New":1}}`,
			map[string]string{"ArkApi/new.txt": "new", "ArkApi/pdbignores.txt": ""}), "AsaApi_2.04.zip")
		calls := 0
		coreFaultHook = func(string) error {
			if calls++; calls == 4 {
				return errors.New("注入的失败")
			}
			return nil
		}
		_, err := ApplyCore(st.Token, nil, nil, false)
		if err == nil || !strings.Contains(err.Error(), "注入的失败") || !strings.Contains(err.Error(), "已回滚") {
			t.Fatalf("应报告注入的失败并已回滚，实际 %v", err)
		}
		assertTreeEqual(t, before, snapshotTree(t, serverWin64Dir()), "回滚后的 Win64 应与更新前逐文件一致")
		if got := read(t, manifestPath()); got != manifestBefore {
			t.Error("回滚后清单应保持更新前的内容")
		}
	})
}

// 装上之后 msvcp140.dll 被外部换过（Steam 校验还原了游戏版本）：卸载时保留现状，不拿旧原件去覆盖。
func TestCoreUninstallKeepsGameFileReplacedAfterInstall(t *testing.T) {
	setupCoreEnv(t)
	mustApplyCore(t, mustStageCore(t, coreFiles("v1", arkConfig, nil), "AsaApi_2.03.zip"), nil)
	write(t, win64Path("msvcp140.dll"), "msvcp steam")
	if st, _ := Status(); !slices.Contains(st.ModifiedFiles, "msvcp140.dll") {
		t.Errorf("被外部换过的文件应报告为改动：%v", st.ModifiedFiles)
	}

	un, err := UninstallCore()
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, win64Path("msvcp140.dll")); got != "msvcp steam" {
		t.Errorf("被外部换过的 msvcp140.dll 应保留现状，实际 %q", got)
	}
	if len(un.Restored) != 0 || !containsText(un.Warnings, "msvcp140.dll") {
		t.Errorf("卸载结果 = %+v", un)
	}
	if got := read(t, filepath.Join(un.Backup, "originals", "msvcp140.dll")); got != gameMsvcp {
		t.Error("没用上的原件应移入备份，而不是丢掉")
	}
}

// 手工装的主程序（没有清单）：按固定清单卸载，msvcp140.dll 保留（方案 D3、§6.2）。
func TestCoreUninstallWithoutManifest(t *testing.T) {
	setupCoreEnv(t)
	for rel, content := range coreFiles("manual", arkConfig, nil) {
		write(t, win64Path(rel), content)
	}
	write(t, win64Path("ArkApi/Cache/offsets.cache"), "cache")

	un, err := UninstallCore()
	if err != nil {
		t.Fatal(err)
	}
	if un.Managed {
		t.Error("没有清单，应按手工安装处理")
	}
	for _, rel := range []string{"AsaApiLoader.exe", "AsaApiLoader.pdb", "libcrypto-3-x64.dll", "libssl-3-x64.dll",
		"msdia140.dll", "config.json", "Lib", "ArkApi"} {
		if exists(win64Path(rel)) {
			t.Errorf("%s 应被移除", rel)
		}
	}
	if got := read(t, win64Path("msvcp140.dll")); got != arkApiMsvcp {
		t.Errorf("无法判断来源的 msvcp140.dll 应保留，实际 %q", got)
	}
	if !containsText(un.Warnings, "msvcp140.dll") {
		t.Errorf("应提示 msvcp140.dll 被保留：%v", un.Warnings)
	}
	if read(t, win64Path("ArkAscendedServer.exe")) != "game exe" || !exists(win64Path("vcruntime140.dll")) {
		t.Error("游戏文件不应被动")
	}

	t.Run("不像 ArkApi 的 config.json 不删", func(t *testing.T) {
		setupCoreEnv(t)
		write(t, win64Path("AsaApiLoader.exe"), amd64PE)
		write(t, win64Path("config.json"), `{"foo":1}`)
		if _, err := UninstallCore(); err != nil {
			t.Fatal(err)
		}
		if !exists(win64Path("config.json")) {
			t.Error("不是 ArkApi 的 config.json 不应被移除")
		}
	})
}

// server-files 的 ArkApi/Plugins 里还有插件（有实例尚未迁移）时保留不动；Cache 随主程序移入备份。
func TestCoreUninstallLeavesLegacyServerPlugins(t *testing.T) {
	setupCoreEnv(t)
	mustApplyCore(t, mustStageCore(t, coreFiles("v1", arkConfig, nil), "AsaApi_2.03.zip"), nil)
	write(t, win64Path("ArkApi/Plugins/Legacy/Legacy.dll"), "legacy")
	write(t, win64Path("ArkApi/Cache/offsets.cache"), "cache")

	un, err := UninstallCore()
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, win64Path("ArkApi/Plugins/Legacy/Legacy.dll")); got != "legacy" {
		t.Error("尚未迁移的实例还在用的全局插件不应被移走")
	}
	if !containsText(un.Warnings, "Plugins") {
		t.Errorf("应提示保留了 Plugins：%v", un.Warnings)
	}
	if exists(win64Path("ArkApi/Cache")) {
		t.Error("Cache 应随主程序移入备份")
	}
	if got := read(t, filepath.Join(un.Backup, "previous", "ArkApi", "Cache", "offsets.cache")); got != "cache" {
		t.Error("Cache 应在备份里")
	}
	if installer.ArkApiInstalled() {
		t.Error("卸载后不应再检测到主程序")
	}
}

// 附带插件不写进 server-files，只装进确认时列出的实例（方案 §6.1、§1.1）。
func TestCoreBundledPluginsOnlyIntoListedInstances(t *testing.T) {
	setupCoreEnv(t, "a", "b")
	st := mustStageCore(t, coreFiles("v1", arkConfig, permissionsBundled()), "AsaApi_2.03.zip")
	if len(st.Bundled) != 1 || st.Bundled[0].Name != "Permissions" || len(st.Bundled[0].Errors) != 0 {
		t.Fatalf("附带插件 = %+v", st.Bundled)
	}
	if !containsText(st.Bundled[0].Warnings, "Ark:SA Permissions") {
		t.Errorf("FullName 与目录名不同应当警告（方案 D1）：%v", st.Bundled[0].Warnings)
	}
	if n := len(st.BundledTargets["Permissions"]); n != 2 {
		t.Errorf("附带插件的目标实例表应列出全部 2 个实例，实际 %d", n)
	}

	// 选了包里没有的插件：整个不做，暂存包留着，改了选择可以再确认
	if _, err := ApplyCore(st.Token, nil, map[string][]string{"Nope": {"a"}}, false); err == nil {
		t.Fatal("选择包里没有的附带插件应被拒")
	}
	if installer.ArkApiInstalled() {
		t.Fatal("被拒的确认不应动 server-files")
	}

	res := mustApplyCore(t, st, map[string][]string{"Permissions": {"a"}})
	if len(res.Results) != 1 || res.Results[0].Instance != "a" || res.Results[0].Plugin != "Permissions" || !res.Results[0].OK {
		t.Fatalf("附带插件结果 = %+v", res.Results)
	}
	if !exists(filepath.Join(pluginDir("a", "Permissions"), "Permissions.dll")) {
		t.Error("实例 a 应装上 Permissions")
	}
	if exists(pluginDir("b", "Permissions")) {
		t.Error("没勾选的实例 b 不应被装上")
	}
	if exists(win64Path("ArkApi/Plugins/Permissions")) {
		t.Error("附带插件不应写进 server-files")
	}
}

// server-files 正忙（Steam 更新等）时拒绝，暂存包保留，空闲之后可以再确认。
func TestCoreApplyRefusedWhileServerFilesBusy(t *testing.T) {
	setupCoreEnv(t)
	st := mustStageCore(t, coreFiles("v1", arkConfig, nil), "AsaApi_2.03.zip")
	end, err := installer.BeginArkApiWrite()
	if err != nil {
		t.Fatal(err)
	}
	_, applyErr := ApplyCore(st.Token, nil, nil, false)
	_, uninstallErr := UninstallCore()
	end()
	if applyErr == nil || uninstallErr == nil {
		t.Fatalf("server-files 正忙时应拒绝：apply=%v uninstall=%v", applyErr, uninstallErr)
	}
	if installer.ArkApiInstalled() {
		t.Fatal("被拒时不应动 server-files")
	}
	mustApplyCore(t, st, nil)
}

// 版本号可以在确认时改；主程序版本已知后，插件包的 MinApiVersion 检查随之生效。
func TestCoreVersionFeedsPluginMinApiCheck(t *testing.T) {
	setupCoreEnv(t, "a")
	st := mustStageCore(t, coreFiles("v1", arkConfig, nil), "AsaApi.zip")
	if st.Version != "" {
		t.Errorf("文件名里没有版本号时应为空，实际 %q", st.Version)
	}
	ver := " 2.03 "
	if _, err := ApplyCore(st.Token, &ver, nil, false); err != nil {
		t.Fatal(err)
	}
	if v := installedCoreVersion(); v != "2.03" {
		t.Fatalf("确认时填的版本号应写入清单，实际 %q", v)
	}

	info := func(min string) map[string]string {
		return map[string]string{"PluginInfo.json": `{"FullName":"Tidy","Version":1.3,"MinApiVersion":` + min + `}`}
	}
	tooNew := mustStage(t, pluginZip(t, "Tidy", "1.3", info("2.1")), "")
	defer Discard(tooNew.Token)
	if !containsText(tooNew.Warnings, "2.03") {
		t.Errorf("MinApiVersion 2.1 高于已装的 2.03，应当警告：%v", tooNew.Warnings)
	}
	ok := mustStage(t, pluginZip(t, "Tidy", "1.3", info("2")), "")
	defer Discard(ok.Token)
	if len(ok.Warnings) != 0 {
		t.Errorf("MinApiVersion 2 不高于 2.03，不应警告：%v", ok.Warnings)
	}
}

func TestCoreStatusReportsExternalChanges(t *testing.T) {
	setupCoreEnv(t)
	mustApplyCore(t, mustStageCore(t, coreFiles("v1", arkConfig, nil), "AsaApi_2.03.zip"), nil)
	write(t, win64Path("ArkApi/AsaApi.dll"), amd64PE+"patched")
	if err := os.Remove(win64Path("Lib/AsaApi.lib")); err != nil {
		t.Fatal(err)
	}
	write(t, win64Path("config.json"), `{"settings":{"x":1}}`) // 给用户改的，不算

	st, err := Status()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.ModifiedFiles, []string{"ArkApi/AsaApi.dll"}) || !slices.Equal(st.MissingFiles, []string{"Lib/AsaApi.lib"}) {
		t.Errorf("modified=%v missing=%v", st.ModifiedFiles, st.MissingFiles)
	}
}

// 手工解压出来的是 arkapi/：装进同一个目录，不在旁边再建一个 ArkApi/（Linux 上那是两个目录，
// 镜像里会出现两份主程序，方案 §4.2 约束 5）。
func TestCoreInstallFollowsOnDiskCaseOfArkApiDir(t *testing.T) {
	setupCoreEnv(t)
	write(t, win64Path("arkapi/Cache/offsets.cache"), "cache")
	mustApplyCore(t, mustStageCore(t, coreFiles("v1", arkConfig, nil), "AsaApi_2.03.zip"), nil)

	entries, err := os.ReadDir(serverWin64Dir())
	if err != nil {
		t.Fatal(err)
	}
	var ark []string
	for _, e := range entries {
		if strings.EqualFold(e.Name(), "arkapi") {
			ark = append(ark, e.Name())
		}
	}
	if !slices.Equal(ark, []string{"arkapi"}) {
		t.Fatalf("Win64 下应只有盘上原有的 arkapi 目录，实际 %v", ark)
	}
	if got := read(t, filepath.Join(serverWin64Dir(), "arkapi", "AsaApi.dll")); got != amd64PE+"v1" {
		t.Error("AsaApi.dll 应装进 arkapi/")
	}

	if _, err := UninstallCore(); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(serverWin64Dir(), "arkapi")) {
		t.Error("卸载后 arkapi/ 应被整个移入备份")
	}
}
