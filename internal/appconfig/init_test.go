package appconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitConfig_WritesOnlyTheConfigFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "cfg") // 目录还不存在
	dataDir := filepath.Join(t.TempDir(), "data")

	path, err := InitConfig(InitOptions{Dir: dir, BaseDir: dataDir, Lang: LangEN})
	if err != nil {
		t.Fatalf("InitConfig: %v", err)
	}
	if want := filepath.Join(dir, ConfigFileName); path != want {
		t.Errorf("返回路径 %q，期望 %q", path, want)
	}
	if got := fileOnlyBaseDir(dir); got != dataDir {
		t.Errorf("basedir 应为 %q，实际 %q", dataDir, got)
	}

	// 只写 config.yaml：不留临时文件，也不建任何数据子目录。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ConfigFileName {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("配置目录里应只有 %s，实际 %v", ConfigFileName, names)
	}
	// InitConfig 不校验、不创建数据目录——那是 ValidateBaseDir / setup 的事。
	if _, err := os.Stat(dataDir); err == nil {
		t.Error("InitConfig 不应创建数据目录")
	}
}

func TestInitConfig_RefusesExistingWithoutForce(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "basedir: \"/keep/me\"\n")

	_, err := InitConfig(InitOptions{Dir: dir})
	if !errors.Is(err, ErrConfigExists) {
		t.Fatalf("目标已存在时应返回 ErrConfigExists，实际 %v", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, ConfigFileName)) {
		t.Errorf("错误信息应带上路径，实际 %q", err)
	}
	if got := fileOnlyBaseDir(dir); got != "/keep/me" {
		t.Errorf("拒绝时不应改动原文件，basedir 变成了 %q", got)
	}
}

func TestInitConfig_ForceBacksUpThenOverwrites(t *testing.T) {
	dir := t.TempDir()
	const old = "basedir: \"/old\"\n"
	writeConfig(t, dir, old)

	if _, err := InitConfig(InitOptions{Dir: dir, BaseDir: filepath.Join(dir, "new"), Force: true}); err != nil {
		t.Fatalf("InitConfig --force: %v", err)
	}
	if got := fileOnlyBaseDir(dir); got != filepath.Join(dir, "new") {
		t.Errorf("覆盖后 basedir 应为新值，实际 %q", got)
	}

	baks, _ := filepath.Glob(filepath.Join(dir, ConfigFileName+".bak-*"))
	if len(baks) != 1 {
		t.Fatalf("应恰好留下一份备份，实际 %v", baks)
	}
	raw, err := os.ReadFile(baks[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != old {
		t.Errorf("备份内容应为原文件，实际 %q", raw)
	}
}

func TestInitConfig_RelativeBaseDirBecomesAbsolute(t *testing.T) {
	dir := t.TempDir()
	if _, err := InitConfig(InitOptions{Dir: dir, BaseDir: "relative-data"}); err != nil {
		t.Fatalf("InitConfig: %v", err)
	}
	got := fileOnlyBaseDir(dir)
	if !filepath.IsAbs(got) || filepath.Base(got) != "relative-data" {
		t.Errorf("相对 basedir 应转为绝对路径，实际 %q", got)
	}
}

func TestInitConfig_RejectsUnknownLang(t *testing.T) {
	dir := t.TempDir()
	if _, err := InitConfig(InitOptions{Dir: dir, Lang: "fr"}); err == nil {
		t.Fatal("不支持的语言应报错")
	}
	if _, err := os.Stat(filepath.Join(dir, ConfigFileName)); err == nil {
		t.Error("参数错误时不应写出文件")
	}
}

func TestDefaultInitDir(t *testing.T) {
	exeDir := t.TempDir()
	OverrideSearchDirsForTest(t, exeDir, t.TempDir())

	t.Setenv("ASA_CFG", "")
	if got, err := DefaultInitDir(); err != nil || got != exeDir {
		t.Errorf("未设 ASA_CFG 时应为 exe 同级 %q，实际 %q, %v", exeDir, got, err)
	}

	cfgDir := t.TempDir()
	t.Setenv("ASA_CFG", cfgDir)
	if got, err := DefaultInitDir(); err != nil || got != cfgDir {
		t.Errorf("设了 ASA_CFG 时应为 %q，实际 %q, %v", cfgDir, got, err)
	}
}

// Load 只读：配置缺失时只用默认值，不落盘，连配置目录都不建。
func TestLoadMissingConfigWritesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("ASA_CFG", dir)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != dir {
		t.Errorf("BaseDir 应回落到配置目录 %q，实际 %q", dir, got)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("Load 不应创建配置目录")
	}
	if !ConfigMissing() {
		t.Error("ConfigMissing() 应为 true")
	}
	if want := filepath.Join(dir, ConfigFileName); ConfigPath() != want {
		t.Errorf("ConfigPath() = %q，期望 %q", ConfigPath(), want)
	}
	if Get().Server.Port != 19193 {
		t.Errorf("应使用默认端口，实际 %d", Get().Server.Port)
	}
}

// ConfigMissing 跟着文件走：InitConfig 生成之后再 Load，报告存在。
func TestConfigMissingTracksFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadFrom(t, dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ConfigMissing() {
		t.Error("没有配置时 ConfigMissing() 应为 true")
	}
	if _, err := os.Stat(filepath.Join(dir, ConfigFileName)); !os.IsNotExist(err) {
		t.Errorf("Load 不应生成模板: %v", err)
	}

	if _, err := InitConfig(InitOptions{Dir: dir}); err != nil {
		t.Fatalf("InitConfig: %v", err)
	}
	if _, err := loadFrom(t, dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ConfigMissing() {
		t.Error("文件已存在时 ConfigMissing() 应为 false")
	}
}

// ConfigPath 必须指向真正被读的那一份——报错提示靠它告诉用户去改哪个文件。
func TestConfigPathFollowsLookupLevel(t *testing.T) {
	t.Setenv("ASA_CFG", "")

	exeDir, sysDir := t.TempDir(), t.TempDir()
	OverrideSearchDirsForTest(t, exeDir, sysDir)
	writeConfig(t, sysDir, "server:\n  port: 19193\n")
	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(sysDir, ConfigFileName); ConfigPath() != want {
		t.Errorf("只有系统目录有配置时 ConfigPath 应为 %q，实际 %q", want, ConfigPath())
	}

	writeConfig(t, exeDir, "server:\n  port: 19193\n")
	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(exeDir, ConfigFileName); ConfigPath() != want {
		t.Errorf("exe 同级有配置时 ConfigPath 应为 %q，实际 %q", want, ConfigPath())
	}
}

func TestCheckFile(t *testing.T) {
	dir := t.TempDir()

	if _, err := CheckFile(filepath.Join(dir, ConfigFileName)); err == nil {
		t.Error("文件不存在时应报错")
	}

	before := Get()
	writeConfig(t, dir, "basedir: \"/data\"\nserver:\n  port: 8443\n")
	cfg, err := CheckFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
	if cfg.Server.Port != 8443 || cfg.BaseDir != "/data" {
		t.Errorf("解析结果不对：port=%d basedir=%q", cfg.Server.Port, cfg.BaseDir)
	}
	if Get() != before {
		t.Error("CheckFile 不应改变 Get() 的返回值")
	}

	writeConfig(t, dir, "server:\n  port: 70000\n")
	if _, err := CheckFile(filepath.Join(dir, ConfigFileName)); err == nil {
		t.Error("非法端口应校验失败")
	}
}
