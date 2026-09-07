package actions

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	procpkg "asa-server/internal/process"

	"github.com/shirou/gopsutil/v4/process"
	"github.com/urfave/cli/v3"
)

// netmon：把「这个 PID 的网络流量到底采不采得到」当场跑出来。
//
// 两条子命令 `netmon ebpf`（Linux）/ `netmon etw`（Windows）各自只在自己的平台
// 存在，由 netmon_{linux,windows}.go 提供；本文件是与机制无关的那一半——
// 目标解析、采样循环、输出、判定、自测流量。
//
// 为什么要有这条命令：实例级网络监控这条链路从来没有独立的验证手段。
// 面板上一条曲线不能区分「采对了」「采到了别人的流量」「收方向整个缺失」，
// 而 Windows 侧恰好有一个悬而未决的风险正是最后那种
// （Kernel-Network 的 UDP 接收事件，见 docs/WINNET_ETW_TODO.md §4）。
// 详见 docs/NETMON_CLI_AND_ETW_WIRING_PLAN.md。
//
// 本文件没有 runtime.GOOS 判断，也不该加：子命令按平台注册，
// 到不了的分支不写（同 internal/actions/prefix.go 的既有裁决）。

// netCollector 是两个实现的公共形状。*procnet.Collector 与 *winnetetw.Collector
// 本来就满足它，不需要为这条命令改任何一个包。
type netCollector interface {
	Bytes(pid int32) (rx, tx uint64, ok bool)
	Describe() string
	Close() error
}

// protoSplitter 是可选能力：只有 ETW 侧给得出 TCP/UDP 分项
// （eBPF 那边六个探针是分协议挂的，Describe() 已给出等价信息）。
type protoSplitter interface {
	BytesByProtocol(pid int32) (tcpRx, tcpTx, udpRx, udpTx uint64, ok bool)
}

// 退出码。分开是为了能写进脚本；3 单独留给「只采到一个方向」——
// 那正是最需要人来看一眼的情况。
const (
	exitCaptureOK      = 0
	exitLoadFailed     = 1
	exitNoTraffic      = 2
	exitOneDirectionOK = 3
)

// NetmonCommand 是父命令；子命令表按平台组装（netmonPlatformCommands）。
func NetmonCommand() *cli.Command {
	return &cli.Command{
		Name:     "netmon",
		Usage:    "诊断按进程的网络流量采集（实例级网络监控用的那套机制）",
		Commands: netmonPlatformCommands(),
	}
}

// netmonFlags 是两条子命令共享的部分。
func netmonFlags() []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:  "pid",
			Usage: "要观察的进程 PID",
		},
		&cli.StringFlag{
			Name:  "instance",
			Usage: "实例名；PID 从实例的 pid 文件解析（与资源监控接口取的是同一个）",
		},
		&cli.BoolFlag{
			Name:  "selftest",
			Usage: "不看别人，观察本进程并主动打出可控流量（回环 TCP/UDP + 外发 DNS）",
		},
		&cli.IntFlag{
			Name:  "seconds",
			Value: 30,
			Usage: "采样总时长（秒）；0 表示一直跑到 Ctrl+C",
		},
		&cli.DurationFlag{
			Name:  "interval",
			Value: 2 * time.Second,
			Usage: "采样间隔；默认与资源采样器同频，看到的数就是面板会看到的数",
		},
	}
}

// netmonTarget 是解析出来的观察目标。
type netmonTarget struct {
	pid   int32
	label string
}

// resolveTarget 从 flag 里取三个值，逻辑本身在 resolveTargetFrom（可单测）。
func resolveTarget(cmd *cli.Command) (netmonTarget, error) {
	return resolveTargetFrom(int(cmd.Int("pid")), cmd.String("instance"), cmd.Bool("selftest"))
}

// resolveTargetFrom 解析观察目标。三个来源**互斥**，同时给多个直接报错——
// 静默挑一个会让人对着错的进程看半天还以为是采集坏了。
func resolveTargetFrom(pid int, instanceRaw string, self bool) (netmonTarget, error) {
	instance := strings.TrimSpace(instanceRaw)

	given := 0
	if pid > 0 {
		given++
	}
	if instance != "" {
		given++
	}
	if self {
		given++
	}
	switch {
	case given == 0:
		return netmonTarget{}, errors.New("必须指定 --pid、--instance 或 --selftest 之一")
	case given > 1:
		return netmonTarget{}, errors.New("--pid / --instance / --selftest 互斥，只能给一个")
	}

	if self {
		return netmonTarget{pid: int32(os.Getpid()), label: "本进程（自测）"}, nil
	}
	if instance != "" {
		p, err := procpkg.GetInstancePID(instance)
		if err != nil {
			return netmonTarget{}, fmt.Errorf("读取实例 %s 的 PID 失败（实例在跑吗？）: %w", instance, err)
		}
		return netmonTarget{pid: int32(p), label: fmt.Sprintf("实例 %s", instance)}, nil
	}
	return netmonTarget{pid: int32(pid), label: procName(int32(pid))}, nil
}

