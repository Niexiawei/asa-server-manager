package appconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// clearASABaseDir 保证测试不受开发机上可能已经设置的 ASA_BASEDIR 干扰，
// 除非测试自己显式用 t.Setenv 给它一个值。
func clearASABaseDir(t *testing.T) {
	t.Helper()
	t.Setenv("ASA_BASEDIR", "")
}

// 核心优先级判据：config.yaml 里的 basedir 字段是最高权威，即使用 ASA_CFG 精确
// 指定了 config.yaml 所在目录，字段依然生效——ASA_CFG 只管"去哪儿找文件"，
// 不参与"文件里写了什么就听什么"这条规则。
func TestLoad_FileBasedirWinsEvenWithASACFG(t *testing.T) {
	clearASABaseDir(t)
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
	clearASABaseDir(t)
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
	clearASABaseDir(t)
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
	clearASABaseDir(t)
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
// 旧版 config.yaml 没有 basedir 字段，字段留空且没有 ASA_BASEDIR 时，BaseDir 就是
// 这份 config.yaml 自己所在的目录，和它今天的实际行为完全一致。
func TestLoad_OldConfigWithoutBasedirField_CompatPath(t *testing.T) {
	clearASABaseDir(t)
	exeDir := t.TempDir()
	// 模拟老版本的 config.yaml：完全不含 basedir 字段。
	writeConfig(t, exeDir, "server:\n  port: 19193\nauth:\n  enabled: false\n")
	OverrideSearchDirsForTest(t, exeDir, filepath.Join(exeDir, "does-not-exist"))

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != exeDir {
		t.Errorf("无 basedir 字段且无环境变量时应回落到 config.yaml 自己所在目录，期望 %q，实际 %q", exeDir, got)
	}
}

// G1 判据：两级都没有 config.yaml 时，启动（这一步）不产生任何目录，只在 exe 同级
// 生成默认 config.yaml；系统固定目录完全不受影响。
func TestLoad_NeitherLevelHasConfig_GeneratesDefaultAtExeDir(t *testing.T) {
	clearASABaseDir(t)
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
	if _, err := os.Stat(filepath.Join(exeDir, ConfigFileName)); err != nil {
		t.Errorf("应在 exe 同级生成默认 config.yaml: %v", err)
	}
	if _, err := os.Stat(sysDir); err == nil {
		t.Error("系统固定目录不存在时不应该被意外创建出来")
	}
}

// 两级查找基于 basedir 字段：exe 同级的 config.yaml 显式填了 basedir 时，
// 应以该字段为准，而不是 config.yaml 自己所在的目录。
func TestLoad_TwoLevelSearch_UsesBasedirFieldWhenSet(t *testing.T) {
	clearASABaseDir(t)
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

// 本次改造的核心目的：数据目录的权威从环境变量搬进了配置文件。basedir 字段非空时，
// 必须赢过 ASA_BASEDIR 环境变量——不能像改造前那样让环境变量说了算。
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
		t.Errorf("文件里的 basedir 字段应优先于 ASA_BASEDIR 环境变量，期望 %q，实际 %q", "/from/file", got)
	}
}

// basedir 字段留空时，ASA_BASEDIR 依然是合法的兜底来源（不是被整个废弃，只是降级），
// 优先级仍然高于"config.yaml 自己所在目录"这个最终回落。
func TestLoad_EnvASABaseDirFallsBackWhenFileFieldEmpty(t *testing.T) {
	exeDir := t.TempDir()
	writeConfig(t, exeDir, "basedir: \"\"\n")
	OverrideSearchDirsForTest(t, exeDir, t.TempDir())
	t.Setenv("ASA_BASEDIR", "/from/env")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "/from/env" {
		t.Errorf("basedir 字段留空时应回落到 ASA_BASEDIR 环境变量，期望 %q，实际 %q", "/from/env", got)
	}
}

// 字段留空、环境变量也没设时，最终回落到 config.yaml 自己所在的目录——
// 这正是现有全部部署的隐式状态，升级后行为不能变。
func TestLoad_NoFileFieldNoEnv_FallsBackToConfigDir(t *testing.T) {
	clearASABaseDir(t)
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
	clearASABaseDir(t)
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
