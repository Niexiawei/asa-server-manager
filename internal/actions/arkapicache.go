package actions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"asa-server/internal/appconfig"
	"asa-server/internal/installer"
	"asa-server/internal/instance"
	"asa-server/internal/mirror"
	"asa-server/pkg/arkcache"

	"github.com/urfave/cli/v3"
)

// ArkApiCacheCommand 管理 ArkApi 的 offsets cache 预取，见
// docs/ARKAPI_CACHE_PREFETCH_PLAN.md。
//
// 这条命令是排障入口：启动路径上的预取是静默的（失败只写日志、不阻断），所以
// 「缓存到底备好了没、镜像里那份是不是最新的」需要一个能主动去问的地方。
func ArkApiCacheCommand() *cli.Command {
	return &cli.Command{
		Name:  "arkapi-cache",
		Usage: "查看/预取/清理 ArkApi 的 offsets 缓存",
		Commands: []*cli.Command{
			{
				Name:   "status",
				Usage:  "只读列出当前 exe 哈希、源缓存是否有效、各实例镜像里的 generation",
				Action: actionArkApiCacheStatus,
			},
			{
				Name:  "fetch",
				Usage: "立刻为当前 server-files 的 exe 预取缓存",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "force",
						Usage: "已有有效缓存时也重新下载",
					},
				},
				Action: actionArkApiCacheFetch,
			},
			{
				Name:  "gc",
				Usage: "清理非当前版本的 generation 与陈旧的下载中转物（默认只预演，加 --apply 才真删）",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "apply",
						Usage: "真正执行删除；不加时只打印将要删除的内容",
					},
				},
				Action: actionArkApiCacheGC,
			},
		},
	}
}

