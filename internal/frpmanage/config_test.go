package frpmanage

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	v1 "github.com/fatedier/frp/pkg/config/v1"
)

func TestValidate(t *testing.T) {
	base := func(rules ...PortRule) *Config {
		return &Config{ServerAddr: "1.2.3.4", Token: "tok", Rules: rules}
	}

	cases := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{"最小可用配置", base(PortRule{Start: 9310, End: 9319, Protocol: ProtocolUDP}), false},
		{"单端口", base(PortRule{Start: 9310, End: 9310, Protocol: ProtocolBoth}), false},
		{"地址为空", &Config{Rules: []PortRule{{Start: 1, End: 1, Protocol: ProtocolTCP}}}, true},
		{"没有规则", base(), true},
		{"起止颠倒", base(PortRule{Start: 9319, End: 9310, Protocol: ProtocolUDP}), true},
		{"端口越界", base(PortRule{Start: 0, End: 10, Protocol: ProtocolTCP}), true},
		{"端口越界上", base(PortRule{Start: 65530, End: 65536, Protocol: ProtocolTCP}), true},
		{"协议非法", base(PortRule{Start: 1, End: 1, Protocol: Protocol("sctp")}), true},
		{"单条规则超上限", base(PortRule{Start: 1000, End: 1000 + maxPortsPerRule, Protocol: ProtocolTCP}), true},
		{
			"同协议区间重叠",
			base(
				PortRule{Start: 9310, End: 9319, Protocol: ProtocolUDP},
				PortRule{Start: 9315, End: 9316, Protocol: ProtocolUDP},
			),
			true,
		},
		{
			// tcp+udp 与 tcp 的交叉重叠：展开后 tcp-asaserver-9310 会出现两次，
			// 是最容易被漏掉的那种重叠
			"tcp+udp 与 tcp 交叉重叠",
			base(
				PortRule{Start: 9310, End: 9310, Protocol: ProtocolBoth},
				PortRule{Start: 9310, End: 9310, Protocol: ProtocolTCP},
			),
			true,
		},
		{
			// 同端口但协议不同，不算重叠
			"同端口不同协议合法",
			base(
				PortRule{Start: 9310, End: 9310, Protocol: ProtocolTCP},
				PortRule{Start: 9310, End: 9310, Protocol: ProtocolUDP},
			),
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.cfg.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestValidateProxyTotalLimit(t *testing.T) {
	// 每条 64 个端口 × tcp+udp = 128 条，正好压线；再加一条就超。
	cfg := &Config{
		ServerAddr: "1.2.3.4",
		Rules: []PortRule{
			{Start: 1000, End: 1063, Protocol: ProtocolBoth},
		},
	}
	if _, err := cfg.Validate(); err != nil {
		t.Fatalf("128 条应当合法，却报错: %v", err)
	}
	if got := cfg.proxyCount(); got != maxProxies {
		t.Fatalf("proxyCount = %d, want %d", got, maxProxies)
	}

	cfg.Rules = append(cfg.Rules, PortRule{Start: 2000, End: 2000, Protocol: ProtocolTCP})
	if _, err := cfg.Validate(); err == nil {
		t.Error("超过总上限时应当报错")
	}
}

func TestValidateEmptyTokenWarns(t *testing.T) {
	cfg := &Config{ServerAddr: "1.2.3.4", Rules: []PortRule{{Start: 1, End: 1, Protocol: ProtocolTCP}}}
	warnings, err := cfg.Validate()
	if err != nil {
		t.Fatalf("空 token 不该阻塞保存: %v", err)
	}
	if len(warnings) == 0 {
		t.Error("空 token 应当返回一条提示")
	}
}

func TestAddrParsing(t *testing.T) {
	cases := []struct {
		addr     string
		port     int
		wantHost string
		wantPort int
	}{
		{"1.2.3.4", 0, "1.2.3.4", defaultServerPort},
		{"1.2.3.4", 7001, "1.2.3.4", 7001},
		{"1.2.3.4:7002", 0, "1.2.3.4", 7002},
		{"frp.example.com:7003", 0, "frp.example.com", 7003},
		{"[::1]:7004", 0, "::1", 7004},
		// 裸 IPv6：SplitHostPort 会报 too many colons，整串就是 host
		{"::1", 0, "::1", defaultServerPort},
	}
	for _, c := range cases {
		cfg := &Config{ServerAddr: c.addr, ServerPort: c.port}
		if got := cfg.host(); got != c.wantHost {
			t.Errorf("host(%q) = %q, want %q", c.addr, got, c.wantHost)
		}
		if got := cfg.port(); got != c.wantPort {
			t.Errorf("port(%q,%d) = %d, want %d", c.addr, c.port, got, c.wantPort)
		}
	}
}

func TestAddrInlinePortConflict(t *testing.T) {
	cfg := &Config{
		ServerAddr: "1.2.3.4:7001",
		ServerPort: 7000,
		Rules:      []PortRule{{Start: 1, End: 1, Protocol: ProtocolTCP}},
	}
	if _, err := cfg.Validate(); err == nil {
		t.Error("地址内端口与端口字段冲突时应当报错")
	}

	// 相同则不算冲突
	cfg.ServerPort = 7001
	if _, err := cfg.Validate(); err != nil {
		t.Errorf("端口一致时不该报错: %v", err)
	}
}

// TestBuildExpandsProxies 是走 config.LoadConfigure 那条路的核心断言：
// 参数 → frp 配置对象的展开必须逐条正确，否则隧道会静默地转发到错误的端口。
func TestBuildExpandsProxies(t *testing.T) {
	cfg := &Config{
		ServerAddr: "47.97.22.91",
		Token:      "tok",
		Rules:      []PortRule{{Start: 9310, End: 9311, Protocol: ProtocolBoth}},
	}

	common, proxies, err := cfg.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if common.ServerAddr != "47.97.22.91" {
		t.Errorf("ServerAddr = %q", common.ServerAddr)
	}
	if common.ServerPort != defaultServerPort {
		t.Errorf("ServerPort = %d, want %d", common.ServerPort, defaultServerPort)
	}
	if common.Auth.Token != "tok" {
		t.Errorf("Auth.Token = %q", common.Auth.Token)
	}
	// Complete() 必须被调过：这几个默认值只有它会填
	if common.Auth.Method != "token" {
		t.Errorf("Auth.Method = %q, want token（Complete 没被调用？）", common.Auth.Method)
	}
	if common.UDPPacketSize == 0 {
		t.Error("UDPPacketSize 为 0，Complete 没被调用")
	}

	if len(proxies) != 4 {
		t.Fatalf("展开出 %d 条代理，want 4", len(proxies))
	}

	type got struct {
		name   string
		typ    string
		local  int
		remote int
	}
	var actual []got
	for _, p := range proxies {
		base := p.GetBaseConfig()
		g := got{name: base.Name, typ: base.Type, local: base.LocalPort}
		switch typed := p.(type) {
		case *v1.TCPProxyConfig:
			g.remote = typed.RemotePort
		case *v1.UDPProxyConfig:
			g.remote = typed.RemotePort
		default:
			t.Fatalf("代理 %s 的类型不是 TCP/UDP: %T", base.Name, p)
		}
		actual = append(actual, g)
	}

	want := []got{
		{"tcp-asaserver-9310", "tcp", 9310, 9310},
		{"udp-asaserver-9310", "udp", 9310, 9310},
		{"tcp-asaserver-9311", "tcp", 9311, 9311},
		{"udp-asaserver-9311", "udp", 9311, 9311},
	}
	if !reflect.DeepEqual(actual, want) {
		t.Errorf("展开结果不符\n got: %+v\nwant: %+v", actual, want)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &Config{
		ServerAddr: "47.97.22.91",
		ServerPort: 7000,
		Token:      "tok",
		Rules: []PortRule{
			{Start: 9310, End: 9319, Protocol: ProtocolUDP, Remark: "游戏端口"},
			{Start: 27020, End: 27020, Protocol: ProtocolTCP, Remark: "RCON"},
		},
	}
	if err := SaveConfig(dir, want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip 不一致\n got: %+v\nwant: %+v", got, want)
	}
}

func TestLoadConfigMissingIsNotConfigured(t *testing.T) {
	if _, err := LoadConfig(t.TempDir()); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("缺文件时应当返回 ErrNotConfigured，got %v", err)
	}
}

func TestNormalizeTrimsAndLowercases(t *testing.T) {
	cfg := &Config{
		ServerAddr: "  1.2.3.4 ",
		Token:      " tok ",
		Rules:      []PortRule{{Start: 1, End: 1, Protocol: Protocol(" UDP "), Remark: " 备注 "}},
	}
	cfg.Normalize()
	if cfg.ServerAddr != "1.2.3.4" || cfg.Token != "tok" {
		t.Errorf("首尾空白没去干净: %+v", cfg)
	}
	if cfg.Rules[0].Protocol != ProtocolUDP {
		t.Errorf("协议没归一化: %q", cfg.Rules[0].Protocol)
	}
	if cfg.Rules[0].Remark != "备注" {
		t.Errorf("备注没去空白: %q", cfg.Rules[0].Remark)
	}
}

func TestCommonChanged(t *testing.T) {
	a := &Config{ServerAddr: "1.2.3.4", ServerPort: 7000, Token: "t",
		Rules: []PortRule{{Start: 1, End: 1, Protocol: ProtocolTCP}}}

	// 只改端口规则 → 可热更新
	b := *a
	b.Rules = []PortRule{{Start: 1, End: 5, Protocol: ProtocolTCP}}
	if b.commonChanged(a) {
		t.Error("只改端口规则时不该判定为需要重启")
	}

	// 改 token / 地址 / 端口 → 必须重启
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Token = "other" },
		func(c *Config) { c.ServerAddr = "5.6.7.8" },
		func(c *Config) { c.ServerPort = 7001 },
	} {
		c := *a
		mutate(&c)
		if !c.commonChanged(a) {
			t.Errorf("连接参数变化时应当判定为需要重启: %+v", c)
		}
	}

	if !a.commonChanged(nil) {
		t.Error("此前没有配置时应当判定为需要重启")
	}
}

