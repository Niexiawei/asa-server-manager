//go:build linux && amd64

package procnet

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"asa-server/pkg/logger"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/btf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

// BPF 对象**已编译好提交进仓库**，常规 go build 不需要 clang/llvm。
// 重新生成的 //go:generate 指令在 procnet.go 里（那个文件没有 build tag，
// 否则在 Windows 开发机上 go generate 会连指令都看不见）。
//
//go:embed bpf/procnet_amd64.o
var bpfObject []byte

// bpfObjectNS 是同一份 C 源加 -DPROCNET_NS_PID 编出来的：它用
// bpf_get_ns_current_pid_tgid 把 tgid 换算到**调用方所在的 PID namespace**。
//
// 为什么要两个产物而不是一个带开关的：那个 helper 要 kernel 5.7+，
// 而本项目基线是 5.4——5.4 的 verifier 见到未知 helper 会直接拒绝整个程序。
// 所以先试这个、失败再退回基础版（见 Load）。
//
//go:embed bpf/procnet_ns_amd64.o
var bpfObjectNS []byte

// pidnsCfg 与 bpf/procnet.c 的 struct pidns_cfg 逐字段对应。
type pidnsCfg struct {
	Dev uint64
	Ino uint64
}

// pidNamespaceID 取本进程 PID namespace 的 (dev, ino)，就是
// bpf_get_ns_current_pid_tgid 要的那两个数。
func pidNamespaceID() (pidnsCfg, error) {
	var st unix.Stat_t
	if err := unix.Stat("/proc/self/ns/pid", &st); err != nil {
		return pidnsCfg{}, err
	}
	return pidnsCfg{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, nil
}

const (
	// targetTTL 目标进程在 BPF 侧的登记有效期。Bytes 每个采样周期（2s）被问一次，
	// 30s = 连续 15 轮没人问才淘汰，足以容忍一次卡顿。
	targetTTL = 30 * time.Second

	// pruneInterval 两次淘汰扫描的最小间隔——每次 Bytes 都扫一遍没必要。
	pruneInterval = 10 * time.Second

	// catchAllKey 是 targets 里的**诊断哨兵**：存在它时 BPF 侧对所有 tgid 计数
	// （bpf/procnet.c 的 tracked()）。tgid 0 是 swapper/idle，走不到那些 socket
	// 路径，所以拿它当哨兵不会和真实目标撞车。只有 Options.Diagnostics 会写。
	catchAllKey = 0
)

// counters 与 bpf/procnet.c 里的 struct counters 必须逐字段对应（累计值，非速率）。
type counters struct {
	Rx uint64
	Tx uint64
}

type probeSpec struct {
	prog   string // BPF 程序名（C 里的函数名）
	symbol string // 内核符号
	ret    bool   // true = kretprobe
}

// probes 六个挂点。ARK 的游戏流量是 UDP，只挂 TCP 会得到一条恒零的实例网络曲线。
// 每个探针**单独兜底**：内核符号跨版本可能漂移，缺一个不影响其它几个。
var probes = []probeSpec{
	{prog: "kprobe_tcp_sendmsg", symbol: "tcp_sendmsg"},
	{prog: "kprobe_tcp_cleanup_rbuf", symbol: "tcp_cleanup_rbuf"},
	{prog: "kprobe_udp_sendmsg", symbol: "udp_sendmsg"},
	{prog: "kprobe_udpv6_sendmsg", symbol: "udpv6_sendmsg"},
	{prog: "kretprobe_udp_recvmsg", symbol: "udp_recvmsg", ret: true},
	{prog: "kretprobe_udpv6_recvmsg", symbol: "udpv6_recvmsg", ret: true},
}

// Collector 持有已加载的 BPF 对象与挂上的探针。进程内单例由调用方保证。
type Collector struct {
	coll     *ebpf.Collection
	targets  *ebpf.Map
	counters *ebpf.Map
	links    []link.Link
	desc     string

	// 诊断用（Options.Diagnostics）：挂上的程序 + 内核统计开关的句柄。
	// attached 与 progs 一一对应，Describe 靠它报每个探针的命中次数。
	attached  []string
	progs     []*ebpf.Program
	statsDone io.Closer
	catchAll  bool // 全捕获哨兵已写入：counters 里会有别的进程，Describe 据此报告

	mu        sync.Mutex
	seen      map[uint32]time.Time
	lastPrune time.Time
}

// Load 加载 BPF 程序并挂上探针。失败一律返回 error，调用方只降级不阻断。
func Load(opts Options) (*Collector, error) {
	// 5.4 上 BPF map 的内存走进程的 locked-memory 配额（memcg 计费是 5.11 才引入的），
	// 默认 ulimit（常见 64KB）下 map 创建直接 EPERM——这一步不是可选项。
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("解除 RLIMIT_MEMLOCK 失败: %w", err)
	}

	btfNote := "内核自带"
	var copts ebpf.CollectionOptions
	if kt, from, err := kernelTypes(opts.BTFPath); err != nil {
		// 外部 BTF 配错了不该直接放弃：这版程序本来就没有 CO-RE 重定位，
		// 内核自带的（乃至完全没有）也能加载。
		logger.Warnf("procnet: 外部 BTF 不可用，改用内核自带的再试一次: %v", err)
	} else if kt != nil {
		copts.Programs.KernelTypes = kt
		btfNote = from
	}

	// 先试 namespace 感知版：只有它能在容器里把 tgid 换算到调用方的 PID 空间。
	// 5.7 以下的内核会因为未知 helper 拒绝加载，那就退回基础版——
	// 基础版在**没有** PID namespace 的机器上完全正确，有的话则采不到（字段为 null）。
	pidnsNote := "命名空间感知"
	coll, err := newCollection(bpfObjectNS, copts)
	if err != nil {
		logger.Debugf("procnet: 命名空间感知版加载失败（需要内核 5.7+），退回基础版: %v", err)
		pidnsNote = "基础版（无命名空间换算）"
		coll, err = newCollection(bpfObject, copts)
	}
	if err != nil {
		return nil, fmt.Errorf("加载 BPF 对象失败（内核不支持 / 无权限 / 被 lockdown 或容器策略挡下）: %w", err)
	}

	c := &Collector{
		coll:      coll,
		targets:   coll.Maps["procnet_targets"],
		counters:  coll.Maps["procnet_counters"],
		seen:      make(map[uint32]time.Time),
		lastPrune: time.Now(),
	}
	if c.targets == nil || c.counters == nil {
		coll.Close()
		return nil, errors.New("BPF 对象里缺少 procnet_targets / procnet_counters map")
	}

	// 把本进程 PID namespace 的 (dev, ino) 交给 BPF 侧；写不进去时 BPF 那边
	// 看到 ino == 0，自动退回 bpf_get_current_pid_tgid()（初始 namespace）。
	if m := coll.Maps["procnet_pidns"]; m != nil {
		cfg, err := pidNamespaceID()
		if err != nil {
			logger.Warnf("procnet: 读取 /proc/self/ns/pid 失败，容器内可能采不到: %v", err)
			pidnsNote = "命名空间感知（未取到 ns id，实际退回初始 namespace）"
		} else if err := m.Put(uint32(0), cfg); err != nil {
			logger.Warnf("procnet: 写入 PID namespace 配置失败: %v", err)
			pidnsNote = "命名空间感知（配置写入失败，实际退回初始 namespace）"
		}
	}

	var attached []string
	for _, p := range probes {
		prog := coll.Programs[p.prog]
		if prog == nil {
			logger.Warnf("procnet: BPF 对象里没有程序 %s，跳过", p.prog)
			continue
		}
		var l link.Link
		var err error
		if p.ret {
			l, err = link.Kretprobe(p.symbol, prog, nil)
		} else {
			l, err = link.Kprobe(p.symbol, prog, nil)
		}
		if err != nil {
			// 内核符号漂移只让这一条曲线偏少，不该拖垮整个采集
			logger.Warnf("procnet: 挂载 %s 失败（该路径的流量不计入）: %v", p.symbol, err)
			continue
		}
		c.links = append(c.links, l)
		c.progs = append(c.progs, prog)
		attached = append(attached, p.symbol)
	}
	if len(attached) == 0 {
		c.Close()
		return nil, errors.New("六个探针一个都没挂上")
	}
	c.attached = attached

	// 诊断模式（只有 asa-server netmon 会开）：
	//   ①打开内核的 BPF 运行统计 → Describe 能报每个探针的命中次数；
	//   ②往 targets 写全捕获哨兵 key 0 → BPF 侧对所有 tgid 计数，于是
	//     counters 里的 key 就是「内核认为这些流量属于谁」的答案。
	// 两个都打不开也不是错误，排障时少几个数而已。
	if opts.Diagnostics {
		if closer, err := ebpf.EnableStats(uint32(unix.BPF_STATS_RUN_TIME)); err != nil {
			logger.Warnf("procnet: 打开 BPF 运行统计失败（需要 5.8+），命中次数将不可用: %v", err)
		} else {
			c.statsDone = closer
		}
		if err := c.targets.Put(uint32(catchAllKey), uint8(1)); err != nil {
			logger.Warnf("procnet: 写入全捕获哨兵失败，将看不到内核观察到的 tgid: %v", err)
		} else {
			c.catchAll = true
		}
	}

	c.desc = fmt.Sprintf("已挂载 %d/%d 个探针 [%s]，BTF 来源：%s，PID 口径：%s",
		len(attached), len(probes), strings.Join(attached, " "), btfNote, pidnsNote)
	return c, nil
}

