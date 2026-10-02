package appconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 核心优先级判据：config.yaml 里的 basedir 字段是最高权威，即使用 ASA_CFG 精确
// 指定了 config.yaml 所在目录，字段依然生效——ASA_CFG 只管"去哪儿找文件"，
// 不参与"文件里写了什么就听什么"这条规则。
func TestLoad_FileBasedirWinsEvenWithASACFG(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "basedir: \"C:\\\\data\\\\real\"\n")
	t.Setenv("ASA_CFG", dir)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := `C:\data\real`; got != want {
		t.Errorf("文件里的 basedir 字段应该生效，期望 %q，实际 %q", want, got)
	}
}

// 三级查找最高一级：ASA_CFG 非空时完整覆盖，即使 exe 同级与系统固定目录也存在
// config.yaml 且内容不同，两者都不会被读取。
func TestLoad_ASACFGWinsOverExeAndSystemDir(t *testing.T) {
	cfgDir, exeDir, sysDir := t.TempDir(), t.TempDir(), t.TempDir()
	writeConfig(t, cfgDir, "server:\n  port: 11111\n")
	writeConfig(t, exeDir, "server:\n  port: 22222\n")
	writeConfig(t, sysDir, "server:\n  port: 33333\n")
	OverrideSearchDirsForTest(t, exeDir, sysDir)
	t.Setenv("ASA_CFG", cfgDir)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != cfgDir {
		t.Errorf("ASA_CFG 应优先于 exe 同级与系统固定目录，期望 %q，实际 %q", cfgDir, got)
	}
	if Get().Server.Port != 11111 {
		t.Errorf("应读到 ASA_CFG 目录里的配置，端口期望 11111，实际 %d", Get().Server.Port)
	}
}

// G2 判据：两级查找中 exe 同级优先于系统固定目录。
func TestLoad_TwoLevelSearch_PrefersExeDir(t *testing.T) {
	exeDir, sysDir := t.TempDir(), t.TempDir()
	writeConfig(t, exeDir, "basedir: \"\"\n")
	writeConfig(t, sysDir, "basedir: \"/should/not/be/used\"\n")
	OverrideSearchDirsForTest(t, exeDir, sysDir)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != exeDir {
		t.Errorf("exe 同级目录存在 config.yaml 时应优先于系统固定目录，期望 %q，实际 %q", exeDir, got)
	}
}

// G2 判据：exe 同级没有 config.yaml 时，第 2 级查找要真的生效（覆盖开发/调试场景）。
func TestLoad_TwoLevelSearch_FallsBackToSystemDir(t *testing.T) {
	exeDir, sysDir := t.TempDir(), t.TempDir()
	// exeDir 里故意不放 config.yaml
	writeConfig(t, sysDir, "server:\n  port: 28080\n")
	OverrideSearchDirsForTest(t, exeDir, sysDir)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != sysDir {
		t.Errorf("exe 同级没有 config.yaml 时应回落到系统固定目录，期望 %q，实际 %q", sysDir, got)
	}
	if Get().Server.Port != 28080 {
		t.Errorf("应该读到系统固定目录里的配置，端口期望 28080，实际 %d", Get().Server.Port)
	}
	// exe 同级目录本身不应该被污染——第 2 级只是"读"，不该在那里生成任何文件。
	if _, err := os.Stat(filepath.Join(exeDir, ConfigFileName)); err == nil {
		t.Error("回落到系统固定目录时不应该在 exe 同级生成 config.yaml")
	}
}

// G1/G2 判据：老部署原地升级后 BaseDir 与升级前完全一致，无需任何手动迁移或补字段——
// 旧版 config.yaml 没有 basedir 字段，字段留空时，BaseDir 就是
// 这份 config.yaml 自己所在的目录，和它今天的实际行为完全一致。
func TestLoad_OldConfigWithoutBasedirField_CompatPath(t *testing.T) {
	exeDir := t.TempDir()
	// 模拟老版本的 config.yaml：完全不含 basedir 字段。
	writeConfig(t, exeDir, "server:\n  port: 19193\nauth:\n  enabled: false\n")
	OverrideSearchDirsForTest(t, exeDir, filepath.Join(exeDir, "does-not-exist"))

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != exeDir {
		t.Errorf("无 basedir 字段时应回落到 config.yaml 自己所在目录，期望 %q，实际 %q", exeDir, got)
	}
}

