package actions

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// 这些都是与平台、与 ETW/eBPF 无关的纯逻辑，所以本文件不带 build tag。
// 采集机制本身没法在单测里伪造出有意义的行为，靠真机验收覆盖
// （docs/NETMON_CLI_AND_ETW_WIRING_PLAN.md §7）。

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1 << 20, "1.0 MB"},
		{3*(1<<30) + (1 << 29), "3.5 GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q，应为 %q", c.in, got, c.want)
		}
	}
}

func TestHumanRate(t *testing.T) {
	if got := humanRate(2048, 1024, 1); got != "1.0 KB/s" {
		t.Errorf("1KB/1s = %q", got)
	}
	// 计数回绕/重置：不产生负数，也不假装是 0
	if got := humanRate(10, 100, 1); got != "-" {
		t.Errorf("cur<prev 应给 %q，实际 %q", "-", got)
	}
	if got := humanRate(100, 10, 0); got != "-" {
		t.Errorf("dt=0 应给 %q，实际 %q", "-", got)
	}
}

// 判定逻辑是这条命令的产出本身，四个分支都得钉住。
func TestVerdictExitCodes(t *testing.T) {
	cases := []struct {
		name string
		d    protoDelta
		want int
	}{
		{"双向都有", protoDelta{rx: 100, tx: 100, rounds: 5}, exitCaptureOK},
		{"只有发送", protoDelta{tx: 100, rounds: 5}, exitOneDirectionOK},
		{"只有接收", protoDelta{rx: 100, rounds: 5}, exitOneDirectionOK},
		{"都没动", protoDelta{rounds: 5}, exitNoTraffic},
	}
	for _, c := range cases {
		err := verdict(c.d)
		code, ok := exitCodeOf(err)
		if !ok {
			t.Fatalf("%s：verdict 应返回 cli.Exit，实际 %v", c.name, err)
		}
		if code != c.want {
			t.Errorf("%s：退出码 %d，应为 %d", c.name, code, c.want)
		}
	}
}

// exitCodeOf 从 cli.Exit 返回的错误里取退出码。
func exitCodeOf(err error) (int, bool) {
	type exitCoder interface{ ExitCode() int }
	if e, ok := err.(exitCoder); ok {
		return e.ExitCode(), true
	}
	return 0, false
}

// ---- 采样循环与自测流量 ----
//
// 采集机制伪造不出来，但**循环、差分、判定、以及自测那三段流量生成**都是普通代码，
// 而它们一旦坏了，真机上表现为「采不到流量」——会被误判成机制不行。所以这里用
// 假的 Collector 把这半边钉住。

type fakeCollector struct {
	mu       sync.Mutex
	rx, tx   uint64
	step     uint64
	ok       bool
	withProt bool
}

func (f *fakeCollector) Bytes(int32) (uint64, uint64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.ok {
		return 0, 0, false
	}
	f.rx += f.step
	f.tx += f.step / 2
	return f.rx, f.tx, true
}
func (f *fakeCollector) Describe() string { return "fake" }
func (f *fakeCollector) Close() error     { return nil }

type fakeProtoCollector struct{ *fakeCollector }

func (f fakeProtoCollector) BytesByProtocol(int32) (a, b, c, d uint64, ok bool) {
	rx, tx, ok := f.fakeCollector.Bytes(0)
	return rx / 2, tx / 2, rx - rx/2, tx - tx/2, ok
}

func TestRunWatchCountsRoundsAndDelta(t *testing.T) {
	f := &fakeCollector{step: 1000, ok: true}
	d := runWatch(context.Background(), f, 1234, 1, 100*time.Millisecond)

	if d.rounds != 10 {
		t.Errorf("应采 10 轮，实际 %d", d.rounds)
	}
	// 首轮是基线，增量从第二轮起算
	if d.rx == 0 || d.tx == 0 {
		t.Errorf("增量不应为零：rx=%d tx=%d", d.rx, d.tx)
	}
	if d.hasProto {
		t.Error("不实现 protoSplitter 的 Collector 不该有分项")
	}
}

func TestRunWatchUnavailable(t *testing.T) {
	f := &fakeCollector{step: 1000, ok: false}
	d := runWatch(context.Background(), f, 1234, 1, 200*time.Millisecond)

	if d.unavailable != 5 || d.rounds != 0 {
		t.Errorf("采不到应全部计入 unavailable：rounds=%d unavailable=%d", d.rounds, d.unavailable)
	}
	if code, _ := exitCodeOf(verdict(d)); code != exitNoTraffic {
		t.Errorf("全程采不到应判 %d，实际 %d", exitNoTraffic, code)
	}
}

func TestRunWatchPicksUpProtoSplit(t *testing.T) {
	f := fakeProtoCollector{&fakeCollector{step: 1000, ok: true}}
	d := runWatch(context.Background(), f, 1234, 1, 250*time.Millisecond)
	if !d.hasProto {
		t.Fatal("实现了 protoSplitter 就应该拿到分项")
	}
	if d.tcpRx == 0 && d.udpRx == 0 {
		t.Error("分项全为零")
	}
}

func TestRunWatchStopsOnContextCancel(t *testing.T) {
	f := &fakeCollector{step: 10, ok: true}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		runWatch(ctx, f, 1234, 0, 100*time.Millisecond) // seconds=0 = 一直跑
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后应立即退出，卡住了")
	}
}

// 自测的三段流量生成本身必须是可靠的：它坏了就等于给出假阴性。
func TestSelftestLoopbackTraffic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := selftestLoopbackTCP(ctx); err != nil {
		t.Errorf("回环 TCP 打流失败: %v", err)
	}
	if err := selftestLoopbackUDP(ctx); err != nil {
		t.Errorf("回环 UDP 打流失败: %v", err)
	}
}

func TestSelftestDNSUsesGoResolver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// 断网机器上会失败，那不是缺陷——这里只要求它不 panic、错误可读
	if err := selftestDNS(ctx); err != nil {
		if !strings.Contains(err.Error(), "DNS 查询全部失败") {
			t.Errorf("错误文案应说明是 DNS 全部失败，实际: %v", err)
		}
		t.Skipf("本机 DNS 不通，跳过: %v", err)
	}
}

func TestBracket(t *testing.T) {
	if got := bracket(""); got != "" {
		t.Errorf("空标签不该带括号，实际 %q", got)
	}
	if got := bracket("x"); got != "(x)" {
		t.Errorf("bracket = %q", got)
	}
}

// resolveTarget 的互斥规则：给多个或一个不给都必须报错，
// 静默挑一个会让人对着错的进程看半天。这里只测不需要 cli.Command 的那两条边界。
func TestResolveTargetMutualExclusion(t *testing.T) {
	// 三个都不给
	if _, err := resolveTargetFrom(0, "", false); err == nil ||
		!strings.Contains(err.Error(), "必须指定") {
		t.Errorf("三个都不给应报错，实际 %v", err)
	}
	// 同时给两个
	if _, err := resolveTargetFrom(123, "srv", false); err == nil ||
		!strings.Contains(err.Error(), "互斥") {
		t.Errorf("同时给两个应报错，实际 %v", err)
	}
	if _, err := resolveTargetFrom(0, "srv", true); err == nil ||
		!strings.Contains(err.Error(), "互斥") {
		t.Errorf("同时给两个应报错，实际 %v", err)
	}
	// --selftest 单独给：目标是本进程
	tgt, err := resolveTargetFrom(0, "", true)
	if err != nil || tgt.pid <= 0 {
		t.Errorf("selftest 应解析出本进程，实际 %+v err=%v", tgt, err)
	}
}