// newCollection 解析并加载一份 BPF 对象。两个产物（基础版 / 命名空间感知版）
// 走同一条路径，Load 靠它做「先试后退」。
func newCollection(obj []byte, copts ebpf.CollectionOptions) (*ebpf.Collection, error) {
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(obj))
	if err != nil {
		return nil, fmt.Errorf("解析内嵌 BPF 对象失败: %w", err)
	}
	return ebpf.NewCollectionWithOptions(spec, copts)
}

// probeHits 返回每个已挂探针的命中次数，只有 Options.Diagnostics 打开时才有值。
//
// 为什么值得专门做：探针「挂上了」与「被执行了」是两回事，而两者失败时的外部
// 表现完全一样——曲线恒零。命中次数把它们分开：
//   - 全是 0：内核根本没走到这些函数（符号被内联、走了别的路径、kprobe 被禁用）；
//   - 有命中但计数仍为 0：程序跑了，是 tgid 对不上（PID namespace！）或参数取错了。
func (c *Collector) probeHits() string {
	if c == nil || c.statsDone == nil || len(c.progs) == 0 {
		return ""
	}
	var b strings.Builder
	var total uint64
	for i, prog := range c.progs {
		st, err := prog.Stats()
		if err != nil {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s=%d", c.attached[i], st.RunCount)
		total += st.RunCount
	}
	if b.Len() == 0 {
		return ""
	}
	counterKeys, counterDesc := c.observedTgids(16)
	out := fmt.Sprintf("；探针命中：%s；map 条目 targets=%d counters=%d",
		b.String(), mapEntries(c.targets), mapEntries(c.counters))

	switch {
	case total == 0:
		out += "（命中全为 0 = 内核根本没走到这些函数，与 tgid 无关）"

	case !c.catchAll && len(counterKeys) == 0:
		out += "（探针在跑但一条计数都没写；没开全捕获哨兵，无法判断是不是 tgid 对不上）"

	case c.catchAll:
		// 全捕获下 counters 的 key 就是「内核认为这些流量属于谁」。
		//
		// ⚠️ 要拿**被跟踪的 PID**去比，不是 os.Getpid()。两者在 --selftest 下恰好
		// 相同，于是这个错误一开始看不出来；换成 --pid 观察别人时就会得出
		// 「本进程不在列 ⇒ tgid 对不上」这种既无关又吓人的结论
		//（2026-09-07 真机上对着 docker CLI 跑就撞上了）。
		tracked := c.trackedPIDs()
		out += fmt.Sprintf("；内核观察到的 tgid %s（被跟踪的 PID=%v", counterDesc, tracked)
		switch {
		case len(counterKeys) == 0:
			out += "，counters 仍为空 ⇒ **哨兵都没生效**，说明 BPF 侧读到的 " +
				"procnet_targets 不是用户态写的那张 map，与 tgid 无关）"
		case anyIn(tracked, counterKeys):
			out += "，在列 ✅）"
		default:
			out += "，**不在列**。两种可能：①目标进程这段时间确实没走 TCP/UDP" +
				"（把活儿交给了别的进程，比如 docker CLI 只通过 unix socket 指挥 dockerd，" +
				"真正下载的是守护进程——上面字节数最大的那个 tgid 就是它）；" +
				"②内核与用户态的 PID 不在同一个空间（看开头的「PID 口径」，" +
				"若已是命名空间感知则基本可排除））"
		}
	}
	return out
}

// trackedPIDs 返回调用方问过、因而被登记进跟踪集合的 PID。诊断专用。
func (c *Collector) trackedPIDs() []uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	pids := make([]uint32, 0, len(c.seen))
	for pid := range c.seen {
		pids = append(pids, pid)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	return pids
}

func anyIn(needles, haystack []uint32) bool {
	for _, n := range needles {
		for _, h := range haystack {
			if n == h {
				return true
			}
		}
	}
	return false
}

// observedTgids 列出 counters 里的 tgid **及其累计字节**。诊断专用。
//
// 光有 key 不够用：真机上第一次拿到 [913 925 958 15036] 时无法判断
// 「其中某个就是本进程在宿主机上的 tgid」还是「这些全是别人、我们的流量压根没入账」。
// 带上字节数就一眼能分——量级对得上自测打的那几十 MB 的那个，就是我们自己。
func (c *Collector) observedTgids(limit int) ([]uint32, string) {
	if c == nil || c.counters == nil {
		return nil, "[]"
	}
	var cur, next uint32
	if err := c.counters.NextKey(nil, &next); err != nil {
		return nil, "[]"
	}
	keys := []uint32{next}
	for len(keys) < limit {
		cur = next
		if err := c.counters.NextKey(cur, &next); err != nil {
			break
		}
		keys = append(keys, next)
	}

	var b strings.Builder
	b.WriteByte('[')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		var v counters
		if err := c.counters.Lookup(k, &v); err != nil {
			fmt.Fprintf(&b, "%d=?", k)
			continue
		}
		fmt.Fprintf(&b, "%d:rx=%s,tx=%s", k, humanBytes(v.Rx), humanBytes(v.Tx))
	}
	b.WriteByte(']')
	return keys, b.String()
}