// 两级都没有 config.yaml 时，Load 落回 exe 同级并报告缺失，但**不生成任何文件或目录**
// ——以前这里会在 exe 同级生成默认模板，2026-10-02 起生成配置只走 InitConfig
// （docs/APPCONFIG_BASEDIR_PLAN.md Part 2 P2-3 第 6 条）。系统固定目录完全不受影响。
func TestLoad_NeitherLevelHasConfig_WritesNothing(t *testing.T) {
	exeDir := t.TempDir()
	sysDir := filepath.Join(t.TempDir(), "nested", "does-not-exist")
	OverrideSearchDirsForTest(t, exeDir, sysDir)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != exeDir {
		t.Errorf("两级都没有时应落回 exe 同级，期望 %q，实际 %q", exeDir, got)
	}
	if !ConfigMissing() {
		t.Error("两级都没有时 ConfigMissing 应为 true")
	}
	if _, err := os.Stat(filepath.Join(exeDir, ConfigFileName)); !os.IsNotExist(err) {
		t.Errorf("不应在 exe 同级生成 config.yaml: %v", err)
	}
	if _, err := os.Stat(sysDir); err == nil {
		t.Error("系统固定目录不存在时不应该被意外创建出来")
	}
}

// 两级查找基于 basedir 字段：exe 同级的 config.yaml 显式填了 basedir 时，
// 应以该字段为准，而不是 config.yaml 自己所在的目录。
func TestLoad_TwoLevelSearch_UsesBasedirFieldWhenSet(t *testing.T) {
	exeDir := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "data")
	writeConfig(t, exeDir, "basedir: \""+filepath.ToSlash(dataDir)+"\"\n")
	OverrideSearchDirsForTest(t, exeDir, t.TempDir())

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.ToSlash(dataDir); got != want {
		t.Errorf("应使用 basedir 字段指向的目录，期望 %q，实际 %q", want, got)
	}
}

// 遗留的 ASA_BASEDIR 已移除：设了也不影响文件里写明的 basedir。
func TestLoad_FileBasedirWinsOverEnvASABaseDir(t *testing.T) {
	exeDir := t.TempDir()
	writeConfig(t, exeDir, "basedir: \"/from/file\"\n")
	OverrideSearchDirsForTest(t, exeDir, t.TempDir())
	t.Setenv("ASA_BASEDIR", "/from/env")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "/from/file" {
		t.Errorf("文件里的 basedir 字段不应受 ASA_BASEDIR 影响，期望 %q，实际 %q", "/from/file", got)
	}
}

// basedir 字段留空时也不再看 ASA_BASEDIR，直接回落到 config.yaml 所在目录。
// Get().BaseDir 也必须是空的：decodeFile 开着 viper 的 AutomaticEnv，basedir 这个键
// 天然对应 ASA_BASEDIR，这里钉住「已移除的变量没有经自动映射复活」。CheckFile
// （config validate 走它）同理。
func TestLoad_ASABaseDirIsIgnored(t *testing.T) {
	exeDir := t.TempDir()
	writeConfig(t, exeDir, "basedir: \"\"\n")
	OverrideSearchDirsForTest(t, exeDir, t.TempDir())
	t.Setenv("ASA_BASEDIR", "/from/env")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != exeDir {
		t.Errorf("basedir 字段留空时应回落到 config.yaml 所在目录，期望 %q，实际 %q", exeDir, got)
	}
	if b := Get().BaseDir; b != "" {
		t.Errorf("Get().BaseDir 应只反映文件内容（空），实际 %q —— ASA_BASEDIR 经 AutomaticEnv 复活了", b)
	}
	cfg, err := CheckFile(filepath.Join(exeDir, ConfigFileName))
	if err != nil {
		t.Fatalf("CheckFile: %v", err)
	}
	if cfg.BaseDir != "" {
		t.Errorf("CheckFile 的 BaseDir 应为空，实际 %q", cfg.BaseDir)
	}
}

// 配置文件别处写错时，Load 返回错误，但 BaseDir 仍按文件里的 basedir 字段给出：
// 调用方要用它显示正确的数据目录、把错误写进正确目录的日志。回落到配置目录的话，
// 配置一写错数据目录就悄悄换了。
func TestLoad_InvalidConfigKeepsFileBasedir(t *testing.T) {
	exeDir := t.TempDir()
	writeConfig(t, exeDir, "basedir: \"/from/file\"\nauth:\n  lan_bypass:\n    networks:\n      - 哦豁\n")
	OverrideSearchDirsForTest(t, exeDir, t.TempDir())

	got, err := Load()
	if err == nil {
		t.Fatal("非法 networks 应返回错误")
	}
	if got != "/from/file" {
		t.Errorf("配置无效时 BaseDir 仍应取文件里的 basedir，期望 %q，实际 %q", "/from/file", got)
	}
}

