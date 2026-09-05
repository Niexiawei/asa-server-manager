package arkcache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// prepareWithCDN 起一个假 CDN，跑一次 Prepare 把缓存备好，返回环境与 CDN。
func prepareWithCDN(t *testing.T, lastModified string) (prepareEnv, *fakeCDN) {
	t.Helper()
	cdn := newCDN(t, zipBytes(t, goodEntries(t)...), lastModified, cdnOpts{})
	env := newPrepareEnv(t, cdn.prefix())

	if res := Prepare(context.Background(), env.req); !res.Ready {
		t.Fatalf("初次预取失败: %s", res.Reason)
	}
	return env, cdn
}

// 只比 exe 哈希是不够的：CDN 为**同一个** exe 版本重发一次包（哈希不变、
// Last-Modified 变了），ArkApi 的 HEAD 一比对就整包重下。快路径必须自己先发现。
func TestPrepareRefetchesWhenLastModifiedChanged(t *testing.T) {
	env, cdn := prepareWithCDN(t, "Tue, 25 Aug 2026 18:20:41 GMT")
	getsAfterFirst := cdn.gets

	// CDN 换了一版：内容不同、Last-Modified 也不同，exe 哈希原样不变。
	newBody := zipBytes(t, []zipEntry{
		{offsetsFileName, buildRecords(t, offsetValueSize, "AShooterGameMode.BeginPlay", "UWorld.Tick")},
		{bitfieldsFileName, buildRecords(t, bitfieldValueSize, "bIsDead", "bReplicates")},
	}...)
	fresh := newCDN(t, newBody, "Wed, 02 Sep 2026 09:00:00 GMT", cdnOpts{})
	env.req.URLs = []string{fresh.prefix()}

	res := Prepare(context.Background(), env.req)
	if !res.Ready {
		t.Fatalf("应当重新获取成功: %s", res.Reason)
	}
	if res.From != FromRefresh {
		t.Fatalf("From = %q，want %q（哈希没变、只是过期）", res.From, FromRefresh)
	}
	if res.LastModified != "Wed, 02 Sep 2026 09:00:00 GMT" {
		t.Fatalf("metadata 里的 last_modified 没更新: %q", res.LastModified)
	}
	if fresh.gets != 1 {
		t.Fatalf("新 CDN 的 GET 次数 = %d，want 1", fresh.gets)
	}
	if cdn.gets != getsAfterFirst {
		t.Fatal("不该再去问旧 CDN 要内容")
	}

	// 落盘的那份也必须换掉，而不是只改了内存里的 Result。
	got, err := Inspect(env.req.CacheRoot, env.hash)
	if err != nil || !got.Ready {
		t.Fatalf("重下之后缓存不可用: %v %s", err, got.Reason)
	}
	if got.LastModified != "Wed, 02 Sep 2026 09:00:00 GMT" {
		t.Fatalf("cached_key.cache 里的 last_modified 没写新的: %q", got.LastModified)
	}
	if got.Generation == res.Generation && res.Generation == "" {
		t.Fatal("generation 没记进 metadata")
	}
	// 旧代必须被清掉（Keep=0）。
	if names := listGenerations(env.req.CacheRoot); len(names) != 1 {
		t.Fatalf("旧 generation 没清: %v", names)
	}
}

// Last-Modified 没变就走快路径：一次 HEAD，零 GET。
func TestPrepareKeepsCacheWhenLastModifiedUnchanged(t *testing.T) {
	env, cdn := prepareWithCDN(t, "Tue, 25 Aug 2026 18:20:41 GMT")
	before := cdn.gets

	res := Prepare(context.Background(), env.req)
	if !res.Ready || res.From != FromExisting {
		t.Fatalf("应当直接采用已有缓存: %+v", res)
	}
	if cdn.gets != before {
		t.Fatal("Last-Modified 没变却重下了")
	}
}

// **这条是硬规则**：HEAD 问不到（断网、CDN 挂了、被墙）绝不能把手里那份好缓存
// 判死。判死的后果是白下一遍，下不成还会让一台本来能起的机器起不来 ——
// 这是加速，不是新的失败点。
func TestPrepareKeepsCacheWhenProbeFails(t *testing.T) {
	env, _ := prepareWithCDN(t, "Tue, 25 Aug 2026 18:20:41 GMT")

	dead := newCDN(t, nil, "", cdnOpts{notFound: true})
	env.req.URLs = []string{dead.prefix()}

	res := Prepare(context.Background(), env.req)
	if !res.Ready || res.From != FromExisting {
		t.Fatalf("主 CDN 不可达时应当沿用本地缓存: %+v", res)
	}
	if dead.gets != 0 {
		t.Fatal("不该发起任何下载")
	}
}

// 缓存本来就无效（典型是 ARK 更新换了 exe）时，重新校验不该改变任何行为：
// 照常下载，From 仍是 download 而不是 refresh。
func TestPrepareInvalidCacheStillReportsDownload(t *testing.T) {
	cdn := newCDN(t, zipBytes(t, goodEntries(t)...), "LM", cdnOpts{})
	env := newPrepareEnv(t, cdn.prefix())

	res := Prepare(context.Background(), env.req)
	if !res.Ready || res.From != FromDownload {
		t.Fatalf("From = %q，want %q", res.From, FromDownload)
	}
}

// 重新获取失败时，**不能**把已有的那份弄坏 —— 与 §4.4 的硬规则同一条。
func TestPrepareRefreshFailureKeepsOldCache(t *testing.T) {
	env, _ := prepareWithCDN(t, "Tue, 25 Aug 2026 18:20:41 GMT")
	before, err := os.ReadFile(filepath.Join(env.req.CacheRoot, metadataFileName))
	if err != nil {
		t.Fatal(err)
	}

	// HEAD 报了一个新的 Last-Modified（判定过期），但包本身是坏的。
	broken := newCDN(t, zipBytes(t, zipEntry{"evil.dll", []byte("x")}, goodEntries(t)[0], goodEntries(t)[1]),
		"Wed, 02 Sep 2026 09:00:00 GMT", cdnOpts{})
	env.req.URLs = []string{broken.prefix()}

	if res := Prepare(context.Background(), env.req); res.Ready {
		t.Fatal("坏包不该被接受")
	}
	after, err := os.ReadFile(filepath.Join(env.req.CacheRoot, metadataFileName))
	if err != nil || string(before) != string(after) {
		t.Fatal("重新获取失败时动了已有的 cached_key.cache")
	}
	if got, err := Inspect(env.req.CacheRoot, env.hash); err != nil || !got.Ready {
		t.Fatalf("原有缓存被弄坏了: %v %s", err, got.Reason)
	}
}