// humanBytes 只给诊断输出用，够读就行。
func humanBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d", n)
	}
}

// mapEntries 数一张 map 里有多少条目。只在诊断路径上调用（要遍历整张表）。
//
// 用 NextKey 而不是 Iterate：后者每取一个 key 还会 Lookup 一次 value，
// 而两张 map 的 value 类型不同（__u8 / struct counters），传 nil 会让迭代直接
// 报错退出——数出来恒为 0，是个会骗人的诊断。
func mapEntries(m *ebpf.Map) int {
	if m == nil {
		return -1
	}
	var cur, next uint32
	if err := m.NextKey(nil, &next); err != nil {
		return 0 // ErrKeyNotExist = 空表
	}
	for n := 1; n <= 512; n++ {
		cur = next
		if err := m.NextKey(cur, &next); err != nil {
			return n
		}
	}
	return 512 // max_entries 是 256，数到这儿说明遍历出问题了
}

// Bytes 返回该 PID 的**累计**收发字节，速率由调用方按 Δt 求差。
//
// 首次问到某个 PID 时先把它登记进 BPF 侧的 targets，并以 0 作为基线返回。
// 0 不是「猜的」：登记之前内核根本没为这个 tgid 计数，计数器就是从 0 开始的，
// 所以下一轮的差值恰好是这两次之间的流量。调用方那边这一帧仍然没有速率可算
// （没有 prev），与「首帧速率为 null」的既有约定一致（§3.1.1）。
func (c *Collector) Bytes(pid int32) (rx, tx uint64, ok bool) {
	if c == nil || pid <= 0 {
		return 0, 0, false
	}
	tgid := uint32(pid)

	c.mu.Lock()
	_, tracked := c.seen[tgid]
	c.seen[tgid] = time.Now()
	c.pruneLocked()
	c.mu.Unlock()

	if !tracked {
		if err := c.targets.Put(tgid, uint8(1)); err != nil {
			// 登记没成功就别留在 seen 里，否则下一轮会被当成「已登记」，
			// 然后一直读一个永远不会出现的计数条目
			logger.Debugf("procnet: 登记 PID %d 失败: %v", pid, err)
			c.mu.Lock()
			delete(c.seen, tgid)
			c.mu.Unlock()
			return 0, 0, false
		}
		return 0, 0, true
	}

	var v counters
	if err := c.counters.Lookup(tgid, &v); err != nil {
		if errors.Is(err, ebpf.ErrKeyNotExist) {
			// 已登记但一个包都没收发过——累计值就是 0，不是「采不到」
			return 0, 0, true
		}
		logger.Debugf("procnet: 读取 PID %d 的计数失败: %v", pid, err)
		return 0, 0, false
	}
	return v.Rx, v.Tx, true
}

