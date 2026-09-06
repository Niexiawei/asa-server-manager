/* bpf_min.h —— 自带的最小 BPF 头，只声明同目录 procnet.c 真正用到的东西。
 *
 * 为什么不 vendor libbpf 的 bpf_helpers.h / bpf_tracing.h + 生成 vmlinux.h：
 *   1. **开发机是 Windows**（本项目的主平台）。vmlinux.h 要从目标内核的 BTF 生成、
 *      bpf_tracing.h 的 PT_REGS_* 要靠 vmlinux.h 给出 struct pt_regs——在 Windows 上
 *      这两样都拿不到，而 `.o` 就是在这台机器上用 clang -target bpf 编出来提交进仓库的。
 *   2. procnet.c 只用 4 个 helper、不访问任何内核结构体字段（见下），
 *      为此拖进 bpf_helper_defs.h 那 4000 行没有收益。
 *
 * 这里出现的每个数字都是**内核 ABI**，不随版本变化：helper 编号定义在
 * uapi/linux/bpf.h 的 __BPF_FUNC_MAPPER 里且只增不改；BPF_MAP_TYPE_HASH / BPF_NOEXIST
 * 同样是 uapi 常量。struct pt_regs 是 x86_64 的寄存器保存布局
 * （arch/x86/include/uapi/asm/ptrace.h），本文件只在 amd64 上编译（见 procnet_linux.go 的
 * build tag），所以不需要按架构分支。
 */
#ifndef ASA_PROCNET_BPF_MIN_H
#define ASA_PROCNET_BPF_MIN_H

typedef unsigned char __u8;
typedef unsigned int __u32;
typedef unsigned long long __u64;

#define SEC(name) __attribute__((section(name), used))
#define __always_inline inline __attribute__((always_inline))

/* BTF 风格 map 定义用的两个宏（与 libbpf bpf_helpers.h 中同名宏一致）。
 * 它们把 map 的属性编码成结构体成员的类型，由 clang -g 写进 .BTF 段，
 * cilium/ebpf 解析 .maps 段时读的就是这份 BTF。 */
#define __uint(name, val) int (*name)[val]
#define __type(name, val) typeof(val) *name

/* uapi/linux/bpf.h */
#define BPF_MAP_TYPE_HASH 1
#define BPF_NOEXIST 1

/* helper 编号即内核 ABI */
static void *(*bpf_map_lookup_elem)(void *map, const void *key) = (void *)1;
static long (*bpf_map_update_elem)(void *map, const void *key, const void *value,
                                   __u64 flags) = (void *)2;
static __u64 (*bpf_get_current_pid_tgid)(void) = (void *)14;

/* kprobe 程序的 ctx 就是 struct pt_regs *，verifier 允许在 sizeof(struct pt_regs)
 * 范围内直接读取（kprobe_prog_is_valid_access），不需要 bpf_probe_read，
 * 也因此**不需要 CO-RE 重定位、不需要目标机内核 BTF**。 */
struct pt_regs {
    unsigned long r15, r14, r13, r12, bp, bx;
    unsigned long r11, r10, r9, r8, ax, cx, dx, si, di;
    unsigned long orig_ax, ip, cs, flags, sp, ss;
};

/* System V AMD64 调用约定的前三个整型参数与返回值 */
#define PT_REGS_PARM1(x) ((x)->di)
#define PT_REGS_PARM2(x) ((x)->si)
#define PT_REGS_PARM3(x) ((x)->dx)
#define PT_REGS_RC(x) ((x)->ax)

#endif /* ASA_PROCNET_BPF_MIN_H */
