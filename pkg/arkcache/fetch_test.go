package arkcache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type cdnOpts struct {
	notFound    bool
	ignoreRange bool // 服务端无视 Range，返回 200 全量
	headSize    int64
	etag        string
}

type fakeCDN struct {
	*httptest.Server
	mu          sync.Mutex
	rangeStarts []int64
	gets        int
	heads       int
}

func newCDN(t *testing.T, body []byte, lastModified string, opts cdnOpts) *fakeCDN {
	t.Helper()
	c := &fakeCDN{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if opts.notFound {
			http.NotFound(w, r)
			return
		}
		if lastModified != "" {
			w.Header().Set("Last-Modified", lastModified)
		}
		if opts.etag != "" {
			w.Header().Set("ETag", opts.etag)
		}

		if r.Method == http.MethodHead {
			c.mu.Lock()
			c.heads++
			c.mu.Unlock()
			size := int64(len(body))
			if opts.headSize > 0 {
				size = opts.headSize
			}
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
			w.WriteHeader(http.StatusOK)
			return
		}

		c.mu.Lock()
		c.gets++
		c.mu.Unlock()

		var start int64
		if rng := r.Header.Get("Range"); rng != "" {
			fmt.Sscanf(rng, "bytes=%d-", &start)
			c.mu.Lock()
			c.rangeStarts = append(c.rangeStarts, start)
			c.mu.Unlock()
			if !opts.ignoreRange {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
				w.Header().Set("Content-Length", strconv.FormatInt(int64(len(body))-start, 10))
				w.WriteHeader(http.StatusPartialContent)
				w.Write(body[start:])
				return
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *fakeCDN) prefix() string { return c.URL + "/cache/" }

func fetchRequest(t *testing.T, urls ...string) Request {
	t.Helper()
	return Request{WorkDir: t.TempDir(), URLs: urls}.withDefaults()
}

func TestFetchZipHappyPath(t *testing.T) {
	body := bytes.Repeat([]byte("Z"), 4096)
	cdn := newCDN(t, body, "Thu, 03 Sep 2026 15:20:10 GMT", cdnOpts{})
	req := fetchRequest(t, cdn.prefix())

	out, err := fetchZip(context.Background(), req, testHash)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out.zipPath)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("内容不符: err=%v len=%d", err, len(got))
	}
	if out.lastModified != "Thu, 03 Sep 2026 15:20:10 GMT" {
		t.Fatalf("lastModified = %q", out.lastModified)
	}
}

// .part 存在且旁车一致时必须**续传**，Range 起始偏移就等于 .part 的大小。
func TestFetchZipResumesFromPart(t *testing.T) {
	body := bytes.Repeat([]byte("R"), 4096)
	cdn := newCDN(t, body, "LM", cdnOpts{etag: `"v1"`})
	req := fetchRequest(t, cdn.prefix())

	zipPath := filepath.Join(req.WorkDir, testHash+".zip")
	if err := os.WriteFile(zipPath+".part", body[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	writeSidecar(zipPath+".meta.json", sidecar{
		SourceURL:     zipURL(cdn.prefix(), testHash),
		ETag:          `"v1"`,
		ContentLength: int64(len(body)),
		LastModified:  "LM",
	})

	if _, err := fetchZip(context.Background(), req, testHash); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(zipPath); !bytes.Equal(got, body) {
		t.Fatalf("续传后内容不符，长度 %d", len(got))
	}
	cdn.mu.Lock()
	defer cdn.mu.Unlock()
	if len(cdn.rangeStarts) != 1 || cdn.rangeStarts[0] != 1000 {
		t.Fatalf("Range 起始偏移 = %v，want [1000]", cdn.rangeStarts)
	}
}

// 服务端无视 Range 返回 200 时，绝不能 append —— 那会拼出一段双倍长度的垃圾。
func TestFetchZipRestartsWhenServerIgnoresRange(t *testing.T) {
	body := bytes.Repeat([]byte("I"), 2048)
	cdn := newCDN(t, body, "LM", cdnOpts{ignoreRange: true, etag: `"v1"`})
	req := fetchRequest(t, cdn.prefix())

	zipPath := filepath.Join(req.WorkDir, testHash+".zip")
	if err := os.WriteFile(zipPath+".part", body[:500], 0o644); err != nil {
		t.Fatal(err)
	}
	writeSidecar(zipPath+".meta.json", sidecar{
		SourceURL:     zipURL(cdn.prefix(), testHash),
		ETag:          `"v1"`,
		ContentLength: int64(len(body)),
		LastModified:  "LM",
	})

	if _, err := fetchZip(context.Background(), req, testHash); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(zipPath); !bytes.Equal(got, body) {
		t.Fatalf("应当重下而不是 append，得到 %d 字节", len(got))
	}
}

// 旁车对不上（换了源/换了版本）时必须丢掉 .part，绝不 append 到来路不同的字节上。
func TestFetchZipDropsPartOnSidecarMismatch(t *testing.T) {
	body := bytes.Repeat([]byte("M"), 3000)
	cdn := newCDN(t, body, "LM", cdnOpts{etag: `"v2"`})
	req := fetchRequest(t, cdn.prefix())

	zipPath := filepath.Join(req.WorkDir, testHash+".zip")
	if err := os.WriteFile(zipPath+".part", bytes.Repeat([]byte("X"), 900), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSidecar(zipPath+".meta.json", sidecar{
		SourceURL:     "https://other.example/cache/" + testHash + ".zip",
		ETag:          `"v1"`,
		ContentLength: 900,
	})

	if _, err := fetchZip(context.Background(), req, testHash); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(zipPath); !bytes.Equal(got, body) {
		t.Fatalf("旧 .part 没被丢弃，得到 %d 字节", len(got))
	}
	cdn.mu.Lock()
	defer cdn.mu.Unlock()
	if len(cdn.rangeStarts) != 0 {
		t.Fatalf("不该发 Range 请求: %v", cdn.rangeStarts)
	}

	var s sidecar
	data, _ := os.ReadFile(zipPath + ".meta.json")
	json.Unmarshal(data, &s)
	if s.ETag != `"v2"` {
		t.Fatalf("旁车没被刷新: %+v", s)
	}
}

func TestFetchZipContentLengthMismatchFails(t *testing.T) {
	body := bytes.Repeat([]byte("S"), 1024)
	cdn := newCDN(t, body, "LM", cdnOpts{headSize: 4096})
	req := fetchRequest(t, cdn.prefix())

	if _, err := fetchZip(context.Background(), req, testHash); err == nil {
		t.Fatal("大小不符时应当失败")
	}
}

func TestFetchZipFallsBackToNextCDN(t *testing.T) {
	body := bytes.Repeat([]byte("F"), 2048)
	dead := newCDN(t, nil, "", cdnOpts{notFound: true})
	live := newCDN(t, body, "SECONDARY-LM", cdnOpts{})
	req := fetchRequest(t, dead.prefix(), live.prefix())

	out, err := fetchZip(context.Background(), req, testHash)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.sourceURL, live.URL) {
		t.Fatalf("应当从备用 CDN 下载，实际 %s", out.sourceURL)
	}
	// 主 CDN 连 HEAD 都不通 → 只能退用实际下载源的值。
	if out.lastModified != "SECONDARY-LM" {
		t.Fatalf("lastModified = %q", out.lastModified)
	}
}

// §4.5 第 2 条：从备用 CDN 下载后，last_modified 必须回头取**主 CDN** 的值 ——
// ArkApi 的 HEAD 只问第一个，写错就等于每次启动都被它整包重下。
func TestFetchZipTakesLastModifiedFromPrimaryCDN(t *testing.T) {
	body := bytes.Repeat([]byte("P"), 2048)
	// 主 CDN 的 HEAD 通、GET 不通（体积对不上）：HEAD 拿得到 Last-Modified，
	// 但下载得从第二个源来。
	primary := newCDN(t, body[:10], "PRIMARY-LM", cdnOpts{headSize: 999999})
	secondary := newCDN(t, body, "SECONDARY-LM", cdnOpts{})
	req := fetchRequest(t, primary.prefix(), secondary.prefix())

	out, err := fetchZip(context.Background(), req, testHash)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.sourceURL, secondary.URL) {
		t.Fatalf("应当从备用 CDN 下载，实际 %s", out.sourceURL)
	}
	if out.lastModified != "PRIMARY-LM" {
		t.Fatalf("lastModified = %q，必须是主 CDN 的值", out.lastModified)
	}
}

func TestFetchZipRejectsOversize(t *testing.T) {
	cdn := newCDN(t, bytes.Repeat([]byte("O"), 64), "LM", cdnOpts{headSize: defaultMaxSize + 1})
	req := fetchRequest(t, cdn.prefix())
	req.MaxSize = defaultMaxSize

	if _, err := fetchZip(context.Background(), req, testHash); err == nil {
		t.Fatal("超过上限时应当拒绝")
	}
	cdn.mu.Lock()
	defer cdn.mu.Unlock()
	if cdn.gets != 0 {
		t.Fatal("超限时不该发起 GET")
	}
}
