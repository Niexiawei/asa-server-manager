/* procnet.c —— 按 tgid 统计 TCP/UDP 收发字节的 kprobe 程序。
 *
 * 目标：给 pkg/serverinfo 提供「单个游戏进程的网络收发」这个 gopsutil 给不了的量
 * （Process.NetIOCounters 在 Windows 未实现，在 Linux 上是网络 namespace 级）。
 * 设计与取舍见 docs/RESOURCE_RATE_CHART_PLAN.md §2.2。
 *
 * 挂点用 kprobe/kretprobe 而不是 fentry：基线内核是 5.4，x86 的 BPF trampoline
 * 要 5.5 才有。ARK 的游戏流量是 UDP，所以 UDP 四条路径必须覆盖，只挂 TCP 会得到一条恒零的曲线。
 *
 * 编译（产物已提交进仓库，只有改本文件时才需要 clang）：
 *   见 procnet_linux.go 顶部的 //go:generate
 */
#include "bpf_min.h"

struct counters {
    __u64 rx;
    __u64 tx;
};

/* procnet_targets：用户态登记的「要统计哪些 tgid」。探针第一件事就是查它，
 * 没命中立刻返回。
 *
 * 这一步不是优化而是设计的承重件：没有它，机器上**每个**进程的每次收发都会往
 * counters 里塞条目，map 迟早被撑满（这时新条目插不进去，倒霉的可能正是我们要看的
 * 那个游戏进程），并且得另挂 sched_process_exit 才能淘汰。先按目标过滤之后，
 * 两张 map 的条目数都被限死在「被跟踪的实例数」，淘汰交给用户态的 TTL 即可。 */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);
    __type(value, __u8);
    __uint(max_entries, 256);
} procnet_targets SEC(".maps");

/* procnet_counters：每个目标 tgid 的**累计**收发字节。速率由用户态按 Δt 求差，
 * 与磁盘/网卡指标的处理一致。 */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);
    __type(value, struct counters);
    __uint(max_entries, 256);
} procnet_counters SEC(".maps");

/* tracked：这个 tgid 要不要统计。
 *
 * key 0 是**诊断用的全捕获哨兵**：targets 里存在它时，所有 tgid 一律计数。
 * tgid 0 是 swapper/idle，永远走不到这些 socket 路径，拿它当哨兵不会和真实目标撞车。
 * 只有 `asa-server netmon`（procnet.Options.Diagnostics）会写这个 key，服务进程不会。
 *
 * 为什么需要它：探针「挂上了」「跑了」都能从用户态看到，唯独
 * 「内核眼里这笔流量属于哪个 tgid」看不到——而用户态登记的 PID 与内核看到的 tgid
 * 一旦不是同一个（典型是进程在 PID namespace 里），表现就是探针命中数一直涨、
 * counters 却永远是空的，没有任何线索。开了哨兵之后 counters 里的 key
 * 就是内核的答案，一眼能和 os.Getpid() 对上。 */
#ifdef PROCNET_NS_PID
/* ---- PID namespace 感知（只编进 procnet_ns_amd64.o） ----
 *
 * bpf_get_current_pid_tgid() 给的是**初始 namespace** 的 tgid。asa-server 跑在容器
 * （或任何 PID namespace）里时，用户态拿到的 PID 与它不是同一个空间：登记的 key
 * 永远匹配不上，表现为探针命中数一直涨、counters 里全是别的 tgid。
 * 2026-09-07 真机实测就是这个形态（本进程 PID 14597，内核只报 913/925/958/15036）。
 *
 * bpf_get_ns_current_pid_tgid(dev, ino, ...) 正是为此而设：给它
 * /proc/self/ns/pid 的 (st_dev, st_ino)，它返回**那个 namespace 里**的 pid/tgid，
 * 与用户态的 os.Getpid() 同一个空间。
 *
 * 这个 helper 要 **kernel 5.7+**，而本项目的基线是 5.4——用了它的程序在 5.4 上
 * 会被 verifier 直接拒绝加载（未知 helper）。所以它单独编成一个 .o，
 * 用户态先试它、失败再退回基础版（procnet_linux.go 的 Load）。
 * 拿不到 dev/ino 时 cfg->ino == 0，同样退回 bpf_get_current_pid_tgid()。 */
struct pidns_cfg {
    __u64 dev;
    __u64 ino;
};

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, __u32);
    __type(value, struct pidns_cfg);
    __uint(max_entries, 1);
} procnet_pidns SEC(".maps");

struct bpf_pidns_info {
    __u32 pid;
    __u32 tgid;
};

