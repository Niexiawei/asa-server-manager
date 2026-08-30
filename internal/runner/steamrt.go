package runner

// Steam Linux Runtime 预下载的纯逻辑部分：变体映射、归档名、SHA256SUMS 解析、
// umu 缓存文件命名。落盘/属主/HTTP 在 steamrt_linux.go。
//
// 这个文件刻意不加 //go:build linux —— 与 pkg/archive 同一个理由：里面全是字符串
// 与文件解析，没有任何平台专属 API，不加约束才能在 Windows 上直接跑单测。而
// 「steamrt4 的归档名是 SteamLinuxRuntime_4.tar.xz 不是 _sniper」这种事，正是需要
// 一条能天天跑的单测钉住的，见 docs/STEAMRT_PREFETCH_PLAN.md §2.2。

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// steamrtHost 是 Valve 官方的运行时仓库，也是 umu 自己写死的那一个。
// 刻意不做成配置项：需要走代理的用户 download.http_proxy 已经覆盖了这条路径
// （我们走 download.Client()），见 docs/STEAMRT_PREFETCH_PLAN.md §6。
const steamrtHost = "https://repo.steampowered.com"

// steamrtTailBytes 是预置缓存**故意**留空的尾部长度。
//
// 这不是保守起见，是必需的。umu 命中缓存后会发 "Range: bytes=<缓存大小>-" 续传，
// 而 repo.steampowered.com（Google-Edge-Cache）对**越界** Range 返回的是 200 + 全量
// body，不是 416：
//
//	Range: bytes=193105864-   =>  206  Content-Range: bytes 193105864-193105963/193105964
//	Range: bytes=193105964-   =>  200  Content-Length: 193105964      ← 越界，给全量
//
// 所以放一个「完整」的文件进缓存，umu 会以 "ab+" 把整个归档再追加一遍，文件变成两倍
// 长度，sha256 对不上，然后 raise ValueError("Digest mismatched") —— 那不是「优化不
// 生效」，是把一台本来能装成的机器弄挂。留空尾部 1 MiB 让 Range 落在中段（实测 206），
// umu 补齐后文件与上游逐字节一致，它自己的校验照常通过。
//
// 取 1 MiB 而不是 1 字节：留出足够宽的区间，避开任何 CDN 对极小 Range 的边界行为；
// 代价是一次 1 MiB 传输，可忽略。见 docs/STEAMRT_PREFETCH_PLAN.md §2.5 / §4.3。
const steamrtTailBytes = 1 << 20

// steamrtStampSuffix 标记「dest 已经是截好尾、可交给 umu 续传的缓存」。
// 内容是 "<sha256> <截尾后字节数>"，供重跑 setup 时判断能否跳过整包重下。
const steamrtStampSuffix = ".asa-prefetch"

// steamrtVariant 描述一个 Steam Linux Runtime 变体。
type steamrtVariant struct {
	// Variant 既是 repo.steampowered.com 上的路径段，也是 UMU_LOCAL 下的目录名。
	Variant string
	// Codename 是 umu RUNTIME_VERSIONS 里的 name 字段（归档名由它推导）。
	Codename string
	// Archive 是要下载的 tar.xz 文件名。
	Archive string
}

// Steam 侧的 compat tool appid，就是 GE-Proton 的 toolmanifest.vdf 里
// require_tool_appid 填的那个值。
const (
	steamrtAppIDSniper   = "1628350" // Steam Linux Runtime 3.0 (sniper)
	steamrtAppIDSteamrt4 = "4183110" // Steam Linux Runtime 4.0
)

// steamrtByAppID 是 umu 1.4.4 umu_runtime.py 里 RUNTIME_VERSIONS 表的子集。
//
// 只收 x86_64：ARK 服务端没有 arm64 构建，把 steamrt4-arm64 也列进来只会制造一条
// 永远走不到、也就永远没人验证的分支。认不出的变体一律返回 false，让 umu 自己去判断，
// 不猜。
//
// 归档名不是「variant 拼一下」就能得到的，umu 的推导是：
//
//	if codename.removeprefix("steamrt").removesuffix("-arm64").isdigit():
//	    archive = f"SteamLinuxRuntime_{codename.removeprefix('steamrt')}.tar.xz"
//	else:
//	    archive = f"SteamLinuxRuntime_{codename}.tar.xz"
//
// 代入 sniper 得 SteamLinuxRuntime_sniper.tar.xz，代入 steamrt4 得
// SteamLinuxRuntime_4.tar.xz —— 后者最容易被想当然写成 _sniper，写错的结果是 404。
var steamrtByAppID = map[string]steamrtVariant{
	steamrtAppIDSniper:   {Variant: "steamrt3", Codename: "sniper", Archive: "SteamLinuxRuntime_sniper.tar.xz"},
	steamrtAppIDSteamrt4: {Variant: "steamrt4", Codename: "steamrt4", Archive: "SteamLinuxRuntime_4.tar.xz"},
}