// procName 尽力取一个进程名做标签；取不到不是错误，标签只是给人看的。
func procName(pid int32) string {
	p, err := process.NewProcess(pid)
	if err != nil {
		return "进程已退出？"
	}
	name, err := p.Name()
	if err != nil {
		return ""
	}
	return name
}

// runNetmon 是两条子命令的共同主体。load 由平台文件提供。
func runNetmon(ctx context.Context, cmd *cli.Command, load func() (netCollector, error)) error {
	target, err := resolveTarget(cmd)
	if err != nil {
		return cli.Exit(err.Error(), exitLoadFailed)
	}

	c, err := load()
	if err != nil {
		// 两个包的错误文案都已经是可行动的中文，原样透出即可
		return cli.Exit(fmt.Sprintf("加载失败: %v", err), exitLoadFailed)
	}
	defer func() {
		if err := c.Close(); err != nil {
			fmt.Printf("[收尾] 关闭时报错: %v\n", err)
		}
	}()

	fmt.Printf("[加载] %s\n", c.Describe())
	fmt.Printf("[目标] PID %d %s\n\n", target.pid, bracket(target.label))

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	var delta protoDelta
	if cmd.Bool("selftest") {
		delta = runSelftest(ctx, c, target.pid)
	} else {
		delta = runWatch(ctx, c, target.pid, cmd.Int("seconds"), cmd.Duration("interval"))
	}

	fmt.Printf("\n[结束] %s\n", c.Describe())
	return verdict(delta)
}

// protoDelta 是整个观察窗口里的净增量。分项在 eBPF 侧恒为 hasProto=false。
type protoDelta struct {
	rx, tx                     uint64
	tcpRx, tcpTx, udpRx, udpTx uint64
	hasProto                   bool
	rounds                     int
	unavailable                int // 有多少轮返回了「采不到」
}

// runWatch 是常规模式：每 interval 打一行累计值与速率。
func runWatch(ctx context.Context, c netCollector, pid int32, seconds int, interval time.Duration) protoDelta {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	rounds := 0
	if seconds > 0 {
		rounds = int(time.Duration(seconds) * time.Second / interval)
		if rounds < 1 {
			rounds = 1
		}
	}

	fmt.Printf("%6s  %12s  %12s  %12s  %12s\n", "轮次", "累计 RX", "累计 TX", "RX 速率", "TX 速率")
	fmt.Println(strings.Repeat("-", 64))

	var (
		d              protoDelta
		baseRx, baseTx uint64
		prevRx, prevTx uint64
		prevAt         time.Time
	)
	first := true

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for i := 1; rounds == 0 || i <= rounds; i++ {
		select {
		case <-ctx.Done():
			fmt.Println("\n[中断] 收到 Ctrl+C")
			return d
		case <-ticker.C:
		}

		now := time.Now()
		rx, tx, ok := c.Bytes(pid)
		if !ok {
			d.unavailable++
			fmt.Printf("%6d  %12s  %12s  %12s  %12s\n", i, "采不到", "采不到", "-", "-")
			continue
		}
		d.rounds++

		if first {
			// 首轮只有基线，没有速率——与面板「首帧速率为 null」是同一条规则。
			// 打成 0 B/s 会被读成「采不到」。
			baseRx, baseTx = rx, tx
			first = false
			fmt.Printf("%6d  %12s  %12s  %12s  %12s\n", i, humanBytes(rx), humanBytes(tx), "-", "-")
		} else {
			dt := now.Sub(prevAt).Seconds()
			fmt.Printf("%6d  %12s  %12s  %12s  %12s\n", i,
				humanBytes(rx), humanBytes(tx),
				humanRate(rx, prevRx, dt), humanRate(tx, prevTx, dt))
		}
		prevRx, prevTx, prevAt = rx, tx, now
		d.rx, d.tx = rx-baseRx, tx-baseTx

		if s, okProto := c.(protoSplitter); okProto {
			if a, b, e, f, ok2 := s.BytesByProtocol(pid); ok2 {
				d.tcpRx, d.tcpTx, d.udpRx, d.udpTx = a, b, e, f
				d.hasProto = true
			}
		}
	}
	return d
}

