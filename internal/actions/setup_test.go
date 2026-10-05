package actions

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asa-server/internal/appconfig"
	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/download"
	"asa-server/pkg/logger"
)

// setupEnv 在 newConfigEnv 之上额外保护 setup 会改动的进程级状态：
// cfgpkg 的目录变量、文件日志（Reload 会切到新目录，Windows 上开着的句柄会让
// TempDir 删不掉）。
func setupEnv(t *testing.T) *configEnv {
	t.Helper()
	e := newConfigEnv(t)
	// 五个目录变量原样还原。以前是 SetDirectories(origBase)：origBase 为空时它会把
	// InstancesDir 等设成相对路径 "instances"，而不是还原成空串。
	cfgpkg.UseTempDirsForTest(t)
	t.Cleanup(func() { _ = logger.Close() })
	return e
}

func runSetupResolve(t *testing.T, flagBaseDir string, nonInteractive, tty bool, input string) (string, bool, string, error) {
	t.Helper()
	var out bytes.Buffer
	baseDir, created, err := resolveSetupBaseDir(flagBaseDir, nonInteractive, newPrompter(strings.NewReader(input), &out), tty)
	return baseDir, created, out.String(), err
}

func TestSetupResolve_ExistingConfigIsReused(t *testing.T) {
	e := setupEnv(t)
	writeFile(t, filepath.Join(e.exeDir, appconfig.ConfigFileName), "server:\n  port: 19193\n")
	e.reload(t)
	cfgpkg.SetDirectories(e.exeDir)

	baseDir, created, out, err := runSetupResolve(t, "/ignored", false, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if created || baseDir != e.exeDir {
		t.Errorf("已有配置应沿用：baseDir=%q created=%v", baseDir, created)
	}
	if !strings.Contains(out, "检测到已有配置") {
		t.Errorf("应提示沿用已有配置:\n%s", out)
	}
}

func TestSetupResolve_NonInteractiveNeedsBaseDirOrConfigInit(t *testing.T) {
	setupEnv(t)
	_, _, _, err := runSetupResolve(t, "", true, false, "")
	if err == nil || !strings.Contains(err.Error(), "config init") {
		t.Fatalf("应指向 config init，实际 %v", err)
	}
}

// 非交互 + --basedir：生成配置、不停顿、Reload 后数据目录与子目录就位。
func TestSetupResolve_NonInteractiveGeneratesAndReloads(t *testing.T) {
	e := setupEnv(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	t.Cleanup(func() { _ = logger.Close() }) // dataDir 的 TempDir 清理之前先关日志

	baseDir, created, out, err := runSetupResolve(t, dataDir, true, false, "")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}
	if !created || baseDir != dataDir || cfgpkg.BaseDir != dataDir {
		t.Errorf("baseDir=%q created=%v cfgpkg.BaseDir=%q，期望都指向 %q", baseDir, created, cfgpkg.BaseDir, dataDir)
	}
	if _, err := os.Stat(filepath.Join(e.exeDir, appconfig.ConfigFileName)); err != nil {
		t.Errorf("应在 exe 同级生成配置: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "server-files")); err != nil {
		t.Errorf("Reload 后数据子目录应已创建: %v", err)
	}
	if strings.Contains(out, "按回车") {
		t.Errorf("非交互不应停顿:\n%s", out)
	}
}

// 交互 + 终端：问语言 → 问数据目录 → 停顿；用户在停顿期间改了配置，回车后必须生效
// ——下载代理就是 setup 之前没法改的那个典型字段。用 http_proxy 验证：它直接体现在
// download.Client() 的 Transport 上，能证明 Reload 真的重做了 download.Configure。
func TestSetupResolve_PauseAppliesUserEdits(t *testing.T) {
	e := setupEnv(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	t.Cleanup(func() { _ = logger.Close() })
	t.Cleanup(func() { download.Configure(download.Config{}) })

	cfgPath := filepath.Join(e.exeDir, appconfig.ConfigFileName)
	// 停顿处的「编辑」：prompter 读到回车之前，模拟用户改了 github_proxy。
	in := &editOnRead{
		lines: []string{"1", dataDir, ""},
		onLast: func() {
			raw, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			edited := strings.Replace(string(raw), `http_proxy: ""`, `http_proxy: "http://127.0.0.1:18080"`, 1)
			if err := os.WriteFile(cfgPath, []byte(edited), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}
	var out bytes.Buffer
	baseDir, created, err := resolveSetupBaseDir("", false, newPrompter(in, &out), true)
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out.String())
	}
	if !created || baseDir != dataDir {
		t.Errorf("baseDir=%q created=%v", baseDir, created)
	}
	if !strings.Contains(out.String(), "按回车继续") {
		t.Errorf("交互 + 终端应停顿等回车:\n%s", out.String())
	}
	if got := appconfig.Get().Download.HTTPProxy; got != "http://127.0.0.1:18080" {
		t.Errorf("停顿期间的修改应在 Reload 后生效，http_proxy=%q", got)
	}
	tr, ok := download.Client().Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		t.Fatal("下载器应已按新配置设置代理（以前 setup 里漏了 download.Configure）")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	if u, err := tr.Proxy(req); err != nil || u == nil || u.Host != "127.0.0.1:18080" {
		t.Errorf("下载器代理应为 127.0.0.1:18080，实际 %v, %v", u, err)
	}
}

// 管道喂数据目录的老用法：不问语言、不停顿，第一行就是数据目录。
func TestSetupResolve_PipedInputKeepsOldBehaviour(t *testing.T) {
	setupEnv(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	t.Cleanup(func() { _ = logger.Close() })

	baseDir, _, out, err := runSetupResolve(t, "", false, false, dataDir+"\n")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}
	if baseDir != dataDir {
		t.Errorf("管道第一行应作为数据目录，实际 %q", baseDir)
	}
	if strings.Contains(out, "Choose [1/2]") || strings.Contains(out, "按回车继续") {
		t.Errorf("非终端不应问语言或停顿:\n%s", out)
	}
}

// 停顿后配置改坏了：报错并允许修正后重试，输入 q 放弃。
func TestSetupResolve_BrokenEditCanBeAbandoned(t *testing.T) {
	e := setupEnv(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	t.Cleanup(func() { _ = logger.Close() })
	cfgPath := filepath.Join(e.exeDir, appconfig.ConfigFileName)

	in := &editOnRead{
		lines:   []string{"1", dataDir, "", "q"},
		onIndex: 2,
		onLast: func() {
			if err := os.WriteFile(cfgPath, []byte("server:\n  port: 70000\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}
	var out bytes.Buffer
	_, _, err := resolveSetupBaseDir("", false, newPrompter(in, &out), true)
	if err == nil || !strings.Contains(err.Error(), "已放弃") {
		t.Fatalf("输入 q 应放弃，实际 %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "70000") {
		t.Errorf("应把校验错误展示给用户:\n%s", out.String())
	}
}

// editOnRead 按行喂输入；在交出第 onIndex 行（默认最后一行）之前调用 onLast，
// 模拟用户在那个提示处去编辑了文件。
type editOnRead struct {
	lines   []string
	onIndex int
	onLast  func()
	next    int
	buf     []byte
}

func (r *editOnRead) Read(p []byte) (int, error) {
	if len(r.buf) == 0 {
		if r.next >= len(r.lines) {
			return 0, os.ErrClosed
		}
		idx := r.onIndex
		if idx == 0 {
			idx = len(r.lines) - 1
		}
		if r.next == idx && r.onLast != nil {
			r.onLast()
		}
		r.buf = []byte(r.lines[r.next] + "\n")
		r.next++
	}
	n := copy(p, r.buf[:1]) // 一次一个字节：不让 bufio 预读到还没到时候的行
	r.buf = r.buf[n:]
	return n, nil
}
