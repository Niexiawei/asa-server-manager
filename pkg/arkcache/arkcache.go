// Package arkcache 在 AsaApiLoader.exe 启动**之前**，把 ArkApi 需要的 offsets
// cache 下好、解压好、按 ArkApi 认得的格式提交好，让加载器启动时直接采用本地缓存。
//
// 为什么要接管这件事（docs/ARKAPI_CACHE_PREFETCH_PLAN.md §1）：那次下载由 ArkApi
// 的 C++ 代码发起，读不到本程序的 download.http_proxy / download.retries，没有任何
// 断点续传（std::ios::trunc 全量重写），而且压着一个 **10 分钟的总死线** ——
// 平均速率低到 10 分钟下不完的链路，ArkApi 永远下不完这个包，重试多少次都一样。
// 本包的 .part 续传没有这个天花板，这是质变而不只是省时间。
//
// 硬约束：**这是加速，不是新的失败点。** Prepare 永远不向上抛致命错误，任何一步
// 失败都无声降级回今天的行为（ArkApi 自己去下），绝不能让一台原本能启动的机器
// 启动不了。
//
// 依赖上是叶子包：只用标准库与 pkg/download。它不认识实例、镜像、
// BaseDir 里的任何一个 —— 路径由调用方注入，包本身不去猜。
package arkcache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Result.From 的取值。
const (
	FromExisting = "existing"
	FromDownload = "download"
	// FromRefresh：本地缓存对当前 exe **是**有效的，但 CDN 上那个包的
	// Last-Modified 变了，于是重下了一份 —— ArkApi 会拿它那次 HEAD 的结果与
	// cached_key.cache 逐字比对，不提前发现就等于预取白做。
	FromRefresh = "refresh"
)

// Request 是一次预取的全部输入。调用方给什么，包就用什么。
type Request struct {
	// ExePath 是 ArkAscendedServer.exe —— **源目录（server-files）里那份**。
	// 镜像里的 exe 是它的字节副本，哈希必然相同，所以一台机器只算一次。
	ExePath string
	// CacheRoot 是 <exe 目录>/ArkApi/Cache。
	CacheRoot string
	// WorkDir 是下载中转目录（.part / .meta.json / .lock），**不能**落在
	// CacheRoot 之下：镜像同步会把半成品复制进每个实例。
	WorkDir string
	// URLs 是 CDN 前缀列表，按序回退。空 = DefaultURLs。第一个必须与 ArkApi
	// 实际优先查询的那个一致，理由见 DefaultURLs 的注释。
	URLs []string
	// MaxSize 是下载体与解压总量的上限。0 = 768 MiB，与 C++ 侧一致。
	MaxSize int64
	// Keep 是源目录里除当前 generation 外额外保留几代同哈希的旧代。默认 0。
	Keep int
	// Progress 是下载进度回调，可为 nil。
	Progress func(done, total int64)
}

// Result 是一次 Prepare / Inspect 的结论。
type Result struct {
	// Ready 表示 CacheRoot 下现在是一份对当前 exe 有效的缓存。
	Ready bool
	// Hash 是 exe 的 SHA256（小写十六进制）。
	Hash string
	// Generation 是相对 CacheRoot 的 "generations/<name>"；历史格式（缓存直接
	// 落在 Cache 根）下为空。
	Generation string
	// From 是 FromExisting 或 FromDownload。
	From string
	// LastModified 是 metadata 里那个逐字比对用的值（方案 §4.5）。
	LastModified string
	// Reason 是 Ready=false 时的人话原因。
	Reason string
}

func (r Request) withDefaults() Request {
	if len(r.URLs) == 0 {
		r.URLs = DefaultURLs
	}
	if r.MaxSize <= 0 {
		r.MaxSize = defaultMaxSize
	}
	if r.Keep < 0 {
		r.Keep = 0
	}
	if r.WorkDir == "" {
		r.WorkDir = filepath.Join(os.TempDir(), "arkapi-cache")
	}
	return r
}

