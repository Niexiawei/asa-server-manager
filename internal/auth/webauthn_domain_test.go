package auth

import (
	"slices"
	"testing"
)

// 域名闸门是整个 WebAuthn 设计里安全最关键的一环：
// RP ID 来自 Host 头，而 Host 头是客户端可控的。
func TestMatchDomain(t *testing.T) {
	domains := []string{"localhost", "ark.example.com"}

	cases := []struct {
		name     string
		host     string
		domains  []string
		wantRPID string
		wantOK   bool
	}{
		{"精确匹配", "ark.example.com", domains, "ark.example.com", true},
		{"带端口", "ark.example.com:19193", domains, "ark.example.com", true},
		{"localhost 带端口", "localhost:19193", domains, "localhost", true},
		{"大小写不敏感", "ARK.Example.COM", domains, "ark.example.com", true},
		{"FQDN 尾点", "ark.example.com.", domains, "ark.example.com", true},

		// IP 地址不是合法 RP ID，这是规范层面的限制，无法绕过。
		// 也就是说用 https://192.168.x.x 访问时 WebAuthn 一定不可用。
		{"IPv4", "192.168.1.10", domains, "", false},
		{"IPv4 带端口", "192.168.1.10:19193", domains, "", false},
		{"IPv6", "[::1]", domains, "", false},
		{"IPv6 带端口", "[::1]:19193", domains, "", false},

		{"不在白名单", "evil.com", domains, "", false},
		{"空 domains 一律不通过", "ark.example.com", nil, "", false},
		{"空 Host", "", domains, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rpID, ok := MatchDomain(c.host, c.domains)
			if ok != c.wantOK {
				t.Fatalf("MatchDomain(%q) ok = %v，期望 %v", c.host, ok, c.wantOK)
			}
			if rpID != c.wantRPID {
				t.Errorf("MatchDomain(%q) rpID = %q，期望 %q", c.host, rpID, c.wantRPID)
			}
		})
	}
}

// 配父域名可让所有子域名共享同一套凭证，这是规范允许的
// 「RP ID 必须是 Origin 有效域名的可注册后缀」。
func TestMatchDomainParentSuffix(t *testing.T) {
	domains := []string{"example.com"}

	rpID, ok := MatchDomain("ark.example.com", domains)
	if !ok || rpID != "example.com" {
		t.Errorf("子域名应命中父域名并返回父域名作为 RP ID，实际 (%q, %v)", rpID, ok)
	}
	rpID, ok = MatchDomain("a.b.example.com", domains)
	if !ok || rpID != "example.com" {
		t.Errorf("多级子域名也应命中，实际 (%q, %v)", rpID, ok)
	}

	// ★ 易错点：后缀判断必须带上那个点。
	// 不带的话 notexample.com 会被 example.com 误命中，
	// 等于把凭证签发给了一个完全不相干的域名。
	if _, ok := MatchDomain("notexample.com", domains); ok {
		t.Error("notexample.com 不得被 example.com 命中（后缀判断必须带 .）")
	}
	if _, ok := MatchDomain("evilexample.com", domains); ok {
		t.Error("evilexample.com 不得被 example.com 命中")
	}
	// 父域名自身当然要命中
	if rpID, ok := MatchDomain("example.com", domains); !ok || rpID != "example.com" {
		t.Errorf("父域名自身应命中，实际 (%q, %v)", rpID, ok)
	}
}

// 同时配了父域名和子域名时，精确匹配应该胜出（更具体的配置优先）
func TestMatchDomainExactWinsOverParent(t *testing.T) {
	domains := []string{"example.com", "ark.example.com"}

	rpID, ok := MatchDomain("ark.example.com", domains)
	if !ok || rpID != "ark.example.com" {
		t.Errorf("精确匹配应优先于父域名匹配，实际 (%q, %v)", rpID, ok)
	}
	// 没被精确列出的子域名才回落到父域名
	rpID, ok = MatchDomain("other.example.com", domains)
	if !ok || rpID != "example.com" {
		t.Errorf("未精确列出的子域名应回落到父域名，实际 (%q, %v)", rpID, ok)
	}
}

