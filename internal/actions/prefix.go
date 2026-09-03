package actions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/runner"

	"github.com/urfave/cli/v3"
)

// PrefixCommand 管理 Linux 上的 Wine 前缀目录。
//
// 三种 `linux.prefix_mode` 留下三种形态：`shared` 只有一个
// `{BaseDir}/umu-prefix`；`per-instance` 每实例一个 `umu-prefix-<实例名>`；
// `overlay` 每实例一个 `umu-prefix-overlay/<实例名>/`（可写层）。但清理必须与
// 当前模式无关 —— 换过模式的用户盘上会同时留着好几种，除了这条命令没有别的
// 东西会报告它们。
//
// `reconcilePrefixVersion` 在 Proton 版本变化时留下的 `umu-prefix.bak-<版本>`
// 同样归这里管：它们同样占盘，同样没人会主动去看。
func PrefixCommand() *cli.Command {
	return &cli.Command{
		Name:  "prefix",
		Usage: "查看/清理 Wine 前缀目录（Linux）",
		Commands: []*cli.Command{
			{
				Name:   "status",
				Usage:  "只读列出所有 Wine 前缀：归属实例、Proton 版本、占用空间、是否正在被使用",
				Action: actionPrefixStatus,
			},
			{
				Name:  "gc",
				Usage: "删除没有对应实例、且当前没有 wineserver 占用的前缀（默认只预演，加 --apply 才真删）",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "apply",
						Usage: "真正执行删除；不加时只打印将要删除的内容",
					},
				},
				Action: actionPrefixGC,
			},
		},
	}
}

func actionPrefixStatus(ctx context.Context, cmd *cli.Command) error {
	if runtime.GOOS != "linux" {
		fmt.Println("Windows 上没有 Wine 前缀：ARK 服务端直接以原生进程运行。")
		return nil
	}

	prefixes := runner.PrefixStatus()
	if len(prefixes) == 0 {
		fmt.Println("尚未创建任何 Wine 前缀。执行 asa-server setup 完成环境准备。")
		return nil
	}

	instances := existingInstances()
	var (
		total      int64
		hasOverlay bool
	)

	fmt.Printf("%-28s %-20s %-10s %-8s %s\n", "前缀", "归属", "Proton", "独占占用", "状态")
	for _, p := range prefixes {
		total += p.SizeBytes

		owner := "共享（全部实例）"
		switch {
		case strings.HasPrefix(p.Key, "bak-"):
			owner = "旧版本备份"
		case p.Key != "" && instances[p.Key]:
			owner = "实例 " + p.Key
		case p.Key != "":
			owner = "实例 " + p.Key + "（已不存在）"
		}
		if p.Overlay {
			owner += " · 可写层"
			hasOverlay = true
		}

		var state []string
		// 可写层的三种形态在下面单独说，"未初始化" 对它们只会误导：重启后
		// 「没挂载」是常态，而 merged 那时本来就是个空挂载点。
		if !p.Initialized && !p.Overlay {
			state = append(state, "未初始化")
		}
		// 换过 prefix_mode 之后，上一个模式的目录还在，而且它的实例也还在 ——
		// 光看这张表完全看不出它已经再也不会被打开了，几百 MiB 就这么留着。
		if !p.Current && p.Key != "" && instances[p.Key] {
			state = append(state, "旧模式残留，可回收")
		}
		if p.Overlay {
			// 三种形态占同一个路径，必须报出来是哪一种：挂载是正常形态，
			// 「已复制」说明当初 overlayfs 没挂上、走了降级路径（那台机器上这个
			// 模式并没有在省盘，只看占用数字是看不出来的），而「未挂载」是宿主机
			// 重启之后的常态 —— 挂载不跨重启存活，内容还在 upper 里。
			switch {
			case p.Mounted:
				state = append(state, "已挂载")
			case p.Initialized:
				state = append(state, "已复制（overlayfs 未生效）")
			default:
				// 挂载活在宿主机的 mount namespace 里，不跨重启存活。
				// 内容还在 upper 里，下次启动实例时自动重新挂上。
				state = append(state, "未挂载，下次启动时自动挂载")
			}
		}
		if p.InUse {
			state = append(state, "使用中")
		}
		if len(state) == 0 {
			state = append(state, "就绪")
		}

		// overlay 的行显示 <实例名>/merged 而不是光秃秃的 "merged" ——
		// 后者每一行都长得一样，等于没显示。
		name := filepath.Base(p.Path)
		if p.Overlay {
			name = filepath.Join(p.Key, filepath.Base(p.Path))
		}
		fmt.Printf("%-28s %-20s %-10s %-8s %s\n",
			name, owner, orDash(p.ProtonVersion),
			humanSize(p.SizeBytes), strings.Join(state, "、"))
	}

	fmt.Printf("\n合计占用：%s\n", humanSize(total))
	if hasOverlay {
		fmt.Println("（可写层一栏是**独占**占用：与它们共享的底层前缀只在上面计过一次。）")
	}
	if n := len(gcCandidates(prefixes, instances)); n > 0 {
		fmt.Printf("其中 %d 个可回收，执行 asa-server prefix gc 查看详情。\n", n)
	}
	return nil
}