var requireToolAppIDRe = regexp.MustCompile(`(?i)"require_tool_appid"\s+"(\d+)"`)

// steamrtForProton 解析某个 GE-Proton 构建需要哪个 Steam Linux Runtime 变体。
//
// 权威来源是 {protonDir}/toolmanifest.vdf 的 require_tool_appid —— umu 自己就是这么
// 查的（CompatLayer.required_runtime），照做才不会在 GE-Proton 换代时和 umu 的判断
// 分叉。装还没装好、或 manifest 格式变了时，回落到版本名前缀这套启发式；再认不出
// 就返回 false，表示「不知道，别猜」。
func steamrtForProton(protonDir, protonVersion string) (steamrtVariant, bool) {
	if appID, ok := protonRequiredToolAppID(protonDir); ok {
		v, known := steamrtByAppID[appID]
		return v, known
	}
	return steamrtForProtonVersion(protonVersion)
}

// protonRequiredToolAppID 从 toolmanifest.vdf 里抠出 require_tool_appid。
// 用正则而不是引入 vdf 解析库：要读的就这一个标量字段，manifest 只有几行。
func protonRequiredToolAppID(protonDir string) (string, bool) {
	if protonDir == "" {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(protonDir, "toolmanifest.vdf"))
	if err != nil {
		return "", false
	}
	m := requireToolAppIDRe.FindSubmatch(data)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

// steamrtForProtonVersion 是按版本名前缀的回落判断：GE-Proton 9/10 用 steamrt3
// (sniper)，GE-Proton 11 用 steamrt4。只在读不到 toolmanifest.vdf 时才轮到它。
func steamrtForProtonVersion(protonVersion string) (steamrtVariant, bool) {
	switch {
	case strings.HasPrefix(protonVersion, "GE-Proton9-"), strings.HasPrefix(protonVersion, "GE-Proton10-"):
		return steamrtByAppID[steamrtAppIDSniper], true
	case strings.HasPrefix(protonVersion, "GE-Proton11-"):
		return steamrtByAppID[steamrtAppIDSteamrt4], true
	}
	return steamrtVariant{}, false
}

// steamrtCacheName 复刻 umu 的缓存文件命名：f"{archive}.{buildid}.parts"。
// 名字对不上不会出错，只是缓存打空、umu 照常全量下载。
func steamrtCacheName(v steamrtVariant, buildID string) string {
	return v.Archive + "." + buildID + ".parts"
}

func steamrtImagesURL(v steamrtVariant) string {
	return steamrtHost + "/" + v.Variant + "/images"
}

func steamrtFileURL(v steamrtVariant, version, name string) string {
	return steamrtImagesURL(v) + "/" + version + "/" + name
}

// steamrtTokenRe 限定 version / BUILD_ID 的取值。两者都来自远端、都会被拼进 URL，
// BUILD_ID 还会成为缓存文件名的一部分 —— 不校验就等于让上游（或中间人、或一个配错的
// 镜像）决定我们往哪个路径写文件。
var steamrtTokenRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)

func steamrtSafeToken(kind, s string) (string, error) {
	s = strings.TrimSpace(s)
	if !steamrtTokenRe.MatchString(s) || strings.Contains(s, "..") {
		return "", fmt.Errorf("%s 取值异常: %q", kind, s)
	}
	return s, nil
}

// parseSHA256Sums 从 sha256sum 格式的清单（"<hex>  <文件名>"，二进制模式下文件名前
// 有个 '*'）里取出 name 对应的摘要。
//
// 按字段精确匹配文件名，不用 HasSuffix：同一个 images 目录里既有
// SteamLinuxRuntime_4.tar.xz 也有 SteamLinuxRuntime_4-arm64.tar.xz，SHA256SUMS 里
// 另有几百个不相干的条目。
func parseSHA256Sums(data []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("解析 SHA256SUMS 失败: %w", err)
	}
	return "", fmt.Errorf("SHA256SUMS 里没有 %s 的条目", name)
}
