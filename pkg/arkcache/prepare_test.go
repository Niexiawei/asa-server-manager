package arkcache

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipBytes 把 goodEntries 打成内存里的 ZIP，直接当 CDN 的响应体。
func zipBytes(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// prepareEnv 造一个「源目录」的最小形态：exe + Cache 根 + 中转目录。
type prepareEnv struct {
	req  Request
	hash string
}

func newPrepareEnv(t *testing.T, urls ...string) prepareEnv {
	t.Helper()
	base := t.TempDir()
	win64 := filepath.Join(base, "server-files", "ShooterGame", "Binaries", "Win64")
	if err := os.MkdirAll(win64, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(win64, "ArkAscendedServer.exe")
	if err := os.WriteFile(exe, []byte("pretend this is 700MB of ARK"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := ExeHash(exe)
	if err != nil {
		t.Fatal(err)
	}
	return prepareEnv{
		req: Request{
			ExePath:   exe,
			CacheRoot: filepath.Join(win64, "ArkApi", "Cache"),
			WorkDir:   filepath.Join(base, "arkapi-cache"),
			URLs:      urls,
		},
		hash: hash,
	}
}

func TestPrepareEndToEnd(t *testing.T) {
	body := zipBytes(t, goodEntries(t)...)
	cdn := newCDN(t, body, "Thu, 03 Sep 2026 15:20:10 GMT", cdnOpts{})
	env := newPrepareEnv(t, cdn.prefix())

	res := Prepare(context.Background(), env.req)
	if !res.Ready {
		t.Fatalf("应当就绪: %s", res.Reason)
	}
	if res.From != FromDownload {
		t.Fatalf("From = %q", res.From)
	}
	if res.Hash != env.hash {
		t.Fatalf("Hash = %q", res.Hash)
	}
	if !isSafeGenerationDirectory(res.Generation) {
		t.Fatalf("generation 路径不合规: %s", res.Generation)
	}

	genDir := filepath.Join(env.req.CacheRoot, filepath.FromSlash(res.Generation))
	for _, name := range []string{offsetsFileName, bitfieldsFileName} {
		if _, err := os.Stat(filepath.Join(genDir, name)); err != nil {
			t.Fatalf("%s 未落盘: %v", name, err)
		}
	}
	// 中转 ZIP 与锁都不该留在盘上，更不该出现在 Cache 根（那会被镜像同步复制进每个实例）。
	if _, err := os.Stat(filepath.Join(env.req.WorkDir, env.hash+".zip")); err == nil {
		t.Fatal("中转 ZIP 没被清掉")
	}
	if _, err := os.Stat(filepath.Join(env.req.WorkDir, env.hash+".lock")); err == nil {
		t.Fatal("锁文件没被释放")
	}

	// 幂等：第二次直接走快路径，不再发任何请求。
	before := cdn.gets
	again := Prepare(context.Background(), env.req)
	if !again.Ready || again.From != FromExisting {
		t.Fatalf("第二次应当命中已有缓存: %+v", again)
	}
	if cdn.gets != before {
		t.Fatal("快路径不该再下载")
	}
}

// 失败时**不动**已有的 cached_key.cache —— 宁可让 ArkApi 用一份旧的，
// 也不能留下「metadata 指向一个不存在的 generation」。
func TestPrepareFailureLeavesExistingMetadataAlone(t *testing.T) {
	// ZIP 里塞一个白名单外的名字 → 整包拒绝。
	body := zipBytes(t, append(goodEntries(t), zipEntry{"evil.dll", []byte("x")})...)
	cdn := newCDN(t, body, "LM", cdnOpts{})
	env := newPrepareEnv(t, cdn.prefix())

	staleHash := strings.Repeat("c", 64)
	staleRel := seedGeneration(t, env.req.CacheRoot, staleHash, "OLD-LM")
	before, err := os.ReadFile(filepath.Join(env.req.CacheRoot, metadataFileName))
	if err != nil {
		t.Fatal(err)
	}

	res := Prepare(context.Background(), env.req)
	if res.Ready {
		t.Fatal("非法 ZIP 不该被接受")
	}
	if res.Reason == "" {
		t.Fatal("失败必须给出人话原因")
	}

	after, err := os.ReadFile(filepath.Join(env.req.CacheRoot, metadataFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("失败路径动了已有的 cached_key.cache")
	}
	if _, err := os.Stat(filepath.Join(env.req.CacheRoot, filepath.FromSlash(staleRel))); err != nil {
		t.Fatal("失败路径删了已有的 generation")
	}
	// 半成品必须清干净：generations 下只该剩那一代旧的。
	if names := listGenerations(env.req.CacheRoot); len(names) != 1 {
		t.Fatalf("残留了半成品 generation: %v", names)
	}
}

func TestPrepareDegradesWhenAllCDNsFail(t *testing.T) {
	dead := newCDN(t, nil, "", cdnOpts{notFound: true})
	env := newPrepareEnv(t, dead.prefix())

	res := Prepare(context.Background(), env.req)
	if res.Ready {
		t.Fatal("全部 CDN 都拿不到时不该报就绪")
	}
	if res.Hash != env.hash {
		t.Fatalf("即使失败也应报出当前 exe 哈希，得到 %q", res.Hash)
	}
}

func TestPrepareCancelledContext(t *testing.T) {
	cdn := newCDN(t, zipBytes(t, goodEntries(t)...), "LM", cdnOpts{})
	env := newPrepareEnv(t, cdn.prefix())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if res := Prepare(ctx, env.req); res.Ready {
		t.Fatal("已取消的 ctx 不该继续预取")
	}
	if cdn.gets != 0 {
		t.Fatal("已取消的 ctx 不该发起下载")
	}
}

func TestPrepareMissingExe(t *testing.T) {
	env := newPrepareEnv(t)
	os.Remove(env.req.ExePath)

	res := Prepare(context.Background(), Request{
		ExePath:   env.req.ExePath,
		CacheRoot: env.req.CacheRoot,
		WorkDir:   env.req.WorkDir,
	})
	if res.Ready || res.Reason == "" {
		t.Fatalf("exe 不存在时应当带原因失败: %+v", res)
	}
}

func TestGC(t *testing.T) {
	body := zipBytes(t, goodEntries(t)...)
	cdn := newCDN(t, body, "LM", cdnOpts{})
	env := newPrepareEnv(t, cdn.prefix())

	res := Prepare(context.Background(), env.req)
	if !res.Ready {
		t.Fatalf("预取失败: %s", res.Reason)
	}

	// 一代别的哈希留下的旧 generation + 一个别的哈希的陈旧 .part
	otherHash := strings.Repeat("d", 64)
	staleGen := generationRelPath(newGenerationName(otherHash, 0))
	if err := os.MkdirAll(filepath.Join(env.req.CacheRoot, filepath.FromSlash(staleGen)), 0o755); err != nil {
		t.Fatal(err)
	}
	stalePart := filepath.Join(env.req.WorkDir, otherHash+".zip.part")
	if err := os.WriteFile(stalePart, []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}

	planned, err := GC(env.req, true)
	if err != nil {
		t.Fatal(err)
	}
	// 旧 generation、陈旧 .part，以及当前哈希那份已经没用的旁车（ZIP 早被清掉了）。
	if len(planned) != 3 {
		t.Fatalf("预演结果 = %v，want 3 项", planned)
	}
	if _, err := os.Stat(stalePart); err != nil {
		t.Fatal("dryRun 不该真删")
	}

	if _, err := GC(env.req, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stalePart); err == nil {
		t.Fatal("陈旧 .part 应当被删")
	}
	if _, err := os.Stat(filepath.Join(env.req.CacheRoot, filepath.FromSlash(staleGen))); err == nil {
		t.Fatal("非当前哈希的 generation 应当被删")
	}
	// 当前缓存必须毫发无伤。
	if got, err := Inspect(env.req.CacheRoot, env.hash); err != nil || !got.Ready {
		t.Fatalf("GC 把当前缓存弄坏了: %v %s", err, got.Reason)
	}
}
