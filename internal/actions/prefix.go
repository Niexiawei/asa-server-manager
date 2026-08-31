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
// 只有 `linux.prefix_mode: per-instance` 会产生每实例前缀；`shared` 下永远只有
// 一个 `{BaseDir}/umu-prefix`。但清理必须与当前模式无关 —— 用过一阵子
// per-instance 再切回 shared 的用户，盘上仍留着一堆 `umu-prefix-<实例名>`，
// 除了这条命令没有别的东西会报告它们。
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
	var total int64

	fmt.Printf("%-28s %-16s %-10s %-8s %s\n", "前缀", "归属", "Proton", "占用", "状态")
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

		var state []string
		if !p.Initialized {
			state = append(state, "未初始化")
		}
		if p.InUse {
			state = append(state, "使用中")
		}
		if len(state) == 0 {
			state = append(state, "就绪")
		}

		fmt.Printf("%-28s %-16s %-10s %-8s %s\n",
			filepath.Base(p.Path), owner, orDash(p.ProtonVersion),
			humanSize(p.SizeBytes), strings.Join(state, "、"))
	}

	fmt.Printf("\n合计占用：%s\n", humanSize(total))
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

// gcCandidates：没有对应实例、且没有 wineserver 占用的前缀。
// 共享前缀（Key 为空）永远不是候选 —— 它是 setup 建的运行时基线，
// 与实例是否存在无关。
func gcCandidates(prefixes []runner.PrefixInfo, instances map[string]bool) []runner.PrefixInfo {
	var out []runner.PrefixInfo
	for _, p := range prefixes {
		if p.Key == "" || p.InUse {
			continue
		}
		if !strings.HasPrefix(p.Key, "bak-") && instances[p.Key] {
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