// hashMutexes 让同机多实例并发启动时，同一个哈希只有一个 goroutine 真的去下，
// 其余等它完成后走 Inspect 的快路径。
var hashMutexes sync.Map

func hashMutex(hash string) *sync.Mutex {
	v, _ := hashMutexes.LoadOrStore(hash, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Prepare 幂等：已经有效就直接返回，不联网、不写盘。
//
// 提交顺序是硬规则（ArkBaseApi.cpp:536-556）：generation 完全就绪 → 两个 .cache
// 通过结构校验 → **最后**才原子写 metadata。任何一步失败都**不动**已有的
// cached_key.cache —— 宁可让 ArkApi 用一份旧的（它自己会判哈希不匹配再去下），
// 也不能出现「metadata 指向的 generation 不存在」，那是 C++ 侧最难诊断的形态。
func Prepare(ctx context.Context, req Request) Result {
	req = req.withDefaults()

	if req.ExePath == "" || req.CacheRoot == "" {
		return Result{Reason: "未提供 exe 路径或缓存根目录"}
	}

	hash, err := ExeHash(req.ExePath)
	if err != nil {
		return Result{Reason: fmt.Sprintf("读不到 %s: %v", req.ExePath, err)}
	}

	mu := hashMutex(hash)
	mu.Lock()
	defer mu.Unlock()

	existing, _ := Inspect(req.CacheRoot, hash)

	// wantLM 是主 CDN 此刻的 Last-Modified，也就是 ArkApi 待会儿会拿来比对的那个值。
	// 空字符串表示「没查到」，此时一律接受现有缓存（见 acceptable）。
	//
	// 为什么只比 exe 哈希不够（这正是「缓存过期」的真正含义）：ArkApi 判定本地缓存
	// 可用有**两个**条件（ArkBaseApi.cpp:371-390），哈希只是第一个；第二个是 HEAD
	// 回来的 Last-Modified 与 cached_key.cache 里的**逐字相等**。CDN 为同一个 exe
	// 版本重发一次包，哈希不变、Last-Modified 变了 —— 只看哈希就会报「已就绪」，
	// 而 ArkApi 一比对就整包重下，预取完全白做（方案 §2.3 承重件 ②）。
	//
	// 这一步没有开关：ArkApi 的自动下载**关不掉**（它在没有 config.json 的情况下
	// 照常运行，那个开关我们够不着，见方案 §22），所以它那次 HEAD 一定会发生，
	// 我们这次提前比对也就一定有意义。
	var wantLM string
	if existing.Ready {
		wantLM = PrimaryLastModified(ctx, req, hash)
	}
	if acceptable(existing, wantLM) {
		return existing
	}
	if ctx.Err() != nil {
		return Result{Hash: hash, Reason: "已取消"}
	}

	release, err := acquireFileLock(ctx, req.WorkDir, hash)
	if err != nil {
		return Result{Hash: hash, Reason: fmt.Sprintf("拿不到预取锁: %v", err)}
	}
	defer release()

	// 拿到进程间锁之后再看一眼：等锁期间别的进程可能已经把缓存备好了。
	// 仍然要过 acceptable —— 别人备的那份也可能是过期的那一版。
	if res, _ := Inspect(req.CacheRoot, hash); acceptable(res, wantLM) {
		return res
	}

	out, err := fetchZip(ctx, req, hash)
	if err != nil {
		return Result{Hash: hash, Reason: fmt.Sprintf("下载失败: %v", err)}
	}

	genName := newGenerationName(hash, 0)
	genRel := generationRelPath(genName)
	genDir := filepath.Join(req.CacheRoot, generationsRel, genName)

	if err := extractCacheZip(out.zipPath, genDir, req.MaxSize); err != nil {
		os.RemoveAll(genDir)
		if errors.Is(err, errZipRejected) {
			// 这个 ZIP 本身有问题，续传它没有意义。
			os.Remove(out.zipPath)
			os.Remove(out.zipPath + ".part")
		}
		return Result{Hash: hash, Reason: fmt.Sprintf("解压失败: %v", err)}
	}

	for name, valueSize := range map[string]int{
		offsetsFileName:   offsetValueSize,
		bitfieldsFileName: bitfieldValueSize,
	} {
		if err := validateSerializedMap(filepath.Join(genDir, name), valueSize); err != nil {
			os.RemoveAll(genDir)
			os.Remove(out.zipPath)
			os.Remove(out.zipPath + ".part")
			return Result{Hash: hash, Reason: fmt.Sprintf("结构校验不通过: %v", err)}
		}
	}

	if err := writeMetadata(req.CacheRoot, metadata{
		Version:        cacheMetadataVersion,
		ExecutableHash: hash,
		LastModified:   out.lastModified,
		CacheDirectory: genRel,
	}); err != nil {
		os.RemoveAll(genDir)
		return Result{Hash: hash, Reason: fmt.Sprintf("写 cached_key.cache 失败: %v", err)}
	}

	// 成品已提交，几百 MB 的中转 ZIP 没有留着的理由。
	os.Remove(out.zipPath)
	pruneGenerations(req.CacheRoot, hash, genRel, req.Keep, false)

	from := FromDownload
	if existing.Ready {
		// 本来就有一份对当前 exe 有效的缓存，是 Last-Modified 变了才重下的。
		from = FromRefresh
	}
	return Result{
		Ready:        true,
		Hash:         hash,
		Generation:   genRel,
		From:         from,
		LastModified: out.lastModified,
	}
}

// acceptable 判断一份现有缓存能不能直接用。
//
// wantLM 为空（HEAD 没成功）时只看 Ready —— **查不动就用现成的**，
// 这条兜底是硬规则：断网的机器不能因为问不到 CDN 就把自己手里那份好缓存判死，
// 那会把一次加速变成一次新的失败。
func acceptable(res Result, wantLM string) bool {
	return res.Ready && (wantLM == "" || res.LastModified == wantLM)
}

// GC 删除非当前哈希的 generation 与陈旧的中转物。dryRun=true 只报不删。
func GC(req Request, dryRun bool) ([]string, error) {
	req = req.withDefaults()

	hash, err := ExeHash(req.ExePath)
	if err != nil {
		return nil, fmt.Errorf("读不到 %s: %w", req.ExePath, err)
	}

	current, _ := Inspect(req.CacheRoot, hash)
	removed := pruneGenerations(req.CacheRoot, hash, current.Generation, req.Keep, dryRun)

	entries, err := os.ReadDir(req.WorkDir)
	if err != nil {
		return removed, nil // 中转目录还没建过，不是错误
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// 属于当前 exe 的中转物只在缓存已就绪时才算陈旧（否则 .part 还要续传）。
		if strings.HasPrefix(name, hash) && !current.Ready {
			continue
		}
		if dryRun {
			removed = append(removed, filepath.Join(req.WorkDir, name))
			continue
		}
		if os.Remove(filepath.Join(req.WorkDir, name)) == nil {
			removed = append(removed, filepath.Join(req.WorkDir, name))
		}
	}
	return removed, nil
}

// 进程间锁：用户手动跑 `asa-server arkapi-cache fetch` 时后台服务可能也在跑。
// O_CREATE|O_EXCL 建一个 <hash>.lock，内写 PID 与时间戳，超过 staleLockAge 视为
// 陈旧（持锁进程崩了）并强夺。不引入新依赖。
const (
	staleLockAge  = 30 * time.Minute
	lockWaitLimit = 60 * time.Second
	lockPollEvery = 2 * time.Second
)

func acquireFileLock(ctx context.Context, dir, hash string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, hash+".lock")
	deadline := time.Now().Add(lockWaitLimit)

	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d %d\n", os.Getpid(), time.Now().Unix())
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if lockIsStale(path) {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("等待 %s 超时（另一个进程正在预取）", path)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPollEvery):
		}
	}
}

func lockIsStale(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return true // 内容不成形，当陈旧处理
	}
	sec, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return true
	}
	return time.Since(time.Unix(sec, 0)) > staleLockAge
}