func actionPrefixGC(ctx context.Context, cmd *cli.Command) error {
	if runtime.GOOS != "linux" {
		fmt.Println("Windows 上没有 Wine 前缀，无需清理。")
		return nil
	}

	candidates := gcCandidates(runner.PrefixStatus(), existingInstances())
	if len(candidates) == 0 {
		fmt.Println("没有可回收的 Wine 前缀。")
		return nil
	}

	var total int64
	for _, p := range candidates {
		total += p.SizeBytes
	}

	if !cmd.Bool("apply") {
		fmt.Printf("以下 %d 个前缀可回收（共 %s）：\n", len(candidates), humanSize(total))
		for _, p := range candidates {
			fmt.Printf("  %s  (%s)\n", p.Path, humanSize(p.SizeBytes))
		}
		fmt.Println("\n这是预演，什么都没有删除。确认无误后执行：asa-server prefix gc --apply")
		return nil
	}

	var failed int
	for _, p := range candidates {
		fmt.Printf("删除 %s ... ", p.Path)
		// 备份目录不属于任何实例，RemoveInstancePrefix 认不出来，直接删。
		// 每实例前缀走 RemoveInstancePrefix，让它再确认一次 wineserver 占用 ——
		// 列表是几秒前拍的快照，这期间实例完全可能被启动。
		var err error
		if strings.HasPrefix(p.Key, "bak-") {
			err = os.RemoveAll(p.Path)
		} else {
			err = runner.RemoveInstancePrefix(p.Key)
		}
		if err != nil {
			failed++
			fmt.Printf("失败：%v\n", err)
			continue
		}
		fmt.Println("完成")
	}

	if failed > 0 {
		return fmt.Errorf("%d 个前缀未能删除", failed)
	}
	fmt.Printf("\n已回收 %s。\n", humanSize(total))
	return nil
}

// gcCandidates：当前模式用不到、且没有 wineserver 占用的前缀。
//
// 判据是「**当前模式**还会不会用到这个目录」，不是「实例还存不存在」。后者漏掉
// 了最大的一类垃圾：换过 prefix_mode 的机器上，上一个模式给**仍然存在的实例**留下
// 的目录 —— 真机上就是两个各 690 MiB 的 umu-prefix-<实例名>，实例活得好好的，
// 所以按旧判据永远不是候选，而它们已经再也不会被打开。
//
// 共享前缀（Key 为空）永远不是候选：它是 setup 建的运行时基线，overlay 模式下
// 更是所有可写层的底层。
func gcCandidates(prefixes []runner.PrefixInfo, instances map[string]bool) []runner.PrefixInfo {
	var out []runner.PrefixInfo
	for _, p := range prefixes {
		if p.Key == "" || p.InUse {
			continue
		}
		if p.Current && instances[p.Key] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// existingInstances 是 instances/ 下的目录名集合。用目录而不是
// LoadInstanceConfig：配置文件损坏的实例仍然是个实例，不该因此被当成孤儿
// 把前缀删掉。
func existingInstances() map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(cfgpkg.InstancesDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
