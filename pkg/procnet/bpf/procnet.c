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

static __always_inline void account(__u64 bytes, int is_rx) {
    if (bytes == 0)
        return;

    __u32 tgid = (__u32)(bpf_get_current_pid_tgid() >> 32);
    if (!bpf_map_lookup_elem(&procnet_targets, &tgid))
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