// YAML 本身读不出来时拿不到 basedir，只能回落到配置目录。
func TestLoad_UnparsableConfigFallsBackToConfigDir(t *testing.T) {
	exeDir := t.TempDir()
	writeConfig(t, exeDir, "basedir: \"/from/file\"\nserver: [\n")
	OverrideSearchDirsForTest(t, exeDir, t.TempDir())

	got, err := Load()
	if err == nil {
		t.Fatal("坏掉的 YAML 应返回错误")
	}
	if got != exeDir {
		t.Errorf("YAML 读不出来时应回落到配置目录，期望 %q，实际 %q", exeDir, got)
	}
}

func TestLegacyBaseDirEnv(t *testing.T) {
	t.Setenv("ASA_BASEDIR", "")
	if _, set := LegacyBaseDirEnv(); set {
		t.Error("没设时应报告未设置")
	}
	if hint := LegacyBaseDirHint("/data", "/cfg/config.yaml"); hint != "" {
		t.Errorf("没设时不应有提示，实际 %q", hint)
	}

	t.Setenv("ASA_BASEDIR", "/old")
	if v, set := LegacyBaseDirEnv(); !set || v != "/old" {
		t.Errorf("LegacyBaseDirEnv = %q, %v", v, set)
	}
	hint := LegacyBaseDirHint("/data", "/cfg/config.yaml")
	for _, want := range []string{"已不再生效", "/data", "/cfg/config.yaml", `basedir: "/old"`} {
		if !strings.Contains(hint, want) {
			t.Errorf("提示应包含 %q，实际 %q", want, hint)
		}
	}
	// 与实际数据目录相同：只需要提示可以删掉，不要让人去改配置。
	same := LegacyBaseDirHint("/old", "/cfg/config.yaml")
	if !strings.Contains(same, "可以删掉") || strings.Contains(same, "basedir:") {
		t.Errorf("目录相同时只应提示删掉变量，实际 %q", same)
	}
}

// 字段留空时，最终回落到 config.yaml 自己所在的目录——
// 这正是现有全部部署的隐式状态，升级后行为不能变。
func TestLoad_NoFileFieldNoEnv_FallsBackToConfigDir(t *testing.T) {
	exeDir := t.TempDir()
	writeConfig(t, exeDir, "basedir: \"\"\n")
	OverrideSearchDirsForTest(t, exeDir, t.TempDir())

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != exeDir {
		t.Errorf("字段与环境变量都没有时应回落到 config.yaml 所在目录，期望 %q，实际 %q", exeDir, got)
	}
}

// 第三步防御性兜底：连"该读哪一份 config.yaml"都定不出来时（模拟 os.Executable()
// 报错——没设 ASA_CFG、exe 同级解析本身失败），Load 仍必须给出一个可用、非空的
// BaseDir，而不是直接把空字符串或错误捅给调用方。
//
// 注：这里只验证兜底值本身；bug 修复轮里确认过 pkg/logger 的 WithConsole() 写的是
// init() 时就已经捕获的 os.Stdout 文件对象，测试里重新赋值 os.Stdout 变量捕获不到
// 它的输出，要严格断言警告文案得给 pkg/logger 补一个可注入的测试 sink，属于那个包
// 的改动，不在本次 appconfig 改造范围内，此处不做这一半的断言。
func TestLoad_DefensiveFallbackWhenLocateFails(t *testing.T) {
	t.Setenv("ASA_CFG", "")
	origExe, origSys := executableDirFn, systemConfigDirFn
	executableDirFn = func() (string, error) { return "", os.ErrPermission }
	systemConfigDirFn = func() string { return "" }
	t.Cleanup(func() {
		executableDirFn, systemConfigDirFn = origExe, origSys
	})

	got, err := Load()
	if err == nil {
		t.Fatal("定位阶段失败时应返回错误")
	}
	if got == "" {
		t.Error("即使定位失败，也必须给出一个非空的兜底 BaseDir")
	}
}
