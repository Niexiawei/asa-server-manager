# ASA Server Manager 的辅助构建目标。
#
# 常规构建**不需要 make**（`go build` / `cd app && npm run build` 就够了）。
# 这个文件只为一件事存在：pkg/procnet 的 eBPF 对象 procnet_amd64.o。
#
# 它是**编译产物却提交进了仓库**——为的是常规 go build 不必装 clang/llvm
# （见 docs/RESOURCE_RATE_CHART_PLAN.md §11.2 决策 25）。代价是改了 BPF 源之后
# 必须记得重新生成，`make bpf` 就是那条命令；CI 也会在 .c/.h 变更时自动跑一次
# 并把结果提交回来（.github/workflows/bpf.yml）。
#
# 依赖：clang（带 bpf target，LLVM 官方发行版默认就有）+ llvm-strip。
# 二者都可以覆盖：`make bpf CLANG=clang-18 LLVM_STRIP=llvm-strip-18`。
#
# ⚠️ 开发机是 Windows 且没有 make 时，等价入口是 `go generate ./pkg/procnet/...`
# （指令写在 pkg/procnet/procnet.go 顶部）。**两处的编译参数必须保持一致**，
# 改了这里记得同步过去。

CLANG      ?= clang
LLVM_STRIP ?= llvm-strip

BPF_DIR := pkg/procnet/bpf
BPF_SRC := $(BPF_DIR)/procnet.c
BPF_HDR := $(BPF_DIR)/bpf_min.h
BPF_OBJ := $(BPF_DIR)/procnet_amd64.o

# -target bpf 选后端；-g **必须留着**——BTF 风格的 map 定义靠它写进 .BTF 段，
# 去掉就没有 .maps 的类型信息，cilium/ebpf 加载不了。
# -mcpu=v1 对齐基线内核 5.4：更高的 cpu 版本会用到 5.4 还没有的指令。
BPF_CFLAGS ?= -O2 -g -Wall -Werror -target bpf -mcpu=v1

.DEFAULT_GOAL := help

.PHONY: help
help:
	@echo "make bpf        按需重新生成 $(BPF_OBJ)（源比产物新时才编）"
	@echo "make bpf-force  无条件重新生成"
	@echo "make bpf-clean  删除产物"
	@echo ""
	@echo "当前工具链：CLANG=$(CLANG)  LLVM_STRIP=$(LLVM_STRIP)"

.PHONY: bpf
bpf: $(BPF_OBJ)

# 只在 .c / .h 比 .o 新时才重编。这不只是省时间：不同版本、不同发行版的 clang
# 编出来的字节并不相同（指令选择与 BTF 编码都可能变），无条件重编会让这个
# 已提交的产物在每台机器之间来回抖动。
$(BPF_OBJ): $(BPF_SRC) $(BPF_HDR)
	$(CLANG) $(BPF_CFLAGS) -c $(BPF_SRC) -o $@
	$(LLVM_STRIP) -g $@
	@echo "已生成 $@ （llvm-strip -g 去掉 DWARF、保留 .BTF）"

# 写成「删了再递归调一次」而不是 `bpf-force: bpf-clean bpf`：
# 并行 make（-j）下同一个目标的多个前置没有先后保证，那种写法可能先编再删。
.PHONY: bpf-force
bpf-force:
	rm -f $(BPF_OBJ)
	$(MAKE) bpf

.PHONY: bpf-clean
bpf-clean:
	rm -f $(BPF_OBJ)
