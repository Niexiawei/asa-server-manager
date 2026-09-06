//go:build linux && amd64

package procnet

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

const (
	// targetTTL 目标进程在 BPF 侧的登记有效期。Bytes 每个采样周期（2s）被问一次，
	// 30s = 连续 15 轮没人问才淘汰，足以容忍一次卡顿。
	targetTTL = 30 * time.Second

	// pruneInterval 两次淘汰扫描的最小间隔——每次 Bytes 都扫一遍没必要。
	pruneInterval = 10 * time.Second
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

	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(bpfObject))
	if err != nil {
		return nil, fmt.Errorf("解析内嵌 BPF 对象失败: %w", err)
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

	coll, err := ebpf.NewCollectionWithOptions(spec, copts)
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
		attached = append(attached, p.symbol)
	}
	if len(attached) == 0 {
		c.Close()
		return nil, errors.New("六个探针一个都没挂上")
	}

	c.desc = fmt.Sprintf("已挂载 %d/%d 个探针 [%s]，BTF 来源：%s",
		len(attached), len(probes), strings.Join(attached, " "), btfNote)
	return c, nil
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
func (c *Collector) Describe() string {
	if c == nil {
		return ""
	}
	return c.desc
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
