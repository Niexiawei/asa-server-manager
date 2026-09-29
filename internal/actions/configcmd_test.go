package actions

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asa-server/internal/appconfig"
	"asa-server/pkg/userenv"
)

// configEnv 隔离三级查找与环境变量，并按「main.go 以 config 子命令启动」的方式
// 加载一次（WithoutAutoGenerate），让 ConfigMissing/ConfigPath 反映 exe/sys 目录的真实状态。
type configEnv struct {
	exeDir, sysDir string
}

func newConfigEnv(t *testing.T) *configEnv {
	t.Helper()
	e := &configEnv{exeDir: t.TempDir(), sysDir: t.TempDir()}
	appconfig.OverrideSearchDirsForTest(t, e.exeDir, e.sysDir)
	t.Setenv("ASA_CFG", "")
	t.Setenv("ASA_BASEDIR", "")
	orig := validateBaseDir
	validateBaseDir = func(string) error { return nil } // 测试机未必有 30GB
	t.Cleanup(func() { validateBaseDir = orig })
	e.reload(t)
	return e
}

func (e *configEnv) reload(t *testing.T) {
	t.Helper()
	if _, err := appconfig.Load(appconfig.WithoutAutoGenerate()); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, o configInitOptions, input string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	if o.Interactive {
		o.AskLang = true
	}
	res, err := runConfigInit(o, newPrompter(strings.NewReader(input), &out))
	if err == nil {
		printConfigInitSummary(&out, res, false)
	}
	return out.String(), err
}

func TestConfigInit_NonInteractiveWritesToExeDir(t *testing.T) {
	e := newConfigEnv(t)
	dataDir := filepath.Join(t.TempDir(), "data")

	out, err := run(t, configInitOptions{BaseDir: dataDir, Lang: "zh"}, "")
	if err != nil {
		t.Fatalf("config init: %v\n%s", err, out)
	}
	path := filepath.Join(e.exeDir, appconfig.ConfigFileName)
	cfg, err := appconfig.CheckFile(path)
	if err != nil {
		t.Fatalf("生成的文件应能通过校验: %v", err)
	}
	if cfg.BaseDir != dataDir {
		t.Errorf("basedir 应为 %q，实际 %q", dataDir, cfg.BaseDir)
	}
	if !strings.Contains(out, path) || !strings.Contains(out, "config validate") {
		t.Errorf("输出应包含文件路径与下一步提示:\n%s", out)
	}
}

