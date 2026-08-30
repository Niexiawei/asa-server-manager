package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeToolManifest 造一个 GE-Proton 目录，内容照抄真实 toolmanifest.vdf 的形状。
func writeToolManifest(t *testing.T, appID string) string {
	t.Helper()
	dir := t.TempDir()
	body := `"manifest"
{
  "version" "2"
  "commandline" "/proton %verb%"
  "require_tool_appid" "` + appID + `"
}
`
	if err := os.WriteFile(filepath.Join(dir, "toolmanifest.vdf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSteamrtForProtonReadsToolManifest(t *testing.T) {
	// manifest 是权威来源：即便版本名说的是另一代，也以 require_tool_appid 为准。
	tests := []struct {
		name          string
		appID         string
		protonVersion string
		wantVariant   string
		wantArchive   string
	}{
		{"sniper", steamrtAppIDSniper, "GE-Proton10-34", "steamrt3", "SteamLinuxRuntime_sniper.tar.xz"},
		{"steamrt4", steamrtAppIDSteamrt4, "GE-Proton11-1", "steamrt4", "SteamLinuxRuntime_4.tar.xz"},
		{"manifest wins over version name", steamrtAppIDSteamrt4, "GE-Proton10-34", "steamrt4", "SteamLinuxRuntime_4.tar.xz"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, ok := steamrtForProton(writeToolManifest(t, tc.appID), tc.protonVersion)
			if !ok {
				t.Fatalf("steamrtForProton(appid=%s) = not ok", tc.appID)
			}
			if v.Variant != tc.wantVariant {
				t.Errorf("Variant = %q, want %q", v.Variant, tc.wantVariant)
			}
			if v.Archive != tc.wantArchive {
				t.Errorf("Archive = %q, want %q", v.Archive, tc.wantArchive)
			}
		})
	}
}

// steamrt4 的归档名是 SteamLinuxRuntime_4.tar.xz，不是 _sniper —— 这是整套预取里
// 最容易想当然写错的一处，写错的结果是 404。单独钉一条。
func TestSteamrt4ArchiveIsNotSniper(t *testing.T) {
	v := steamrtByAppID[steamrtAppIDSteamrt4]
	if strings.Contains(v.Archive, "sniper") {
		t.Fatalf("steamrt4 归档名不应含 sniper，实际为 %q", v.Archive)
	}
	if v.Archive != "SteamLinuxRuntime_4.tar.xz" {
		t.Fatalf("Archive = %q, want SteamLinuxRuntime_4.tar.xz", v.Archive)
	}
}

func TestSteamrtForProtonFallsBackToVersionName(t *testing.T) {
	empty := t.TempDir() // 没有 toolmanifest.vdf：Proton 还没装好

	for _, tc := range []struct {
		version string
		want    string
	}{
		{"GE-Proton9-27", "steamrt3"},
		{"GE-Proton10-34", "steamrt3"},
		{"GE-Proton11-1", "steamrt4"},
	} {
		v, ok := steamrtForProton(empty, tc.version)
		if !ok || v.Variant != tc.want {
			t.Errorf("steamrtForProton(_, %q) = (%q, %v), want (%q, true)", tc.version, v.Variant, ok, tc.want)
		}
	}
}

func TestSteamrtForProtonUnknownGeneration(t *testing.T) {
	// 认不出就返回 false，不猜。调用方据此跳过预取、并保持
	// steamLinuxRuntimeReady 的 "steamrt*" 宽松判定。
	if v, ok := steamrtForProton(t.TempDir(), "GE-Proton12-0"); ok {
		t.Fatalf("未知代次应返回 false，实际拿到 %+v", v)
	}
	// manifest 里是个我们表里没有的 appid，同样不猜。
	if v, ok := steamrtForProton(writeToolManifest(t, "1391110"), "GE-Proton10-34"); ok {
		t.Fatalf("未知 appid 应返回 false，实际拿到 %+v", v)
	}
}

func TestSteamrtCacheName(t *testing.T) {
	got := steamrtCacheName(steamrtByAppID[steamrtAppIDSniper], "3.0.20260805.254768")
	want := "SteamLinuxRuntime_sniper.tar.xz.3.0.20260805.254768.parts"
	if got != want {
		t.Errorf("steamrtCacheName() = %q, want %q", got, want)
	}
}

func TestSteamrtFileURL(t *testing.T) {
	v := steamrtByAppID[steamrtAppIDSniper]
	got := steamrtFileURL(v, "3.0.20260805.254768", v.Archive)
	want := "https://repo.steampowered.com/steamrt3/images/3.0.20260805.254768/SteamLinuxRuntime_sniper.tar.xz"
	if got != want {
		t.Errorf("steamrtFileURL() = %q, want %q", got, want)
	}
}

func TestParseSHA256Sums(t *testing.T) {
	// 真实 SHA256SUMS 的形状：几百条不相干的条目，且 _4 与 _4-arm64 并存 ——
	// 按后缀匹配会串行，所以这里必须精确到字段。
	sums := []byte(strings.Join([]string{
		"1111111111111111111111111111111111111111111111111111111111111111  BUILD_ID.txt",
		"2222222222222222222222222222222222222222222222222222222222222222  SteamLinuxRuntime_4-arm64.tar.xz",
		"3333333333333333333333333333333333333333333333333333333333333333  SteamLinuxRuntime_4.tar.xz",
		"4444444444444444444444444444444444444444444444444444444444444444 *sources/foo.dsc",
		"",
	}, "\n"))

	got, err := parseSHA256Sums(sums, "SteamLinuxRuntime_4.tar.xz")
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Repeat("3", 64) {
		t.Errorf("摘要串行了: got %q", got)
	}

	got, err = parseSHA256Sums(sums, "SteamLinuxRuntime_4-arm64.tar.xz")
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Repeat("2", 64) {
		t.Errorf("arm64 条目取错: got %q", got)
	}

	// 二进制模式的 '*' 前缀要被剥掉
	if got, err = parseSHA256Sums(sums, "sources/foo.dsc"); err != nil || got != strings.Repeat("4", 64) {
		t.Errorf("'*' 前缀未处理: got %q, err %v", got, err)
	}

	if _, err := parseSHA256Sums(sums, "SteamLinuxRuntime_sniper.tar.xz"); err == nil {
		t.Error("缺失条目应报错")
	}
}

func TestSteamrtSafeToken(t *testing.T) {
	for _, ok := range []string{"3.0.20260805.254768", "4.0.20260805.254769", "20260805.254768", "a-b_c.1"} {
		if _, err := steamrtSafeToken("版本", ok); err != nil {
			t.Errorf("steamrtSafeToken(%q) 意外报错: %v", ok, err)
		}
	}
	// 这些值会被拼进 URL、并成为缓存文件名的一部分，必须挡住。
	for _, bad := range []string{"", "  ", "../../etc/passwd", "a/b", "..", "1.0/../..", "-lead", strings.Repeat("x", 65)} {
		if _, err := steamrtSafeToken("版本", bad); err == nil {
			t.Errorf("steamrtSafeToken(%q) 应报错", bad)
		}
	}
}

func TestSteamrtSafeTokenTrimsWhitespace(t *testing.T) {
	// latest-public-beta.txt / BUILD_ID.txt 都带结尾换行
	got, err := steamrtSafeToken("版本", "3.0.20260805.254768\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "3.0.20260805.254768" {
		t.Errorf("got %q", got)
	}
}