// pruneLocked 淘汰一段时间没人问的 PID，两张 map 一起清。
// 因为 BPF 侧只统计登记过的 tgid，条目数天然被限死在被跟踪的实例数，
// 不需要在内核里挂 sched_process_exit。
func (c *Collector) pruneLocked() {
	now := time.Now()
	if now.Sub(c.lastPrune) < pruneInterval {
		return
	}
	c.lastPrune = now
	for tgid, last := range c.seen {
		if now.Sub(last) <= targetTTL {
			continue
		}
		delete(c.seen, tgid)
		_ = c.targets.Delete(tgid)
		_ = c.counters.Delete(tgid)
	}
}

// Describe 返回一行可读的加载结果，供调用方记日志。
// 诊断模式下还会带上每个探针的命中次数（见 probeHits）。
func (c *Collector) Describe() string {
	if c == nil {
		return ""
	}
	return c.desc + c.probeHits()
}

// Close 卸载探针并释放 map。进程退出前必须调用，避免残留内核对象。
func (c *Collector) Close() error {
	if c == nil {
		return nil
	}
	for _, l := range c.links {
		_ = l.Close()
	}
	c.links = nil
	if c.statsDone != nil {
		_ = c.statsDone.Close() // 关掉内核的 BPF 运行统计（全局开关，必须还回去）
		c.statsDone = nil
	}
	c.progs = nil
	if c.coll != nil {
		c.coll.Close()
		c.coll = nil
	}
	return nil
}

