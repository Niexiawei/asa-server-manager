package instance

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"asa-server/internal/appconfig"
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/installer"
	"asa-server/pkg/arkcache"
	"asa-server/pkg/logger"
)

// 这是 pkg/arkcache 的调用侧适配器：把领域知识（ServerFilesDir / BaseDir /
// appconfig）翻译成 arkcache.Request，再把进度接到 logger。算法留在 pkg，
// 领域知识留在 internal —— 见 docs/ARKAPI_CACHE_PREFETCH_PLAN.md §5。

const (
	// win64RelPath 与 mirror 包里的同名常量保持一致；这里只用来定位源目录里的
	// exe 与 ArkApi 目录，不参与镜像逻辑。
	win64RelPath = "ShooterGame/Binaries/Win64"
	// arkApiCacheWorkDirName 是下载中转目录，落在 BaseDir 下而**不是** Cache 里：
	// 放在 Cache 里，镜像同步会把一个下了一半的 .part 当成源里的新文件复制进每个
	// 实例（方案 §4.2）。
	arkApiCacheWorkDirName = "arkapi-cache"
)

// SourceArkExePath 是 server-files 里 ArkAscendedServer.exe 的绝对路径。
func SourceArkExePath() string {
	return filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64RelPath), arkExeName)
}

// SourceArkApiDir 是 server-files 里的 ArkApi 目录。
func SourceArkApiDir() string {
	return filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64RelPath), "ArkApi")
}

// MirrorArkApiDir 是某个实例镜像里的 ArkApi 目录。
func MirrorArkApiDir(mirrorDir string) string {
	return filepath.Join(mirrorDir, filepath.FromSlash(win64RelPath), "ArkApi")
}

// ArkApiCacheRequest 按当前配置拼出预取请求。Progress 留给调用方接。
func ArkApiCacheRequest() arkcache.Request {
	cfg := appconfig.Get().ArkApiCache
	return arkcache.Request{
		ExePath:   SourceArkExePath(),
		CacheRoot: filepath.Join(SourceArkApiDir(), "Cache"),
		WorkDir:   filepath.Join(cfgpkg.BaseDir, arkApiCacheWorkDirName),
		URLs:      cfg.URLs,
		MaxSize:   cfg.MaxSize,
		Keep:      cfg.KeepGenerations,
	}
}

// PrepareArkApiCache 在启动链上把 ArkApi 的 offsets cache 备进**源目录**，
// 随后的镜像同步会把它分发进本实例的镜像 —— 分发不是新代码，是 mirror 的既有职责。
//
// 它**永远不向上抛致命错误**：任何一步失败都只记一条 Warn，启动照常继续，ArkApi
// 会像今天一样自己去下（方案 §4.4）。返回值只用来决定要不要写那个可选的
// AutomaticCacheDownload.Enable 开关。
func PrepareArkApiCache(ctx context.Context) arkcache.Result {
	if !appconfig.Get().ArkApiCache.Enabled {
		return arkcache.Result{Reason: "arkapi_cache.enabled = false"}
	}
	if !installer.ArkApiInstalled() {
		return arkcache.Result{Reason: "源目录里没有装 ArkApi"}
	}

	req := ArkApiCacheRequest()
	req.Progress = newCacheProgress(func(msg string) { logger.Info(msg) })

	res := arkcache.Prepare(ctx, req)
	switch {
	case res.Ready && res.From == arkcache.FromExisting:
		logger.Infof("ArkApi offsets cache 已就绪（exe %s..，%s）", shortHash(res.Hash), res.Generation)
	case res.Ready && res.From == arkcache.FromRefresh:
		// 哈希没变、CDN 上的包换了版本。不重下的话 ArkApi 的 HEAD 会判定不相等，
		// 转头自己整包重下 —— 预取白做，所以这条值得单独说清楚。
		logger.Infof("ArkApi offsets cache 已过期（CDN 上的 Last-Modified 变了），已重新获取（%s）", res.Generation)
	case res.Ready:
		logger.Infof("ArkApi offsets cache 预取完成（exe %s..，%s）", shortHash(res.Hash), res.Generation)
	default:
		logger.Warnf("ArkApi offsets cache 预取未完成，本次启动交由 ArkApi 自行下载：%s", res.Reason)
	}
	return res
}

// PrefetchArkApiCacheAfterUpdate 在 ARK 本体更新完成后立刻预取。
//
// 更新必然换掉 ArkAscendedServer.exe，也就必然换掉缓存的哈希 —— 不在这里补上，
// 那笔下载就会挪到「更新后第一次启动」，而那时用户面对的是一个看起来卡住的实例。
// 更新流程本来就有进度通道、用户本来就在等，是最合适的落点（方案 §4.3）。
//
// 和启动路径一样永不致命：失败只写一行，更新照样算成功。w 可以为 nil。
func PrefetchArkApiCacheAfterUpdate(ctx context.Context, w io.Writer) {
	if !appconfig.Get().ArkApiCache.Enabled || !installer.ArkApiInstalled() {
		return
	}

	emit := func(msg string) {
		logger.Info(msg)
		if w != nil {
			_, _ = w.Write([]byte(msg + "\n"))
		}
	}

	req := ArkApiCacheRequest()
	req.Progress = newCacheProgress(emit)

	if res := arkcache.Prepare(ctx, req); res.Ready {
		emit(fmt.Sprintf("ArkApi offsets cache 已就绪（%s）", res.Generation))
	} else {
		emit(fmt.Sprintf("ArkApi offsets cache 预取未完成，首次启动会由 ArkApi 自行下载：%s", res.Reason))
	}
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// newCacheProgress 把下载进度节流后交给 emit。启动路径把它接到 logger —— 系统日志流
// GET /api/logs 是 SSE，前端「系统日志」面板能实时看到，不需要为这一个下载新开推送
// 通道；更新路径再额外抄一份进更新自己的 SSE。
func newCacheProgress(emit func(msg string)) func(done, total int64) {
	var (
		mu       sync.Mutex
		lastAt   time.Time
		lastPct  int64
		announce bool
	)
	return func(done, total int64) {
		mu.Lock()
		defer mu.Unlock()

		if !announce {
			announce = true
			emit(fmt.Sprintf("正在预取 ArkApi offsets cache（%.1f MB）...", float64(total)/(1<<20)))
		}
		var pct int64
		if total > 0 {
			pct = done * 100 / total
		}
		if time.Since(lastAt) < 3*time.Second && pct-lastPct < 5 && done != total {
			return
		}
		lastAt = time.Now()
		lastPct = pct
		emit(fmt.Sprintf("ArkApi offsets cache 下载中 %d%%（%.1f/%.1f MB）", pct, float64(done)/(1<<20), float64(total)/(1<<20)))
	}
}