// 配了多个父域名时取最长的那个，行为才可预测
func TestMatchDomainLongestParentWins(t *testing.T) {
	domains := []string{"com", "example.com"}
	rpID, ok := MatchDomain("ark.example.com", domains)
	if !ok || rpID != "example.com" {
		t.Errorf("应取最长的父域名，实际 (%q, %v)", rpID, ok)
	}
}

func TestOriginsFor(t *testing.T) {
	t.Run("普通端口", func(t *testing.T) {
		got := OriginsFor("localhost", []string{"localhost"}, 19193, true, nil)
		for _, want := range []string{"https://localhost", "https://localhost:19193"} {
			if !slices.Contains(got, want) {
				t.Errorf("缺少 Origin %q，实际 %v", want, got)
			}
		}
	})

	t.Run("443 不重复列端口", func(t *testing.T) {
		got := OriginsFor("ark.example.com", []string{"ark.example.com"}, 443, true, nil)
		if len(got) != 1 || got[0] != "https://ark.example.com" {
			t.Errorf("443 端口不该额外列出带端口形式，实际 %v", got)
		}
	})

	// 父域名场景最容易出错：RP ID 是 example.com，但浏览器发来的
	// Origin 是 https://ark.example.com。Origin 校验是精确匹配的，
	// 不把实际访问的子域名列进去就会一直校验失败。
	t.Run("父域名场景必须包含子域名 Origin", func(t *testing.T) {
		got := OriginsFor("example.com",
			[]string{"example.com", "ark.example.com"}, 443, true, nil)
		if !slices.Contains(got, "https://ark.example.com") {
			t.Errorf("父域名作 RP ID 时，子域名也必须是合法 Origin，实际 %v", got)
		}
		if !slices.Contains(got, "https://example.com") {
			t.Errorf("父域名自身也应在列，实际 %v", got)
		}
	})

	t.Run("关闭 TLS 时只有 localhost 可用", func(t *testing.T) {
		got := OriginsFor("localhost", []string{"localhost"}, 19193, false, nil)
		if !slices.Contains(got, "http://localhost:19193") {
			t.Errorf("localhost 在明文 HTTP 下是安全上下文特例，应可用，实际 %v", got)
		}

		// 其它主机名在明文 HTTP 下不是安全上下文，不该给出任何 Origin
		got = OriginsFor("ark.example.com", []string{"ark.example.com"}, 19193, false, nil)
		if len(got) != 0 {
			t.Errorf("非 localhost 主机在明文 HTTP 下不该有合法 Origin，实际 %v", got)
		}
	})

	t.Run("额外 Origin 会被附加", func(t *testing.T) {
		got := OriginsFor("localhost", []string{"localhost"}, 19193, true,
			[]string{"http://localhost:3000"})
		if !slices.Contains(got, "http://localhost:3000") {
			t.Errorf("extra_origins 应被附加，实际 %v", got)
		}
	})
}

func TestNewWebAuthnHandle(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		h, err := NewWebAuthnHandle()
		if err != nil {
			t.Fatalf("NewWebAuthnHandle: %v", err)
		}
		// 规范建议用满 64 字节以内的随机值；32 字节足够且不含任何 PII
		if len(h) != 32 {
			t.Errorf("handle 应为 32 字节，实际 %d", len(h))
		}
		if seen[string(h)] {
			t.Fatal("生成了重复的 handle —— discoverable 登录会因此串号")
		}
		seen[string(h)] = true
	}
}

// 很多认证器（尤其 iCloud / Google 同步的 passkey）按设计恒返回 0。
// 把 0/0 当成克隆异常会让这些用户直接登不上去。
func TestShouldTreatAsClone(t *testing.T) {
	if ShouldTreatAsClone(0, 0, true) {
		t.Error("计数恒为 0 表示该认证器不支持这个特性，不该当成克隆")
	}
	if !ShouldTreatAsClone(5, 5, true) {
		t.Error("支持计数的认证器出现计数未前进时应告警")
	}
	if ShouldTreatAsClone(5, 6, false) {
		t.Error("计数正常前进时不该告警")
	}
}
