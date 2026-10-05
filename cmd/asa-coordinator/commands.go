package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/kardianos/service"
	"github.com/urfave/cli/v3"

	"asa-server/internal/meshcoord"
	"asa-server/pkg/meshid"
	"asa-server/pkg/stun"
)

func configCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "配置文件",
		Commands: []*cli.Command{{
			Name:  "init",
			Usage: "生成配置模板（默认写到程序所在目录）",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "输出路径"},
				&cli.BoolFlag{Name: "force", Usage: "目标已存在时覆盖（旧文件先备份）"},
			},
			Action: func(_ context.Context, cmd *cli.Command) error {
				path := cmd.String("output")
				if path == "" {
					p, err := meshcoord.DefaultInitPath()
					if err != nil {
						return err
					}
					path = p
				}
				backup, err := meshcoord.InitConfig(path, cmd.Bool("force"))
				if err != nil {
					return err
				}
				if backup != "" {
					fmt.Printf("旧配置已备份为 %s\n", backup)
				}
				fmt.Printf("已生成 %s\n请先把其中的 public_addr 改成本机的公网 IP 或域名，并在防火墙放行 TCP 443、UDP 3478/3479。\n", path)
				return nil
			},
		}},
	}
}

func nodeCommand() *cli.Command {
	networkFlag := &cli.StringFlag{Name: "network", Value: meshcoord.DefaultNetworkID, Usage: "网络 ID"}
	withStore := func(cmd *cli.Command, fn func(*meshcoord.Store) error) error {
		cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		store, err := meshcoord.OpenStore(cfg.DataDir)
		if err != nil {
			return err
		}
		defer store.Close()
		return fn(store)
	}
	setBanned := func(banned bool) cli.ActionFunc {
		return func(_ context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() != 1 {
				return cli.Exit("用法：asa-coordinator node ban|unban <节点 ID>", 2)
			}
			id, err := meshid.ParseID(cmd.Args().First())
			if err != nil {
				return err
			}
			return withStore(cmd, func(s *meshcoord.Store) error {
				if err := s.SetBanned(cmd.String("network"), id, banned); err != nil {
					return err
				}
				if banned {
					fmt.Printf("已拉黑 %s。正在运行的协调节点会在 30 秒内断开它的会话。\n", id)
				} else {
					fmt.Printf("已解除拉黑 %s\n", id)
				}
				return nil
			})
		}
	}
	return &cli.Command{
		Name:  "node",
		Usage: "节点管理",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "列出接入过的节点",
				Flags: []cli.Flag{configFlag(), &cli.StringFlag{Name: "network", Usage: "只看这个网络（默认全部）"}},
				Action: func(_ context.Context, cmd *cli.Command) error {
					return withStore(cmd, func(s *meshcoord.Store) error {
						nodes, err := s.ListNodes(cmd.String("network"))
						if err != nil {
							return err
						}
						w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
						fmt.Fprintln(w, "网络\t节点 ID\t备注\t版本\t首次接入\t最近在线\t状态")
						for _, n := range nodes {
							state := "正常"
							if n.Banned {
								state = "已拉黑"
							}
							fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
								n.NetworkID, n.NodeID, n.Label, n.Version, fmtTime(n.FirstSeen), fmtTime(n.LastSeen), state)
						}
						return w.Flush()
					})
				},
			},
			{Name: "ban", Usage: "拉黑节点", ArgsUsage: "<节点 ID>", Flags: []cli.Flag{configFlag(), networkFlag}, Action: setBanned(true)},
			{Name: "unban", Usage: "解除拉黑", ArgsUsage: "<节点 ID>", Flags: []cli.Flag{configFlag(), networkFlag}, Action: setBanned(false)},
		},
	}
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func serviceCommand() *cli.Command {
	control := func(fn func(service.Service) error, done string) cli.ActionFunc {
		return func(context.Context, *cli.Command) error {
			svc, err := service.New(&program{}, serviceConfig(nil))
			if err != nil {
				return err
			}
			if err := fn(svc); err != nil {
				return err
			}
			fmt.Println(done)
			return nil
		}
	}
	return &cli.Command{
		Name:  "service",
		Usage: "系统服务（Windows 服务 / systemd）",
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "安装为系统服务（配置文件的绝对路径会写进服务参数）",
				Flags: []cli.Flag{configFlag()},
				Action: func(_ context.Context, cmd *cli.Command) error {
					// 先完整校验：装上一个起不来的服务没有意义。之后服务运行时不再走查找顺序，
					// 挪动配置文件 = 重新 service install。
					cfg, err := loadConfig(cmd)
					if err != nil {
						return err
					}
					svc, err := service.New(&program{}, serviceConfig([]string{"run", "-c", cfg.Path()}))
					if err != nil {
						return err
					}
					if err := svc.Install(); err != nil {
						return err
					}
					fmt.Printf("服务 %s 已安装，配置文件：%s\n运行 asa-coordinator service start 启动。\n", serviceName, cfg.Path())
					return nil
				},
			},
			{Name: "remove", Usage: "卸载服务", Action: control(func(s service.Service) error {
				_ = s.Stop()
				return s.Uninstall()
			}, "服务已卸载")},
			{Name: "start", Usage: "启动服务", Action: control(func(s service.Service) error { return s.Start() }, "服务已启动")},
			{Name: "stop", Usage: "停止服务", Action: control(func(s service.Service) error { return s.Stop() }, "服务已停止")},
		},
	}
}

