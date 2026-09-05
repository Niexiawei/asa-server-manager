//go:build linux

package steamrt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"asa-server/pkg/download"
)

// Prefetch downloads (or confirms an already-prepared cache for) the Steam
// Linux Runtime archive that protonVersion/protonDir need, truncates it so
// umu's own resume-download can finish it, and returns the resolved Variant.
// A zero Variant with a nil error means "nothing to prefetch" (variant
// unrecognized), not an error — the caller should let umu figure it out on
// its own.
//
// chown, if non-nil, is called for every path Prefetch creates or writes
// into — the cache directory, its parent, the archive, and its stamp file —
// so a caller running as a different user than the one who will later read
// these files (see the drop-privileges runtime-user model) can hand them
// over. Prefetch does not know what a "runtime user" is; it just reports
// which paths need a new owner.
//
// A failure here must be treated by the caller as a degradation, not a
// launch failure: the entire value of this optimization is saving time, and
// turning it into a new install failure point would be a net loss.
func Prefetch(ctx context.Context, protonDir, protonVersion, cacheDir string, chown func(path string) error, logf func(string, ...any)) (Variant, error) {
	v, ok := ForProton(protonDir, protonVersion)
	if !ok {
		logf("跳过 Steam Linux Runtime 预下载：认不出 %s 需要哪个运行时变体，交给 umu 自行判断", protonVersion)
		return Variant{}, nil
	}

	version, buildID, digest, err := resolveImage(ctx, v)
	if err != nil {
		return Variant{}, err
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return Variant{}, fmt.Errorf("创建 umu 下载缓存目录失败: %w", err)
	}
	pruneStaleParts(cacheDir, v, buildID)

	dest := filepath.Join(cacheDir, CacheName(v, buildID))
	if cachePrepared(dest, digest) {
		logf("Steam Linux Runtime %s (%s) 已在缓存中，跳过预下载", v.Variant, version)
	} else if err := fetchArchive(ctx, v, version, digest, dest, logf); err != nil {
		return Variant{}, err
	}

	if chown != nil {
		// filepath.Dir(cacheDir) 是 umu 的 XDG_CACHE_HOME（约定上是
		// {home}/.cache），Prefetch 隐式创建了它——同交给运行时用户，理由与
		// cacheDir 本身相同。
		for _, p := range []string{filepath.Dir(cacheDir), cacheDir, dest, dest + StampSuffix} {
			if err := chown(p); err != nil {
				return Variant{}, fmt.Errorf("把 %s 交给运行时用户失败: %w", p, err)
			}
		}
	}
	return v, nil
}

// resolveImage retrieves this run's target image version, BUILD_ID, and the
// archive's sha256.
//
// All three are small files (20 B / 20 B / ~280 KiB), read directly via
// download.Client() rather than download.Fetch (that one is for large
// files). BUILD_ID becomes part of the cache file name and must match
// exactly what umu computes on its own later, so the resolution order here
// deliberately mirrors umu's own _install_umu.
func resolveImage(ctx context.Context, v Variant) (version, buildID, digest string, err error) {
	raw, err := get(ctx, imagesURL(v)+"/latest-public-beta.txt", 4<<10)
	if err != nil {
		return "", "", "", fmt.Errorf("获取 %s 最新版本号失败: %w", v.Variant, err)
	}
	if version, err = SafeToken("运行时版本号", string(raw)); err != nil {
		return "", "", "", err
	}

	if raw, err = get(ctx, fileURL(v, version, "BUILD_ID.txt"), 4<<10); err != nil {
		return "", "", "", fmt.Errorf("获取 %s %s 的 BUILD_ID 失败: %w", v.Variant, version, err)
	}
	if buildID, err = SafeToken("BUILD_ID", string(raw)); err != nil {
		return "", "", "", err
	}

	sums, err := get(ctx, fileURL(v, version, "SHA256SUMS"), 8<<20)
	if err != nil {
		return "", "", "", fmt.Errorf("获取 %s %s 的 SHA256SUMS 失败: %w", v.Variant, version, err)
	}
	if digest, err = ParseSHA256Sums(sums, v.Archive); err != nil {
		return "", "", "", err
	}
	return version, buildID, digest, nil
}