func actionArkApiCacheStatus(ctx context.Context, cmd *cli.Command) error {
	if !installer.ArkApiInstalled() {
		fmt.Println("server-files 里没有安装 ArkApi（找不到 AsaApiLoader.exe），本功能不参与启动流程。")
		return nil
	}

	req := instance.ArkApiCacheRequest()
	hash, err := arkcache.ExeHash(req.ExePath)
	if err != nil {
		return fmt.Errorf("读不到 %s: %w", req.ExePath, err)
	}

	cfg := appconfig.Get().ArkApiCache
	fmt.Printf("配置：       enabled=%v  keep_generations=%d\n", cfg.Enabled, cfg.KeepGenerations)
	fmt.Printf("CDN：        %v\n", requestURLs(req))
	fmt.Printf("当前 exe：   %s\n", req.ExePath)
	fmt.Printf("exe SHA256： %s\n", hash)

	res, _ := arkcache.Inspect(req.CacheRoot, hash)
	fmt.Printf("\n源缓存（%s）\n", req.CacheRoot)
	printCacheResult(res)
	if res.Ready {
		printFreshness(ctx, req, hash, res)
	}

	instances := existingInstances()
	if len(instances) == 0 {
		return nil
	}
	names := make([]string, 0, len(instances))
	for name := range instances {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Println("\n各实例镜像")
	for _, name := range names {
		cacheRoot := filepath.Join(instance.MirrorArkApiDir(mirror.InstanceMirrorDir(name)), "Cache")
		if _, err := os.Stat(cacheRoot); err != nil {
			fmt.Printf("  %-20s 镜像里还没有 ArkApi/Cache（实例可能从未启动）\n", name)
			continue
		}
		mres, _ := arkcache.Inspect(cacheRoot, hash)
		if mres.Ready {
			fmt.Printf("  %-20s 有效  %s\n", name, mres.Generation)
		} else {
			fmt.Printf("  %-20s 无效  %s\n", name, mres.Reason)
		}
	}
	return nil
}

func printCacheResult(res arkcache.Result) {
	if !res.Ready {
		fmt.Printf("  状态：无效（%s）\n", res.Reason)
		fmt.Println("  启动时 ArkApi 会自己去 CDN 下载。执行 asa-server arkapi-cache fetch 可以现在就备好。")
		return
	}
	fmt.Printf("  状态：有效\n")
	fmt.Printf("  generation：  %s\n", res.Generation)
	fmt.Printf("  last_modified：%q\n", res.LastModified)
	if res.LastModified == "" {
		fmt.Println("  ⚠ last_modified 为空：ArkApi 的 HEAD 一旦成功就必然判定不相等并整包重下。")
	}
}

// printFreshness 把「ArkApi 待会儿会不会判定这份缓存过期」直接问出来。
//
// 只比 exe 哈希是不够的：ArkApi 还会拿主 CDN 的 Last-Modified 与 cached_key.cache
// 里的那个逐字比对，不等就整包重下。status 是排障入口，而这一条恰恰是最容易踩、
// 又最难从现象反推的（方案 §2.3 承重件 ②）。
func printFreshness(ctx context.Context, req arkcache.Request, hash string, res arkcache.Result) {
	switch got := arkcache.PrimaryLastModified(ctx, req, hash); {
	case got == "":
		fmt.Println("  新鲜度：查不到（主 CDN 不可达）—— 启动时按「用现成的」处理，不会重下")
	case got == res.LastModified:
		fmt.Println("  新鲜度：与主 CDN 一致，ArkApi 会直接采用")
	default:
		fmt.Printf("  新鲜度：已过期，主 CDN 现在是 %q —— 下次启动会自动重新获取\n", got)
	}
}

func actionArkApiCacheFetch(ctx context.Context, cmd *cli.Command) error {
	if !installer.ArkApiInstalled() {
		return fmt.Errorf("server-files 里没有安装 ArkApi（找不到 AsaApiLoader.exe）")
	}

	req := instance.ArkApiCacheRequest()
	req.Progress = newConsoleProgress()

	// --force 只把指针文件挪开，不删 generation：Prepare 失败时还能原样放回去，
	// 「宁可让 ArkApi 用一份旧的，也不能留下指向空处的 metadata」这条不变式在
	// 手动路径上同样成立。
	var restore func()
	if cmd.Bool("force") {
		restore = stashMetadata(req.CacheRoot)
	}

	res := arkcache.Prepare(ctx, req)
	fmt.Println()
	if !res.Ready {
		if restore != nil {
			restore()
		}
		return fmt.Errorf("预取失败：%s", res.Reason)
	}
	fmt.Printf("预取完成（%s）\n", res.From)
	printCacheResult(res)
	fmt.Println("\n缓存备在源目录里，各实例的镜像会在下次启动同步时自动拿到。")
	return nil
}

// stashMetadata 把 cached_key.cache 改名藏起来，返回一个把它放回去的函数
// （成功提交新缓存后那份藏起来的会被 os.Remove 掉，因为 restore 只在失败时调用）。
func stashMetadata(cacheRoot string) func() {
	final := filepath.Join(cacheRoot, "cached_key.cache")
	stash := final + ".force-bak"
	if err := os.Rename(final, stash); err != nil {
		return func() {}
	}
	return func() {
		if err := os.Rename(stash, final); err == nil {
			fmt.Println("已恢复原有的缓存指针文件。")
		}
	}
}

func actionArkApiCacheGC(ctx context.Context, cmd *cli.Command) error {
	req := instance.ArkApiCacheRequest()
	apply := cmd.Bool("apply")

	removed, err := arkcache.GC(req, !apply)
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		fmt.Println("没有可回收的内容。")
		return nil
	}
	if !apply {
		fmt.Printf("以下 %d 项可回收：\n", len(removed))
		for _, r := range removed {
			fmt.Printf("  %s\n", r)
		}
		fmt.Println("\n这是预演，什么都没有删除。确认无误后执行：asa-server arkapi-cache gc --apply")
		return nil
	}
	fmt.Printf("已回收 %d 项：\n", len(removed))
	for _, r := range removed {
		fmt.Printf("  %s\n", r)
	}
	return nil
}

func requestURLs(req arkcache.Request) []string {
	if len(req.URLs) > 0 {
		return req.URLs
	}
	return arkcache.DefaultURLs
}

// newConsoleProgress 在 CLI 里就地刷一行进度。启动路径走的是 logger（见
// internal/instance/arkcache.go），两者刻意不共用：终端要的是覆盖式单行，
// 日志要的是节流后的多行。
func newConsoleProgress() func(done, total int64) {
	var lastPct int64 = -1
	return func(done, total int64) {
		var pct int64
		if total > 0 {
			pct = done * 100 / total
		}
		if pct == lastPct {
			return
		}
		lastPct = pct
		fmt.Printf("\r下载中 %3d%%  %.1f/%.1f MB", pct, float64(done)/(1<<20), float64(total)/(1<<20))
	}
}