static long (*bpf_get_ns_current_pid_tgid)(__u64 dev, __u64 ino,
                                           struct bpf_pidns_info *nsdata,
                                           __u32 size) = (void *)120;
#endif /* PROCNET_NS_PID */

/* current_tgid：当前任务的 tgid，**与用户态处在同一个 PID namespace**。 */
static __always_inline __u32 current_tgid(void) {
#ifdef PROCNET_NS_PID
    __u32 zero = 0;
    struct pidns_cfg *cfg = bpf_map_lookup_elem(&procnet_pidns, &zero);
    if (cfg && cfg->ino) {
        struct bpf_pidns_info info = {};
        if (bpf_get_ns_current_pid_tgid(cfg->dev, cfg->ino, &info, sizeof(info)) == 0)
            return info.tgid;
    }
#endif
    return (__u32)(bpf_get_current_pid_tgid() >> 32);
}

static __always_inline int tracked(__u32 tgid) {
    if (bpf_map_lookup_elem(&procnet_targets, &tgid))
        return 1;
    __u32 any = 0;
    return bpf_map_lookup_elem(&procnet_targets, &any) != 0;
}

static __always_inline void account(__u64 bytes, int is_rx) {
    if (bytes == 0)
        return;

    __u32 tgid = current_tgid();
    if (!tracked(tgid))
        return;

    struct counters *c = bpf_map_lookup_elem(&procnet_counters, &tgid);
    if (!c) {
        struct counters zero = {0};
        bpf_map_update_elem(&procnet_counters, &tgid, &zero, BPF_NOEXIST);
        c = bpf_map_lookup_elem(&procnet_counters, &tgid);
        if (!c)
            return;
    }

    /* 同一个 tgid 的多个线程会并发命中，必须用原子加 */
    if (is_rx)
        __sync_fetch_and_add(&c->rx, bytes);
    else
        __sync_fetch_and_add(&c->tx, bytes);
}

/* tcp_sendmsg(struct sock *sk, struct msghdr *msg, size_t size)
 * 取的是**请求发送**的字节数，不是最终上线的字节数（不含 TCP/IP 头，发送失败时略偏大）。
 * bcc 的 tcptop 也是这么取的，作为「这个进程有多活跃」的信号足够。 */
SEC("kprobe/tcp_sendmsg")
int kprobe_tcp_sendmsg(struct pt_regs *ctx) {
    account((__u64)PT_REGS_PARM3(ctx), 0);
    return 0;
}

/* tcp_cleanup_rbuf(struct sock *sk, int copied) —— 收方向取已拷给用户态的字节数。
 * 挂它而不是 tcp_recvmsg：后者的 len 是缓冲区大小不是实收量。 */
SEC("kprobe/tcp_cleanup_rbuf")
int kprobe_tcp_cleanup_rbuf(struct pt_regs *ctx) {
    int copied = (int)PT_REGS_PARM2(ctx);
    if (copied > 0)
        account((__u64)copied, 1);
    return 0;
}

/* udp_sendmsg / udpv6_sendmsg(struct sock *sk, struct msghdr *msg, size_t len) */
SEC("kprobe/udp_sendmsg")
int kprobe_udp_sendmsg(struct pt_regs *ctx) {
    account((__u64)PT_REGS_PARM3(ctx), 0);
    return 0;
}

SEC("kprobe/udpv6_sendmsg")
int kprobe_udpv6_sendmsg(struct pt_regs *ctx) {
    account((__u64)PT_REGS_PARM3(ctx), 0);
    return 0;
}

/* udp_recvmsg / udpv6_recvmsg 走 **kretprobe**：返回值才是实际拷贝的字节数
 * （入参 len 只是缓冲区大小）。负值是 errno，忽略。
 * 取返回值还有个好处：这两个函数的形参在 5.19 删掉了 noblock，用 kretprobe 不受影响。 */
SEC("kretprobe/udp_recvmsg")
int kretprobe_udp_recvmsg(struct pt_regs *ctx) {
    int n = (int)PT_REGS_RC(ctx);
    if (n > 0)
        account((__u64)n, 1);
    return 0;
}

SEC("kretprobe/udpv6_recvmsg")
int kretprobe_udpv6_recvmsg(struct pt_regs *ctx) {
    int n = (int)PT_REGS_RC(ctx);
    if (n > 0)
        account((__u64)n, 1);
    return 0;
}

/* 用到的 helper 都不是 GPL-only，双许可即可 */
char _license[] SEC("license") = "Dual MIT/GPL";