func TestConfigInit_ExistingNeedsForce(t *testing.T) {
	e := newConfigEnv(t)
	path := filepath.Join(e.exeDir, appconfig.ConfigFileName)
	writeFile(t, path, "basedir: \"/keep\"\n")
	e.reload(t)

	if _, err := run(t, configInitOptions{Lang: "zh"}, ""); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("已存在且无 --force 应报错并提示 --force，实际 %v", err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "basedir: \"/keep\"\n" {
		t.Error("拒绝时不应改动原文件")
	}

	if _, err := run(t, configInitOptions{Lang: "zh", Force: true}, ""); err != nil {
		t.Fatalf("--force: %v", err)
	}
	if baks, _ := filepath.Glob(path + ".bak-*"); len(baks) != 1 {
		t.Errorf("--force 应留下一份备份，实际 %v", baks)
	}
}

func TestConfigInit_InteractiveConfirmsOverwrite(t *testing.T) {
	e := newConfigEnv(t)
	path := filepath.Join(e.exeDir, appconfig.ConfigFileName)
	writeFile(t, path, "basedir: \"/keep\"\n")
	e.reload(t)

	// 拒绝覆盖。
	if _, err := run(t, configInitOptions{Lang: "zh", Interactive: true}, "n\n"); err == nil {
		t.Fatal("拒绝覆盖应返回「已取消」")
	}
	// 同意覆盖，数据目录直接回车。
	if out, err := run(t, configInitOptions{Lang: "zh", Interactive: true}, "y\n\n"); err != nil {
		t.Fatalf("同意覆盖: %v\n%s", err, out)
	}
	if baks, _ := filepath.Glob(path + ".bak-*"); len(baks) != 1 {
		t.Errorf("覆盖前应备份，实际 %v", baks)
	}
}

// exe 同级新建会遮蔽系统目录里正在用的那份：非交互必须 --force。
func TestConfigInit_ShadowingRequiresForce(t *testing.T) {
	e := newConfigEnv(t)
	writeFile(t, filepath.Join(e.sysDir, appconfig.ConfigFileName), "server:\n  port: 19193\n")
	e.reload(t)

	_, err := run(t, configInitOptions{Lang: "zh"}, "")
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("遮蔽已有配置应要求 --force，实际 %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.exeDir, appconfig.ConfigFileName)); err == nil {
		t.Error("被拒绝时不应写出文件")
	}

	out, err := run(t, configInitOptions{Lang: "zh", Force: true}, "")
	if err != nil {
		t.Fatalf("--force: %v", err)
	}
	if !strings.Contains(out, "遮蔽") {
		t.Errorf("应提示遮蔽:\n%s", out)
	}
}

// --dir 指向三级查找之外：照写，但要告诉用户下次读不到、该设 ASA_CFG。
func TestConfigInit_OutsideLookupWarnsAboutASACFG(t *testing.T) {
	newConfigEnv(t)
	custom := filepath.Join(t.TempDir(), "custom")

	out, err := run(t, configInitOptions{Dir: custom, Lang: "zh"}, "")
	if err != nil {
		t.Fatalf("config init --dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(custom, appconfig.ConfigFileName)); err != nil {
		t.Errorf("应写到 --dir 指定的目录: %v", err)
	}
	if !strings.Contains(out, "ASA_CFG="+custom) {
		t.Errorf("应提示设置 ASA_CFG=%s:\n%s", custom, out)
	}
}

// 双语语言提问：选 2 → 英文模板，且之后整段输出都是 ASCII（终端显示不了中文）。
func TestConfigInit_LanguagePromptChoosesEnglish(t *testing.T) {
	e := newConfigEnv(t)

	// 先给一个非法答案，再选 2；数据目录直接回车。
	out, err := run(t, configInitOptions{Interactive: true}, "x\n2\n\n")
	if err != nil {
		t.Fatalf("config init: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(e.exeDir, appconfig.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "application config") {
		t.Error("选 2 应生成英文模板")
	}

	// 语言提问本身是双语的；提问之后的输出必须纯 ASCII。
	after := out[strings.LastIndex(out, "Choose [1/2]"):]
	for i, r := range after {
		if r > 0x7F {
			t.Fatalf("选英文后的输出含非 ASCII 字符（第 %d 字节 %q）:\n%s", i, r, after)
		}
	}
}

func TestConfigInit_InvalidBaseDirNonInteractive(t *testing.T) {
	newConfigEnv(t)
	validateBaseDir = func(string) error { return errors.New("目录不可写：测试") }

	if _, err := run(t, configInitOptions{BaseDir: "/nope", Lang: "zh"}, ""); err == nil || !strings.Contains(err.Error(), "不可写") {
		t.Fatalf("数据目录校验失败应原样返回，实际 %v", err)
	}
}

func TestConfigInit_RejectsUnknownLang(t *testing.T) {
	newConfigEnv(t)
	if _, err := run(t, configInitOptions{Lang: "jp"}, ""); err == nil {
		t.Fatal("不支持的语言应报错")
	}
}

func TestConfigValidate(t *testing.T) {
	e := newConfigEnv(t)
	var out bytes.Buffer

	if err := runConfigValidate(&out, ""); err == nil || !strings.Contains(err.Error(), "config init") {
		t.Fatalf("没有配置文件时应提示 config init，实际 %v", err)
	}

	path := filepath.Join(e.exeDir, appconfig.ConfigFileName)
	writeFile(t, path, "server:\n  port: 8443\ndownload:\n  github_proxy: \"https://gh.example.com/\"\n")
	e.reload(t)
	out.Reset()
	if err := runConfigValidate(&out, ""); err != nil {
		t.Fatalf("合法配置: %v", err)
	}
	for _, want := range []string{"配置有效", "8443", e.exeDir, "gh.example.com"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("输出应包含 %q:\n%s", want, out.String())
		}
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	writeFile(t, bad, "server:\n  port: 70000\n")
	if err := runConfigValidate(&out, bad); err == nil || !strings.Contains(err.Error(), "配置无效") {
		t.Fatalf("--file 指向非法配置应报错，实际 %v", err)
	}
}

func TestConfigPath(t *testing.T) {
	e := newConfigEnv(t)
	var out bytes.Buffer
	if err := runConfigPath(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "不存在") {
		t.Errorf("没有配置时应显示不存在:\n%s", out.String())
	}

	writeFile(t, filepath.Join(e.sysDir, appconfig.ConfigFileName), "basedir: \"/srv/asa\"\n")
	e.reload(t)
	out.Reset()
	if err := runConfigPath(&out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "当前使用") || !strings.Contains(s, "basedir 字段") {
		t.Errorf("应标出当前使用的层级与数据目录来源:\n%s", s)
	}
	// 「当前使用」标在系统目录那一行。
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "当前使用") && !strings.Contains(line, e.sysDir) {
			t.Errorf("「当前使用」应标在系统目录一行，实际：%s", line)
		}
	}
}

// --set-env：不再警告「下次读不到」，写成功后持久化 ASA_CFG 并同步到当前进程。
func TestConfigInit_SetEnvPersistsConfigDir(t *testing.T) {
	newConfigEnv(t)
	custom := filepath.Join(t.TempDir(), "custom")

	var gotName, gotValue string
	orig := setUserEnv
	setUserEnv = func(name, value string) error { gotName, gotValue = name, value; return nil }
	t.Cleanup(func() { setUserEnv = orig })

	out, err := run(t, configInitOptions{Dir: custom, Lang: "zh", SetEnv: true}, "")
	if err != nil {
		t.Fatalf("config init --set-env: %v\n%s", err, out)
	}
	if gotName != "ASA_CFG" || gotValue != custom {
		t.Errorf("应持久化 ASA_CFG=%q，实际 %s=%q", custom, gotName, gotValue)
	}
	if os.Getenv("ASA_CFG") != custom {
		t.Errorf("当前进程的 ASA_CFG 应同步为 %q，实际 %q", custom, os.Getenv("ASA_CFG"))
	}
	if strings.Contains(out, "不会读取") {
		t.Errorf("--set-env 时不应再警告读不到:\n%s", out)
	}
}

// 不支持持久化的平台：不报错，打印手动设置方法。
func TestConfigInit_SetEnvUnsupportedPrintsHint(t *testing.T) {
	newConfigEnv(t)
	custom := filepath.Join(t.TempDir(), "custom")
	orig := setUserEnv
	setUserEnv = func(string, string) error { return userenv.ErrUnsupported }
	t.Cleanup(func() { setUserEnv = orig })

	out, err := run(t, configInitOptions{Dir: custom, Lang: "zh", SetEnv: true}, "")
	if err != nil {
		t.Fatalf("config init --set-env: %v", err)
	}
	if !strings.Contains(out, "export ASA_CFG="+custom) {
		t.Errorf("应打印 export 提示:\n%s", out)
	}
}