func stunCommand() *cli.Command {
	return &cli.Command{
		Name:  "stun",
		Usage: "STUN 排障",
		Commands: []*cli.Command{{
			Name:      "probe",
			Usage:     "向协调节点的两个 STUN 端口各问一次，判断本机所在网络的 NAT 映射类型",
			ArgsUsage: "<host>[:port]",
			Flags: []cli.Flag{
				&cli.IntFlag{Name: "alt-port", Usage: "第二个端口（默认 = 第一个端口 + 1）"},
				&cli.DurationFlag{Name: "timeout", Value: 5 * time.Second, Usage: "每个端口的超时"},
			},
			Action: actionSTUNProbe,
		}},
	}
}

// actionSTUNProbe 不读配置文件、不碰数据目录：可以在任何机器上跑。
func actionSTUNProbe(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return cli.Exit("用法：asa-coordinator stun probe <host>[:port]", 2)
	}
	target := cmd.Args().First()
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		host, portStr = target, "3478"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("端口 %q 不是数字", portStr)
	}
	alt := int(cmd.Int("alt-port"))
	if alt == 0 {
		alt = port + 1
	}

	pc, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return err
	}
	defer pc.Close()
	localPort := uint16(pc.LocalAddr().(*net.UDPAddr).Port)

	query := func(p int) (netip.AddrPort, error) {
		addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(p)))
		if err != nil {
			return netip.AddrPort{}, err
		}
		qctx, cancel := context.WithTimeout(ctx, cmd.Duration("timeout"))
		defer cancel()
		return stun.Query(qctx, pc, addr)
	}
	a, errA := query(port)
	b, errB := query(alt)

	fmt.Printf("本机 UDP 端口：%d\n", localPort)
	show := func(p int, ap netip.AddrPort, err error) {
		if err != nil {
			fmt.Printf("  %s:%d → 失败：%v\n", host, p, err)
			return
		}
		fmt.Printf("  %s:%d → 反射地址 %s\n", host, p, ap)
	}
	show(port, a, errA)
	show(alt, b, errB)
	if errA != nil && errB != nil {
		return errors.New("两个端口都没有回答：检查协调节点的 stun.listen 与防火墙（UDP）")
	}

	var local []netip.AddrPort
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, ad := range addrs {
			if ipn, ok := ad.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(ipn.IP); ok {
					local = append(local, netip.AddrPortFrom(ip.Unmap(), localPort))
				}
			}
		}
	}
	fmt.Printf("结论：%s\n", stun.ClassifyMapping(local, a, b))
	return nil
}