// legacyTOML 就是现网 E:\asa_server_data\frp\frpc.toml 的形状：
// 用 Go 模板的 parseNumberRangePair 展开端口范围。只有 frp 自己的加载器认得它 ——
// 这正是迁移不能手写 TOML 解析器的原因。
const legacyTOML = `# FRP Client Configuration
serverAddr = "47.97.22.91"
auth.token = "9d4d40ad-0ee5-4414-b38d-4e9e787830a1"

{{- range $_, $v := parseNumberRangePair "9310-9319" "9310-9319" }}
[[proxies]]
name = "udp-asaserver-{{ $v.First }}"
type = "udp"
localPort = {{ $v.First }}
remotePort = {{ $v.Second }}
{{- end }}
`

func TestMigrateFromLegacyTOML(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, legacyTOMLName)
	if err := os.WriteFile(tomlPath, []byte(legacyTOML), 0644); err != nil {
		t.Fatalf("写老配置: %v", err)
	}

	migrated, dropped, err := migrateFromTOML(dir)
	if err != nil {
		t.Fatalf("migrateFromTOML: %v", err)
	}
	if !migrated {
		t.Fatal("应当发生迁移")
	}
	if len(dropped) != 0 {
		t.Errorf("不该有被丢弃的代理: %v", dropped)
	}

	got, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := &Config{
		ServerAddr: "47.97.22.91",
		ServerPort: defaultServerPort,
		Token:      "9d4d40ad-0ee5-4414-b38d-4e9e787830a1",
		Rules:      []PortRule{{Start: 9310, End: 9319, Protocol: ProtocolUDP}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("迁移结果不符\n got: %+v\nwant: %+v", got, want)
	}

	// 老文件必须被改名保留，否则下次启动会重复迁移
	if _, err := os.Stat(tomlPath); !os.IsNotExist(err) {
		t.Error("老的 frpc.toml 应当已被改名")
	}
	if _, err := os.Stat(tomlPath + ".migrated"); err != nil {
		t.Errorf("备份文件应当存在: %v", err)
	}

	// 再跑一次不应重复迁移
	migrated, _, err = migrateFromTOML(dir)
	if err != nil || migrated {
		t.Errorf("已有 frpc.json 时不该再迁移: migrated=%v err=%v", migrated, err)
	}
}