// verdict 把一次观察落成一句结论 + 一个退出码。
func verdict(d protoDelta) error {
	if d.hasProto {
		fmt.Printf("\n[分项] TCP  RX %-12s TX %-12s\n", humanBytes(d.tcpRx), humanBytes(d.tcpTx))
		fmt.Printf("       UDP  RX %-12s TX %-12s\n", humanBytes(d.udpRx), humanBytes(d.udpTx))
	}
	fmt.Printf("[小结] 有效采样 %d 轮：RX +%s，TX +%s\n", d.rounds, humanBytes(d.rx), humanBytes(d.tx))
	if d.unavailable > 0 {
		fmt.Printf("       其中 %d 轮返回「采不到」——会话可能被同机另一个消费进程抢走了\n", d.unavailable)
	}

	switch {
	case d.rx > 0 && d.tx > 0:
		fmt.Println("[判定] 捕获正常")
		return cli.Exit("", exitCaptureOK)

	case d.tx > 0:
		fmt.Println("[判定] ⚠️ 只采到发送方向，接收方向恒零")
		fmt.Println("       若目标是 ARK 实例，这正是 docs/WINNET_ETW_TODO.md §4 说的那个风险：")
		fmt.Println("       Kernel-Network 的 UDP 接收事件可能不触发、或 PID 归错进程。")
		fmt.Println("       接线（procnet 委托）前必须先弄清这一条。")
		return cli.Exit("", exitOneDirectionOK)

	case d.rx > 0:
		fmt.Println("[判定] ⚠️ 只采到接收方向，发送方向恒零")
		return cli.Exit("", exitOneDirectionOK)

	default:
		fmt.Println("[判定] 未捕获到流量。按这四条自查：")
		fmt.Println("       1) 机制本身通不通：先跑一次 --selftest")
		fmt.Println("       2) 目标进程是不是把网络活儿交给了别人 —— 这是最常见的误判。")
		fmt.Println("          比如 docker CLI 只通过 unix socket 指挥守护进程，真正下载的是 dockerd；")
		fmt.Println("          [结束] 那行里字节数最大的 tgid 才是真正在收发的那个进程")
		fmt.Println("       3) 上面 [结束] 那行的事件计数是不是零（零 = 根本没收到内核事件）")
		fmt.Println("       4) PID 对不对（--instance 取的是实例 pid 文件里那个）")
		return cli.Exit("", exitNoTraffic)
	}
}

// ---- --selftest：不依赖任何外部进程的「能不能采到」 ----

const (
	selftestTCPBytes = 8 << 20 // 8 MiB
	selftestUDPBytes = 2 << 20 // 2 MiB
	selftestDatagram = 1200    // 贴近游戏流量的包长，也避开 IP 分片

	// selftestSettle 是每段打完流量之后等事件送达的时间。
	//
	// ⚠️ **必须大于 ETW 会话的 FlushTimer（1 秒）**。原来只等 700ms，结果第一段
	// 的事件在第二段的窗口里才到账，真机上表现为「回环 TCP 0 字节、回环 UDP
	// 16 MB」——数字是对的，只是记在了下一段头上，很容易被读成「TCP 采不到」。
	// eBPF 那边是同步更新 map、不需要等，但两个平台用同一段代码，取大的。
	selftestSettle = 2500 * time.Millisecond
)

