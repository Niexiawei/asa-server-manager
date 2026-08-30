//go:build linux

package runner

// Steam Linux Runtime 预下载。见 docs/STEAMRT_PREFETCH_PLAN.md。
//
// warmPrefix 里那次 `umu-run wineboot --init` 是全流程唯一允许联网抓运行时的调用，
// umu 会在它内部用自带的 urllib3 从 repo.steampowered.com 拉 150~190 MB —— 我们的
// 重试、断点续传、download.http_proxy 一个都够不着，超时的代价是整个 EnsureRuntime
// 报错、用户从头再来。
//
// 这里做的事只有一件：提前用 pkg/download 把那个归档下好，放进 umu 自己的下载缓存
// （UMU_CACHE），让 wineboot 起来时只剩「续传补最后 1 MiB」。umu 依然是唯一负责解压、
// 校验、写 marker、建软链的一方，我们只负责让字节先躺在它要找的地方。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"asa-server/pkg/download"
)

// umuCacheDir 是 umu 的下载中转目录 UMU_CACHE。
//
// umu_consts.py: UMU_CACHE = XDG_CACHE_HOME/umu，XDG_CACHE_HOME 缺省为 ~/.cache。
// 在我们的进程树里 XDG_* 恒为空 —— inheritedEnv 的白名单根本没有 XDG_*，runtimeEnv
// 还会再剥一道 —— 所以它恒等于 {runtimeHomeDir}/.cache/umu。
func umuCacheDir(cfg Config) string {
	return filepath.Join(runtimeHomeDir(cfg), ".cache", "umu")
}

// prefetchSteamRuntime 预取 Steam Linux Runtime 归档。
//
// 返回命中的变体（零值表示「本次不需要预取」，不是错误）。调用方必须把错误当成
// 「降级」而不是「失败」：这个优化的全部价值是省时间，为省时间制造一个新的安装
// 失败点是净亏。
func prefetchSteamRuntime(ctx context.Context, cfg Config, logf func(string, ...any)) (steamrtVariant, error) {
	if cfg.Runtime != "umu" || !cfg.AutoDownload || !cfg.SteamRTPrefetch {
		return steamrtVariant{}, nil
	}
	v, ok := steamrtForProton(protonPath(cfg), cfg.ProtonVersion)
	if !ok {
		logf("跳过 Steam Linux Runtime 预下载：认不出 %s 需要哪个运行时变体，交给 umu 自行判断", cfg.ProtonVersion)
		return steamrtVariant{}, nil
	}
	if steamLinuxRuntimeReady(cfg) {
		return steamrtVariant{}, nil
	}

	home := runtimeHomeDir(cfg)
	if home == "" {
		return steamrtVariant{}, fmt.Errorf("无法确定 umu 缓存所在的家目录")
	}

	version, buildID, digest, err := resolveSteamrtImage(ctx, v)
	if err != nil {
		return steamrtVariant{}, err
	}

	cacheDir := umuCacheDir(cfg)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return steamrtVariant{}, fmt.Errorf("创建 umu 下载缓存目录失败: %w", err)
	}
	pruneStaleSteamrtParts(cacheDir, v, buildID)

	dest := filepath.Join(cacheDir, steamrtCacheName(v, buildID))
	if steamrtCachePrepared(dest, digest) {
		logf("Steam Linux Runtime %s (%s) 已在缓存中，跳过预下载", v.Variant, version)
	} else if err := fetchSteamrtArchive(ctx, v, version, digest, dest, logf); err != nil {
		return steamrtVariant{}, err
	}

	if err := handSteamrtCacheToRuntime(cfg, home, cacheDir, dest); err != nil {
		return steamrtVariant{}, err
	}
	return v, nil
}

// resolveSteamrtImage 取回本次要下的镜像版本、BUILD_ID 与归档的 sha256。
//
// 三个都是小文件（20 B / 20 B / ~280 KiB），走 download.Client() 直接读，不经
// download.Fetch —— 那是给大文件准备的。BUILD_ID 决定缓存文件名，必须和 umu 稍后
// 自己算出来的完全一致，所以这里的解析顺序刻意与 _install_umu 保持一致。
func resolveSteamrtImage(ctx context.Context, v steamrtVariant) (version, buildID, digest string, err error) {
	raw, err := steamrtGet(ctx, steamrtImagesURL(v)+"/latest-public-beta.txt", 4<<10)
	if err != nil {
		return "", "", "", fmt.Errorf("获取 %s 最新版本号失败: %w", v.Variant, err)
	}
	if version, err = steamrtSafeToken("运行时版本号", string(raw)); err != nil {
		return "", "", "", err
	}

	if raw, err = steamrtGet(ctx, steamrtFileURL(v, version, "BUILD_ID.txt"), 4<<10); err != nil {
		return "", "", "", fmt.Errorf("获取 %s %s 的 BUILD_ID 失败: %w", v.Variant, version, err)
	}
	if buildID, err = steamrtSafeToken("BUILD_ID", string(raw)); err != nil {
		return "", "", "", err
	}

	sums, err := steamrtGet(ctx, steamrtFileURL(v, version, "SHA256SUMS"), 8<<20)
	if err != nil {
		return "", "", "", fmt.Errorf("获取 %s %s 的 SHA256SUMS 失败: %w", v.Variant, version, err)
	}
	if digest, err = parseSHA256Sums(sums, v.Archive); err != nil {
		return "", "", "", err
	}
	return version, buildID, digest, nil
}

