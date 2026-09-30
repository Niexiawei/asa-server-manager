//go:build linux

package wineprefix

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asa-server/pkg/umu"
)

// overlayFixture 建一个 overlay 模式的 Manager：umu-run、Proton、已初始化的共享底层都
// 摆好，EnsurePrefix 的前置检查能过。返回 Manager、它的 Config 与 key "a" 的 merged。
func overlayFixture(t *testing.T) (*Manager, Config, string) {
	t.Helper()
	const proton = "GE-Proton10-34"
	base := t.TempDir()
	writeFixture(t, filepath.Join(base, "umu-launcher", "umu-run"), "x", 0o755)
	writeFixture(t, filepath.Join(base, "proton", proton, "proton"), "x", 0o755)
	lower := filepath.Join(base, "umu-prefix")
	initPrefix(t, lower)
	writeFixture(t, filepath.Join(lower, umu.PrefixMarkerFile), proton+"\n", 0o644)

	cfg := Config{BaseDir: base, PrefixMode: "overlay", ProtonVersion: proton}
	m := newManager(cfg)
	merged := overlayMergedDir(cfg, "a")
	t.Cleanup(func() {
		if overlayMounted(merged) {
			_ = unmountOverlay(merged)
		}
	})
	return m, cfg, merged
}

// mustMountedLayer 让 key "a" 的可写层真的挂上 overlayfs；环境不支持（非 root、
// 内核没有 overlay、tmpfs 不能当 upperdir……）时跳过——那时走的是降级复制形态，
// 与这里要钉的「挂载形态」无关。
func mustMountedLayer(t *testing.T, m *Manager, merged string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root to mount overlayfs")
	}
	if err := m.EnsurePrefix(context.Background(), "a", nil); err != nil {
		t.Fatalf("EnsurePrefix: %v", err)
	}
	if !overlayMounted(merged) {
		t.Skip("overlayfs could not be mounted here (fell back to a copy)")
	}
}

// 回归 docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §3.2：宿主机重启后可写层没挂着、
// merged 是个空挂载点，而 upper 还在。这种静息形态以前直接落进「清空重建」，
// 连日志都没有；底层没变时它必须原样重新挂上。
func TestEnsureOverlayRemountsRestingLayer(t *testing.T) {
	m, _, merged := overlayFixture(t)
	mustMountedLayer(t, m, merged)

	sentinel := filepath.Join(merged, "written-before-reboot")
	writeFixture(t, sentinel, "x", 0o644)          // 经由挂载写入 → 落在 upper
	if err := unmountOverlay(merged); err != nil { // 模拟宿主机重启
		t.Fatal(err)
	}

	if err := m.EnsurePrefix(context.Background(), "a", nil); err != nil {
		t.Fatalf("EnsurePrefix after reboot: %v", err)
	}
	if !overlayMounted(merged) {
		t.Fatal("resting layer was not mounted again")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("the layer's upper was wiped instead of remounted: %v", err)
	}
}

// 回归 §3.2 P1-7：写底层的窗口打开期间不许挂任何可写层——挂上去就是对一个正在被
// 修改的 lowerdir 做 overlay，overlayfs 明确的未定义行为。要立刻失败并说出是谁在改，
// 窗口关闭后恢复正常。不需要 root：拒绝发生在挂载之前。
func TestPrepareSharedWriteBlocksLayerMounts(t *testing.T) {
	m, _, _ := overlayFixture(t)

	done, err := m.PrepareSharedWrite("单测里的底层修改")
	if err != nil {
		t.Fatalf("PrepareSharedWrite with no layers: %v", err)
	}
	err = m.EnsurePrefix(context.Background(), "a", nil)
	if err == nil || !strings.Contains(err.Error(), "单测里的底层修改") {
		t.Fatalf("EnsurePrefix during a write window: got %v, want an error naming the operation", err)
	}

	done()
	done() // 幂等：第二次不能再 Unlock 一次
	if err := m.EnsurePrefix(context.Background(), "a", nil); err != nil {
		t.Fatalf("EnsurePrefix after the window closed: %v", err)
	}
}

// 回归 §3.2 P1-7：层已挂上、游戏的 wineserver 还没起来时，只有租约能说明它在用。
// 持有租约期间 PrepareSharedWrite 必须拒绝且不卸载；释放后才把它当空闲卸掉。
func TestPrepareSharedWriteRespectsLayerLease(t *testing.T) {
	m, _, merged := overlayFixture(t)
	mustMountedLayer(t, m, merged)

	release := m.HoldLayer("a")
	if done, err := m.PrepareSharedWrite("单测"); err == nil {
		done()
		t.Fatal("PrepareSharedWrite unmounted / ignored a leased layer")
	}
	if !overlayMounted(merged) {
		t.Fatal("a leased layer was unmounted")
	}

	release()
	release() // 幂等
	done, err := m.PrepareSharedWrite("单测")
	if err != nil {
		t.Fatalf("PrepareSharedWrite after the lease was released: %v", err)
	}
	done()
	if overlayMounted(merged) {
		t.Fatal("an idle, unleased layer should have been unmounted")
	}
}