// kernelTypes 解析 Options.BTFPath。
// 返回 (nil, "", nil) 表示没配置——交给 cilium/ebpf 自己去读 /sys/kernel/btf/vmlinux。
func kernelTypes(path string) (*btf.Spec, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, "", nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if !st.IsDir() {
		spec, err := loadBTFFile(path)
		if err != nil {
			return nil, "", err
		}
		return spec, path, nil
	}

	release := kernelRelease()
	if release == "" {
		return nil, "", errors.New("读不到内核版本（/proc/sys/kernel/osrelease）")
	}
	id, versionID, idLike := readOSRelease()
	for _, pattern := range btfhubCandidates(path, release, "x86_64", append([]string{id}, idLike...), versionID) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, m := range matches {
			spec, err := loadBTFFile(m)
			if err != nil {
				logger.Debugf("procnet: %s 不是可用的 BTF: %v", m, err)
				continue
			}
			return spec, m, nil
		}
	}
	return nil, "", fmt.Errorf("在 %s 下没找到内核 %s 对应的 BTF", path, release)
}

// loadBTFFile 读一个 BTF 文件。btfhub 发布的是 .btf.tar.xz（一个归档里就一个 .btf），
// 用 tar 解到标准输出——Go 标准库没有 xz，为这一个场景引一个解压依赖不划算，
// 而 tar 本来就是 Linux 侧的既有前置（见 pkg/linuxdeps）。
func loadBTFFile(path string) (*btf.Spec, error) {
	if strings.HasSuffix(path, ".tar.xz") {
		raw, err := exec.Command("tar", "-xOJf", path).Output()
		if err != nil {
			return nil, fmt.Errorf("解压 %s 失败（需要 tar + xz）: %w", path, err)
		}
		return btf.LoadSpecFromReader(bytes.NewReader(raw))
	}
	return btf.LoadSpec(path)
}

func kernelRelease() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readOSRelease() (id, versionID string, idLike []string) {
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		if b, err := os.ReadFile(p); err == nil {
			return parseOSRelease(string(b))
		}
	}
	return "", "", nil
}
