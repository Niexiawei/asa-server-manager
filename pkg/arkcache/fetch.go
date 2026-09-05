package arkcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"asa-server/pkg/download"
)

// DefaultURLs 是 ArkApi 内置的三个 CDN 前缀，顺序与 ArkBaseApi.cpp:318-322 一致。
//
// ⚠️ 顺序是承重的，尤其是第一个：写进 cached_key.cache 的 last_modified 必须取自
// ArkApi 会**优先** HEAD 的那个 CDN，否则它会判定本地缓存过期并整包重下，预取白做
// （方案 §4.5）。用户覆盖 arkapi_cache.urls 时也应保持第一个相同。
var DefaultURLs = []string{
	"https://cdn.pelayori.com/cache/",
	"https://cdn.shadowhunter.co.za/cache/",
	"https://cdn.shadowhunter-systems.co.za/cache/",
}

// sidecar 是 <hash>.zip.meta.json，记录 .part 到底是从哪个源、哪个版本下来的。
//
// 这个方案**没有校验和可用** —— URL 里那个哈希是 exe 的，不是 ZIP 的。跨 CDN 续传
// 的安全性全靠它：源或长度或 ETag 一变就丢掉 .part 重下，绝不 append 到一段来路
// 不同的字节上。完整性由「大小对得上 + ZIP 能打开 + 条目合白名单 + 两个 .cache
// 通过结构校验」四条共同保证。
type sidecar struct {
	SourceURL     string `json:"source_url"`
	ETag          string `json:"etag"`
	ContentLength int64  `json:"content_length"`
	LastModified  string `json:"last_modified"`
}

// headInfo 是一次 HEAD 的结果。
type headInfo struct {
	url           string
	contentLength int64
	etag          string
	lastModified  string
}

func (h headInfo) sidecar() sidecar {
	return sidecar{SourceURL: h.url, ETag: h.etag, ContentLength: h.contentLength, LastModified: h.lastModified}
}

// sameSource 判断已有的 .part 还能不能接着下。
func (s sidecar) sameSource(h headInfo) bool {
	return s.SourceURL == h.url && s.ContentLength == h.contentLength && s.ETag == h.etag
}

// fetchOutcome 是一次成功下载的产物。
type fetchOutcome struct {
	zipPath string
	// lastModified 是要写进 cached_key.cache 的那个值：**主 CDN 返回的原始
	// Last-Modified 头**，不做任何格式化、时区换算或重排（方案 §4.5）。
	lastModified string
	sourceURL    string
}

var errNoCDN = errors.New("所有 CDN 都拿不到这个 exe 版本的 offsets cache")

// headRequest 发一次 HEAD。
//
// 跟随重定向是有意的（不去关 http.Client 的默认策略）：ArkApi 那边最多跟 5 跳并
// 取终点响应的头（Requests.cpp:996-1014），CDN 哪天加一层 302，我们和它仍然看到
// 同一个值。用 download.Client() 是为了继承 download.http_proxy 与超时 —— 那正是
// 「ArkApi 自己下」读不到的东西。
func headRequest(ctx context.Context, url string) (headInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return headInfo{}, err
	}
	resp, err := download.Client().Do(req)
	if err != nil {
		return headInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return headInfo{}, fmt.Errorf("HEAD %s: %s", url, resp.Status)
	}
	return headInfo{
		url:           url,
		contentLength: resp.ContentLength,
		etag:          resp.Header.Get("ETag"),
		lastModified:  resp.Header.Get("Last-Modified"),
	}, nil
}

// revalidateTimeout 给快路径上那次 HEAD 封顶。它是**纯优化**（问不到就用本地那份），
// 所以宁可早早放弃，也不能让一次启动挂在一个不可达的 CDN 上。断网的机器本来就要
// 被 ArkApi 自己那三轮 HEAD 拖掉 60~120 秒，我们这 10 秒是噪声。
const revalidateTimeout = 10 * time.Second

