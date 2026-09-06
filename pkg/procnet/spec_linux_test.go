//go:build linux && amd64

package procnet

import (
	"bytes"
	"testing"

	"github.com/cilium/ebpf"
)

// 这个测试**不加载进内核**（那要 root，也要真机内核），只解析内嵌的
// bpf/procnet_amd64.o，确认它仍然是我们期望的那个对象。
//
// 存在的理由：那个 .o 是编译产物却提交进了仓库，重新生成时用错编译参数
// （比如漏掉 -g 导致没有 .BTF、map 定义失效）或改名了某个探针函数，
// 在 Linux 真机上跑起来之前不会有任何提示。这里把契约钉死在编译期之后、
// 部署之前，CI 的 make bpf 之后就跑它。
func TestEmbeddedObjectSpec(t *testing.T) {
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(bpfObject))
	if err != nil {
		t.Fatalf("解析内嵌 BPF 对象失败（.o 是不是用错参数重新生成过？）: %v", err)
	}

	maps := map[string]struct {
		keySize, valueSize, maxEntries uint32
	}{
		// key = tgid(u32)，value = u8 标记
		"procnet_targets": {keySize: 4, valueSize: 1, maxEntries: 256},
		// key = tgid(u32)，value = struct counters{u64 rx; u64 tx}
		"procnet_counters": {keySize: 4, valueSize: 16, maxEntries: 256},
	}
	for name, want := range maps {
		m, ok := spec.Maps[name]
		if !ok {
			t.Errorf("对象里缺少 map %s", name)
			continue
		}
		if m.Type != ebpf.Hash {
			t.Errorf("%s 的类型 = %v, want Hash", name, m.Type)
		}
		if m.KeySize != want.keySize || m.ValueSize != want.valueSize {
			t.Errorf("%s 的 key/value 大小 = %d/%d, want %d/%d（与 procnet.c 里的结构体对不上，Go 侧的 counters 也要跟着改）",
				name, m.KeySize, m.ValueSize, want.keySize, want.valueSize)
		}
		if m.MaxEntries != want.maxEntries {
			t.Errorf("%s 的 max_entries = %d, want %d", name, m.MaxEntries, want.maxEntries)
		}
	}

	// probes 是运行时真正会去查的名字，对象里少一个就等于少一条曲线
	for _, p := range probes {
		prog, ok := spec.Programs[p.prog]
		if !ok {
			t.Errorf("对象里缺少程序 %s（挂点 %s）", p.prog, p.symbol)
			continue
		}
		if prog.Type != ebpf.Kprobe {
			t.Errorf("%s 的类型 = %v, want Kprobe（kretprobe 在 BPF 侧也是 Kprobe 类型）", p.prog, prog.Type)
		}
		// SEC("kprobe/<符号>") 里的符号必须与 probes 表一致，否则 C 侧改了挂点
		// 而 Go 侧没跟上时，只会在真机上表现为「这条曲线一直是 0」
		if prog.AttachTo != p.symbol {
			t.Errorf("%s 的挂点 = %q, want %q", p.prog, prog.AttachTo, p.symbol)
		}
	}
}