// fetchSteamrtArchive 下载归档、校验 sha256，然后截掉尾部交给 umu 续传。
func fetchSteamrtArchive(ctx context.Context, v steamrtVariant, version, digest, dest string, logf func(string, ...any)) error {
	// 先下到 .full：截尾之后的文件本身是"残缺"的，不能让它在中途被误认成成品。
	// download.Fetch 的 .part 续传作用在这个路径上，中断重跑不会白下。
	full := dest + ".full"
	url := steamrtFileURL(v, version, v.Archive)

	logf("正在预下载 Steam Linux Runtime %s (%s)：%s", v.Variant, version, url)
	if err := download.Fetch(ctx, download.Options{
		URL:      url,
		Dest:     full,
		Checksum: "sha256:" + digest,
		Resume:   true,
		Progress: downloadProgress(v.Variant, logf),
	}); err != nil {
		return fmt.Errorf("下载 %s 失败: %w", v.Archive, err)
	}

	size, err := truncateForUmuResume(full, dest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dest+steamrtStampSuffix,
		[]byte(digest+" "+strconv.FormatInt(size, 10)+"\n"), 0o644); err != nil {
		return fmt.Errorf("写预下载标记失败: %w", err)
	}

	logf("Steam Linux Runtime %s 预下载完成，umu 初始化时只需补齐最后 %d MiB",
		v.Variant, steamrtTailBytes>>20)
	return nil
}

// truncateForUmuResume 把校验通过的完整归档改名到 dest 并截掉尾部，返回截尾后大小。
// 为什么必须截：见 steamrtTailBytes 的注释。
func truncateForUmuResume(full, dest string) (int64, error) {
	fi, err := os.Stat(full)
	if err != nil {
		return 0, err
	}
	if fi.Size() <= steamrtTailBytes {
		// 真实归档是 150 MB 起，走到这里说明拿到的根本不是运行时镜像
		// （校验已经过了，那就是上游把 SHA256SUMS 和内容一起换掉了）。
		return 0, fmt.Errorf("下载到的 %s 只有 %d 字节，不像是运行时镜像", filepath.Base(full), fi.Size())
	}
	if err := os.Rename(full, dest); err != nil {
		return 0, err
	}
	size := fi.Size() - steamrtTailBytes
	if err := os.Truncate(dest, size); err != nil {
		// 留一个截了一半的文件在缓存里，下次 umu 会从错误的偏移续传并以
		// "Digest mismatched" 中止安装 —— 那正是这套机制唯一能造成的实质伤害。
		_ = os.Remove(dest)
		return 0, fmt.Errorf("截断预下载缓存失败: %w", err)
	}
	return size, nil
}

// steamrtCachePrepared 判断 dest 是否已经是本次要用的、截好尾的缓存。
//
// 只看文件在不在是不够的：一个大小对不上的残留会让 umu 从错误的偏移续传，最后以
// "Digest mismatched" 中止安装。所以判据是标记文件里的 sha256 与截尾后大小都对得上。
func steamrtCachePrepared(dest, digest string) bool {
	data, err := os.ReadFile(dest + steamrtStampSuffix)
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || !strings.EqualFold(fields[0], digest) {
		return false
	}
	fi, err := os.Stat(dest)
	if err != nil {
		return false
	}
	return strconv.FormatInt(fi.Size(), 10) == fields[1]
}

// pruneStaleSteamrtParts 清掉本归档在 UMU_CACHE 里其它 BUILD_ID 的残留。
//
// 上游发新版之后旧的预取文件就再也不会被 umu 找到了，不清就是一个 190 MB 的垃圾。
// 当前 BUILD_ID 相关的一律留着（.parts / .parts.full / .parts.full.part / 标记文件），
// 它们正是续传要用的。
func pruneStaleSteamrtParts(cacheDir string, v steamrtVariant, buildID string) {
	matches, _ := filepath.Glob(filepath.Join(cacheDir, v.Archive+".*"))
	keep := filepath.Join(cacheDir, steamrtCacheName(v, buildID))
	for _, m := range matches {
		if strings.HasPrefix(m, keep) {
			continue
		}
		_ = os.Remove(m)
	}
}

// handSteamrtCacheToRuntime 把缓存交给降权后的运行时用户。
//
// umu 在 UMU_CACHE 里要 mkdtemp / rename / unlink（只需目录权限）并读 .parts（需文件
// 权限），两者在这里一次性给全。
//
// 必须在这里显式做，不能指望 ensureRuntimeUser 的 reconcileRuntimeOwnership：那一步
// 跑在 ensureRuntime 的最开头，而这些目录是我们**之后**才创建的 —— 和 ACL 加固那次
// 「共享访问准备必须挪到 runner.Run 正前方」是同一个教训，见
// docs/ACL_PERMISSION_HARDENING_PLAN.md。
func handSteamrtCacheToRuntime(cfg Config, home, cacheDir, dest string) error {
	for _, p := range []string{
		filepath.Join(home, ".cache"),
		cacheDir,
		dest,
		dest + steamrtStampSuffix,
	} {
		if err := chownPathForRuntime(p); err != nil {
			return fmt.Errorf("把 %s 交给运行时用户失败: %w", p, err)
		}
	}
	return nil
}

// steamrtGet 读一个小的元数据文件。用 download.Client() 而不是裸 http.DefaultClient，
// 这样 config.yaml 的 download.http_proxy / timeout 对这几个请求同样生效。
func steamrtGet(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := download.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s 返回 %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}