// runSelftest 分三段跑，每段单独报增量。**不合并判定**：
// 回环是否被计入尚未在真机确认（docs/WINNET_ETW_PLAN.md §4.9 是推断不是实测），
// 前两段为零并不等于机制不工作，得看第三段。
func runSelftest(ctx context.Context, c netCollector, pid int32) protoDelta {
	fmt.Println("自测三段，每段单独看增量：")
	fmt.Println("  1) 回环 TCP   2) 回环 UDP   3) 外发 DNS（真网卡 + UDP 双向）")
	fmt.Println()

	c.Bytes(pid) // 首问 = 登记，此后内核事件才为它计数
	time.Sleep(500 * time.Millisecond)

	var d protoDelta
	baseRx, baseTx, _ := c.Bytes(pid)

	phases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"回环 TCP", selftestLoopbackTCP},
		{"回环 UDP", selftestLoopbackUDP},
		{"外发 DNS", selftestDNS},
	}

	loopbackMoved := false
	for i, ph := range phases {
		if ctx.Err() != nil {
			fmt.Println("[中断] 收到 Ctrl+C")
			break
		}
		beforeRx, beforeTx, _ := c.Bytes(pid)
		err := ph.run(ctx)
		time.Sleep(selftestSettle) // 等事件送达，见该常量的注释
		afterRx, afterTx, ok := c.Bytes(pid)

		status := ""
		if err != nil {
			status = fmt.Sprintf("（流量生成出错: %v）", err)
		}
		if !ok {
			fmt.Printf("  %d) %-8s 采不到%s\n", i+1, ph.name, status)
			d.unavailable++
			continue
		}
		dRx, dTx := afterRx-beforeRx, afterTx-beforeTx
		mark := "✅"
		if dRx == 0 && dTx == 0 {
			mark = "❌"
		} else if i < 2 {
			loopbackMoved = true
		}
		fmt.Printf("  %d) %-8s %s RX +%-10s TX +%-10s%s\n",
			i+1, ph.name, mark, humanBytes(dRx), humanBytes(dTx), status)
		d.rounds++
	}

	endRx, endTx, _ := c.Bytes(pid)
	d.rx, d.tx = endRx-baseRx, endTx-baseTx
	if s, okProto := c.(protoSplitter); okProto {
		if a, b, e, f, ok2 := s.BytesByProtocol(pid); ok2 {
			d.tcpRx, d.tcpTx, d.udpRx, d.udpTx = a, b, e, f
			d.hasProto = true
		}
	}
	if !loopbackMoved && d.rx+d.tx > 0 {
		fmt.Println("\n  注：回环两段没动、外发那段有值 —— 本机的按进程计量不覆盖回环流量。")
		fmt.Println("      这不是故障：真正要看的 ARK 流量走真实网卡。")
	}
	if d.hasProto && d.udpRx == 0 && d.udpTx == 0 && d.tcpRx+d.tcpTx > 0 {
		fmt.Println("\n  ⚠️ 只有 TCP 有值、UDP 四路全零 —— ARK 的游戏流量是 UDP，")
		fmt.Println("      这种情况下实例网络图会是一条恒零曲线，接线前必须先弄清。")
	}
	return d
}

// selftestLoopbackTCP 在进程内起一个 127.0.0.1 的 echo 监听，自己连自己打满 N 字节。
// 不打外网：要的是确定性与离线可用。
func selftestLoopbackTCP(ctx context.Context) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64<<10)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if _, werr := conn.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	payload := make([]byte, 64<<10)
	done := make(chan error, 1)
	go func() { // 一边收，避免把内核缓冲打满后自己阻塞住
		buf := make([]byte, 64<<10)
		got := 0
		for got < selftestTCPBytes {
			n, err := conn.Read(buf)
			got += n
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	sent := 0
	for sent < selftestTCPBytes {
		n, err := conn.Write(payload)
		if err != nil {
			return err
		}
		sent += n
	}
	select {
	case <-done:
	case <-ctx.Done():
	case <-time.After(20 * time.Second):
	}
	return nil
}

// selftestLoopbackUDP 同上，UDP echo。
func selftestLoopbackUDP(ctx context.Context) error {
	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer srv.Close()

	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := srv.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := srv.WriteTo(buf[:n], addr); err != nil {
				return
			}
		}
	}()

	cli, err := net.Dial("udp", srv.LocalAddr().String())
	if err != nil {
		return err
	}
	defer cli.Close()

	payload := make([]byte, selftestDatagram)
	go func() { // 收方向单独跑，丢包无所谓，这里只求把计数打动
		buf := make([]byte, 2048)
		for {
			_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, err := cli.Read(buf); err != nil {
				return
			}
		}
	}()

	sent := 0
	for sent < selftestUDPBytes {
		if ctx.Err() != nil {
			break
		}
		n, err := cli.Write(payload)
		if err != nil {
			return err
		}
		sent += n
	}
	time.Sleep(300 * time.Millisecond)
	return nil
}

// selftestDNS 用**Go 自己的**解析器发若干次 DNS 查询。
//
// ⚠️ PreferGo 是必须的，不是风格问题：Windows 上默认解析器把查询交给系统
// DNS Client 服务，UDP 包是 svchost.exe 发的，本进程的计数一个字节都不会动——
// 拿那个结果去判定「UDP 采不到」是纯粹的误判。
func selftestDNS(ctx context.Context) error {
	r := &net.Resolver{PreferGo: true}
	hosts := []string{"example.com", "www.microsoft.com", "go.dev", "github.com", "cloudflare.com"}
	var lastErr error
	okCount := 0
	for _, h := range hosts {
		if ctx.Err() != nil {
			break
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err := r.LookupHost(lookupCtx, h)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		okCount++
	}
	if okCount == 0 && lastErr != nil {
		return fmt.Errorf("DNS 查询全部失败（本机不通外网？）: %w", lastErr)
	}
	return nil
}

// ---- 输出小工具 ----

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

func humanRate(cur, prev uint64, dt float64) string {
	if dt <= 0 || cur < prev {
		return "-"
	}
	return humanBytes(uint64(float64(cur-prev)/dt)) + "/s"
}

func bracket(s string) string {
	if s == "" {
		return ""
	}
	return "(" + s + ")"
}
