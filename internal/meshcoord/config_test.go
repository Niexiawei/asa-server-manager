package meshcoord

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, ConfigFileName)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const minimalConfig = `
listen: ":443"
public_addr: "coord.example.com:443"
`

func TestTemplateLoadsAndValidates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ConfigFileName)
	if _, err := InitConfig(p, false); err != nil {
		t.Fatal(err)
	}
	cfg, warnings, err := Load(p)
	if err != nil {
		t.Fatalf("模板应能原样加载并通过校验：%v", err)
	}
	if !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "示例地址") }) {
		t.Fatalf("模板里的示例 public_addr 应给出警告，得到 %v", warnings)
	}
	if !slices.Equal(cfg.STUN.Listen, []string{":3478", ":3479"}) {
		t.Fatalf("模板应默认开 STUN，得到 %v", cfg.STUN.Listen)
	}
	if !slices.Equal(cfg.STUNAdvertise(), []string{"203.0.113.10:3478", "203.0.113.10:3479"}) {
		t.Fatalf("STUNAdvertise = %v", cfg.STUNAdvertise())
	}
	raw, _ := os.ReadFile(p)
	if strings.HasPrefix(string(raw), string([]byte{0xEF, 0xBB, 0xBF})) || strings.Contains(string(raw), "\r\n") {
		t.Fatal("模板应是无 BOM、LF 换行")
	}
}

func TestInitConfigRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "listen: \":1\"\n")
	if _, err := InitConfig(p, false); !errors.Is(err, ErrConfigExists) {
		t.Fatalf("已存在且没有 --force 应拒绝，得到 %v", err)
	}
	if raw, _ := os.ReadFile(p); string(raw) != "listen: \":1\"\n" {
		t.Fatal("拒绝覆盖时不该动原文件")
	}
	backup, err := InitConfig(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(backup); string(raw) != "listen: \":1\"\n" {
		t.Fatalf("--force 应先把旧文件备份到 %s", backup)
	}
	if raw, _ := os.ReadFile(p); !strings.Contains(string(raw), "public_addr") {
		t.Fatal("--force 后应是新模板")
	}
}

func TestRelativePathsResolveAgainstConfigDir(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, minimalConfig+`
data_dir: "state"
tls:
  cert_file: "certs/c.pem"
  key_file: "certs/k.pem"
`)
	// 换一个工作目录，确认结果与 cwd 无关。
	t.Chdir(t.TempDir())
	cfg, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != filepath.Join(dir, "state") {
		t.Fatalf("data_dir = %s，应相对配置文件目录解析", cfg.DataDir)
	}
	if cfg.TLS.CertFile != filepath.Join(dir, "certs", "c.pem") || cfg.TLS.KeyFile != filepath.Join(dir, "certs", "k.pem") {
		t.Fatalf("证书路径应相对配置文件目录解析：%s / %s", cfg.TLS.CertFile, cfg.TLS.KeyFile)
	}
	if cfg.Path() != p {
		t.Fatalf("Path() = %s", cfg.Path())
	}

	// 默认 data_dir 也在配置文件旁边。
	p2 := writeConfig(t, t.TempDir(), minimalConfig)
	cfg2, _, err := Load(p2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.DataDir != filepath.Join(filepath.Dir(p2), "data") {
		t.Fatalf("默认 data_dir = %s", cfg2.DataDir)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	p := writeConfig(t, t.TempDir(), minimalConfig+`
stun:
  listn: [":3478"]
`)
	if _, _, err := Load(p); err == nil || !strings.Contains(err.Error(), "listn") {
		t.Fatalf("拼错的字段应报错并点名，得到 %v", err)
	}
}

func TestLocate(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.yaml")
	_, err := Locate(missing)
	var nf *ErrConfigNotFound
	if !errors.As(err, &nf) || !slices.Equal(nf.Searched, []string{missing}) {
		t.Fatalf("显式 -c 指向不存在的文件时只报它一个，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "config init") {
		t.Fatal("找不到配置时应提示 config init")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("找不到配置时不该生成文件")
	}

	p := writeConfig(t, dir, minimalConfig)
	got, err := Locate(p)
	if err != nil || got != p {
		t.Fatalf("Locate(%s) = %s, %v", p, got, err)
	}

	// 不给 -c 时的查找顺序：程序所在目录优先于系统目录。
	paths := SearchPaths("")
	if len(paths) == 0 || filepath.Base(paths[0]) != ConfigFileName {
		t.Fatalf("SearchPaths = %v", paths)
	}
	exe, _ := os.Executable()
	if paths[0] != filepath.Join(filepath.Dir(exe), ConfigFileName) {
		t.Fatalf("第一个查找位置应是程序所在目录，得到 %s", paths[0])
	}
	// 给了 -c 就只看它。
	if got := SearchPaths(p); !slices.Equal(got, []string{p}) {
		t.Fatalf("SearchPaths(-c) = %v", got)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		extra   string
		wantErr string
		warn    string
	}{
		{name: "三个 STUN 端口", extra: "stun:\n  listen: [\":1\", \":2\", \":3\"]\n", wantErr: "最多 2 个"},
		{name: "端口重复", extra: "stun:\n  listen: [\":3478\", \"0.0.0.0:3478\"]\n", wantErr: "不能相同"},
		{name: "没有端口", extra: "stun:\n  listen: [\"3478\"]\n", wantErr: "host:port"},
		{name: "一个 STUN 端口", extra: "stun:\n  listen: [\":3478\"]\n", warn: "NAT4"},
		{name: "证书只给一半", extra: "tls:\n  cert_file: a.pem\n", wantErr: "同时给出"},
		{name: "advertise 格式", extra: "stun:\n  listen: [\":3478\", \":3479\"]\n  advertise: [\"nope\"]\n", wantErr: "advertise"},
	}
	for _, c := range cases {
		p := writeConfig(t, t.TempDir(), minimalConfig+c.extra)
		_, warnings, err := Load(p)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: 应报含 %q 的错误，得到 %v", c.name, c.wantErr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.warn != "" && !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, c.warn) }) {
			t.Errorf("%s: 应警告 %q，得到 %v", c.name, c.warn, warnings)
		}
	}

	for _, missing := range []string{"listen: \":443\"\n", "public_addr: \"x:1\"\n"} {
		p := writeConfig(t, t.TempDir(), missing)
		if _, _, err := Load(p); err == nil {
			t.Errorf("缺必填字段应报错：%q", missing)
		}
	}
}

func TestSTUNAdvertise(t *testing.T) {
	cfg := Config{PublicAddr: "coord.example.com:443"}
	if got := cfg.STUNAdvertise(); got != nil {
		t.Fatalf("未开 STUN 时应为空，得到 %v", got)
	}
	cfg.STUN.Listen = []string{":3478", "0.0.0.0:3479"}
	if got := cfg.STUNAdvertise(); !slices.Equal(got, []string{"coord.example.com:3478", "coord.example.com:3479"}) {
		t.Fatalf("应由 public_addr 主机名 + listen 端口推导，得到 %v", got)
	}
	cfg.PublicAddr = "[2001:db8::1]:443"
	if got := cfg.STUNAdvertise(); got[0] != "[2001:db8::1]:3478" {
		t.Fatalf("IPv6 主机名应带方括号，得到 %v", got)
	}
	cfg.STUN.Advertise = []string{"stun.example.com:3478"}
	if got := cfg.STUNAdvertise(); !slices.Equal(got, []string{"stun.example.com:3478"}) {
		t.Fatalf("显式 advertise 优先，得到 %v", got)
	}
}