// fetchArchive downloads the archive, verifies its sha256, then truncates
// the tail for umu to resume.
func fetchArchive(ctx context.Context, v Variant, version, digest, dest string, logf func(string, ...any)) error {
	// 先下到 .full：截尾之后的文件本身是"残缺"的，不能让它在中途被误认成成品。
	// download.Fetch 的 .part 续传作用在这个路径上，中断重跑不会白下。
	full := dest + ".full"
	url := fileURL(v, version, v.Archive)

	logf("正在预下载 Steam Linux Runtime %s (%s)：%s", v.Variant, version, url)
	if err := download.Fetch(ctx, download.Options{
		URL:      url,
		Dest:     full,
		Checksum: "sha256:" + digest,
		Resume:   true,
		Progress: progressLogf(v.Variant, logf),
	}); err != nil {
		return fmt.Errorf("下载 %s 失败: %w", v.Archive, err)
	}

	size, err := truncateForResume(full, dest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dest+StampSuffix,
		[]byte(digest+" "+strconv.FormatInt(size, 10)+"\n"), 0o644); err != nil {
		return fmt.Errorf("写预下载标记失败: %w", err)
	}

	logf("Steam Linux Runtime %s 预下载完成，umu 初始化时只需补齐最后 %d MiB",
		v.Variant, TailBytes>>20)
	return nil
}

// truncateForResume renames the checksum-verified full archive to dest and
// truncates its tail, returning the post-truncation size. See TailBytes for
// why this truncation is required.
func truncateForResume(full, dest string) (int64, error) {
	fi, err := os.Stat(full)
	if err != nil {
		return 0, err
	}
	if fi.Size() <= TailBytes {
		// 真实归档是 150 MB 起，走到这里说明拿到的根本不是运行时镜像
		// （校验已经过了，那就是上游把 SHA256SUMS 和内容一起换掉了）。
		return 0, fmt.Errorf("下载到的 %s 只有 %d 字节，不像是运行时镜像", filepath.Base(full), fi.Size())
	}
	if err := os.Rename(full, dest); err != nil {
		return 0, err
	}
	size := fi.Size() - TailBytes
	if err := os.Truncate(dest, size); err != nil {
		// 留一个截了一半的文件在缓存里，下次 umu 会从错误的偏移续传并以
		// "Digest mismatched" 中止安装 —— 那正是这套机制唯一能造成的实质伤害。
		_ = os.Remove(dest)
		return 0, fmt.Errorf("截断预下载缓存失败: %w", err)
	}
	return size, nil
}

// cachePrepared reports whether dest is already this run's tail-truncated
// cache.
//
// Existence alone isn't enough: a leftover of the wrong size would make umu
// resume from the wrong offset and abort with "Digest mismatched". So the
// test is that the stamp file's sha256 and post-truncation size both match.
func cachePrepared(dest, digest string) bool {
	data, err := os.ReadFile(dest + StampSuffix)
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

// pruneStaleParts clears this archive's leftovers from other BUILD_IDs in
// cacheDir.
//
// Once upstream ships a new version, umu will never look at the old
// prefetched file again — leaving it is just a ~190 MB piece of garbage.
// Anything matching the current BUILD_ID is kept (.parts / .parts.full /
// .parts.full.part / the stamp file); those are what a resume needs.
func pruneStaleParts(cacheDir string, v Variant, buildID string) {
	matches, _ := filepath.Glob(filepath.Join(cacheDir, v.Archive+".*"))
	keep := filepath.Join(cacheDir, CacheName(v, buildID))
	for _, m := range matches {
		if strings.HasPrefix(m, keep) {
			continue
		}
		_ = os.Remove(m)
	}
}

// get reads one small metadata file. Uses download.Client() rather than a
// bare http.DefaultClient so config.yaml's download.http_proxy / timeout
// apply to these requests too.
func get(ctx context.Context, url string, limit int64) ([]byte, error) {
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

// progressLogf throttles pkg/download's byte-level callback into a
// human-readable progress line: one line per 5% or 2 seconds, plus a final
// line. The callback runs serially on io.Copy's single goroutine, so no
// locking is needed.
//
// When the total size is unknown (total <= 0) it throttles by time only —
// otherwise the percentage would be stuck at 0 and "gained 5%" would never
// trip, printing a line for every chunk read.
func progressLogf(label string, logf func(string, ...any)) func(done, total int64) {
	var (
		lastAt  time.Time
		lastPct = -1
	)
	return func(done, total int64) {
		pct := 0
		if total > 0 {
			pct = int(done * 100 / total)
		}
		final := total > 0 && done >= total
		now := time.Now()
		if !final && pct < lastPct+5 && now.Sub(lastAt) < 2*time.Second {
			return
		}
		lastAt, lastPct = now, pct
		if total > 0 {
			logf("  %s: %d%% (%.1f/%.1f MiB)", label, pct, mib(done), mib(total))
		} else {
			logf("  %s: %.1f MiB", label, mib(done))
		}
	}
}

func mib(n int64) float64 { return float64(n) / (1 << 20) }