// PrimaryLastModified 返回主 CDN 此刻的 Last-Modified，拿不到时返回空串 ——
// 也就是 ArkApi 待会儿会拿来和 cached_key.cache 逐字比对的那个值。
//
// 只问**第一个** CDN，这不是偷懒：ArkApi 的 CDN 循环只在抛 Poco 异常时才换下一个
// （ArkBaseApi.cpp:378-390），HEAD 返回 404 或没有 Last-Modified 头会直接 break，
// 所以它实践中拿去比对的就是列表第一个的值。问别人问出来的值再对也没用
// —— 判定权在 ArkApi 手里（方案 §4.5 第 2 条）。
//
// 导出是给 `asa-server arkapi-cache status` 用的：把「过期与否」直接问出来，
// 比让用户去对 cached_key.cache 的原文强得多。
func PrimaryLastModified(ctx context.Context, req Request, hash string) string {
	req = req.withDefaults()
	if len(req.URLs) == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, revalidateTimeout)
	defer cancel()

	info, err := headRequest(ctx, zipURL(req.URLs[0], hash))
	if err != nil {
		return ""
	}
	return info.lastModified
}

func zipURL(base, hash string) string {
	return strings.TrimSuffix(base, "/") + "/" + hash + ".zip"
}

func readSidecar(path string) sidecar {
	var s sidecar
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s)
	return s
}

func writeSidecar(path string, s sidecar) {
	if data, err := json.Marshal(s); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}

// fetchZip 按序试每个 CDN，把 <hash>.zip 下到 req.WorkDir。
//
// .part 与旁车都落在 WorkDir，**不落在源目录的 Cache 下** —— 否则镜像同步会把一个
// 下了一半的 .part 当成源里的新文件复制进每个实例（方案 §4.2）。
func fetchZip(ctx context.Context, req Request, hash string) (fetchOutcome, error) {
	if err := os.MkdirAll(req.WorkDir, 0o755); err != nil {
		return fetchOutcome{}, err
	}
	zipPath := filepath.Join(req.WorkDir, hash+".zip")
	metaPath := zipPath + ".meta.json"
	partPath := zipPath + ".part"

	var lastErr error = errNoCDN
	for i, base := range req.URLs {
		if ctx.Err() != nil {
			return fetchOutcome{}, ctx.Err()
		}
		url := zipURL(base, hash)

		info, err := headRequest(ctx, url)
		if err != nil {
			lastErr = err
			continue
		}
		if info.contentLength <= 0 {
			lastErr = fmt.Errorf("HEAD %s: 没有可用的 Content-Length", url)
			continue
		}
		if info.contentLength > req.MaxSize {
			lastErr = fmt.Errorf("HEAD %s: %d 字节超过上限 %d", url, info.contentLength, req.MaxSize)
			continue
		}

		// 换源/换版本就绝不能 append 到旧 .part 上。
		if !readSidecar(metaPath).sameSource(info) {
			_ = os.Remove(partPath)
		}
		writeSidecar(metaPath, info.sidecar())

		if fi, statErr := os.Stat(zipPath); statErr != nil || fi.Size() != info.contentLength {
			_ = os.Remove(zipPath)
			if err := download.Fetch(ctx, download.Options{
				URL:      url,
				Dest:     zipPath,
				Resume:   true,
				Progress: req.Progress,
			}); err != nil {
				if ctx.Err() != nil {
					return fetchOutcome{}, ctx.Err() // .part 保留，下次续传
				}
				lastErr = err
				continue
			}
		}

		fi, err := os.Stat(zipPath)
		if err != nil {
			lastErr = err
			continue
		}
		if fi.Size() != info.contentLength {
			_ = os.Remove(zipPath)
			lastErr = fmt.Errorf("下载完成但大小不符：%d != %d", fi.Size(), info.contentLength)
			continue
		}

		return fetchOutcome{
			zipPath:      zipPath,
			lastModified: primaryLastModified(ctx, req, hash, i, info),
			sourceURL:    url,
		}, nil
	}
	return fetchOutcome{}, lastErr
}

// primaryLastModified 决定写进 metadata 的 last_modified。
//
// ArkApi 的 CDN 循环（ArkBaseApi.cpp:378-390）**只在抛 Poco 异常（连不上/超时）时
// 才换下一个**：HEAD 返回 false（404，或 200 但没有 Last-Modified 头）会直接 break。
// 所以它实践中几乎总是只问列表第一个。我们要是因为主 CDN 挂了而从备用源下载，就得
// 回头补一次对主 CDN 的 HEAD —— 拿得到就用主 CDN 的值，拿不到再退用实际下载源的
// （方案 §4.5 第 2 条）。
func primaryLastModified(ctx context.Context, req Request, hash string, usedIndex int, used headInfo) string {
	if usedIndex == 0 || len(req.URLs) == 0 {
		return used.lastModified
	}
	if primary, err := headRequest(ctx, zipURL(req.URLs[0], hash)); err == nil && primary.lastModified != "" {
		return primary.lastModified
	}
	return used.lastModified
}