func TestMigrateDropsUnrepresentableProxies(t *testing.T) {
	dir := t.TempDir()
	const mixed = `serverAddr = "1.2.3.4"
auth.token = "tok"

[[proxies]]
name = "tcp-asaserver-9310"
type = "tcp"
localPort = 9310
remotePort = 9310

[[proxies]]
name = "shifted"
type = "tcp"
localPort = 8080
remotePort = 9999

[[proxies]]
name = "web"
type = "http"
localPort = 80
customDomains = ["a.example.com"]
`
	if err := os.WriteFile(filepath.Join(dir, legacyTOMLName), []byte(mixed), 0644); err != nil {
		t.Fatalf("写老配置: %v", err)
	}

	migrated, dropped, err := migrateFromTOML(dir)
	if err != nil {
		t.Fatalf("migrateFromTOML: %v", err)
	}
	if !migrated {
		t.Fatal("应当发生迁移")
	}
	// 无法表达的两条必须被点名，不能静默丢弃
	if len(dropped) != 2 {
		t.Fatalf("应当丢弃 2 条代理，实际 %v", dropped)
	}

	got, _ := LoadConfig(dir)
	want := []PortRule{{Start: 9310, End: 9310, Protocol: ProtocolTCP}}
	if !reflect.DeepEqual(got.Rules, want) {
		t.Errorf("规则不符\n got: %+v\nwant: %+v", got.Rules, want)
	}
}

func TestRulesFromPortsCompressesRanges(t *testing.T) {
	got := rulesFromPorts(map[string][]int{
		"tcp": {27020, 9310, 9311},      // 9310-9311 与 9310-9311 的 udp 重合 → tcp+udp
		"udp": {9310, 9311, 9315, 9316}, // 9315-9316 只有 udp
	})
	want := []PortRule{
		{Start: 27020, End: 27020, Protocol: ProtocolTCP},
		{Start: 9315, End: 9316, Protocol: ProtocolUDP},
		{Start: 9310, End: 9311, Protocol: ProtocolBoth},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("压缩结果不符\n got: %+v\nwant: %+v", got, want)
	}
}
