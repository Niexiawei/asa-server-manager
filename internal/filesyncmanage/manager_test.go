package filesyncmanage

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// unreachableAddress 返回一个没有人监听的本机地址：节点会一直尝试连接，
// 这正好让这里只测 Manager 自己的生命周期逻辑，不依赖协调端。
func unreachableAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := Initialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop() })
	return m
}

// markEnrolled 在 m 的目录里放一张"节点证书"（借用 cfg 的引导证书，它同样由该 CA 签发），
// 让节点跳过 Enroll、直接进入同步库的断线重连循环——连不上协调端时节点一直活着，
// 这是测 Manager 热更新逻辑需要的稳定状态（没接入过的机器会先反复重试 Enroll，
// 见 TestUnenrolledNodeKeepsTryingAnUnreachableCoordinator）。
func markEnrolled(t *testing.T, m *Manager, cfg *Config) {
	t.Helper()
	if err := os.WriteFile(m.nodeCertPath(), []byte(cfg.BootstrapCertPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.nodeKeyPath(), []byte(cfg.BootstrapKeyPEM), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func rootIDs(m *Manager) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.roots))
	for id := range m.roots {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func TestStartWithoutConfigurationReportsNotConfigured(t *testing.T) {
	m := newTestManager(t)
	if err := m.Start(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Start = %v, want ErrNotConfigured", err)
	}
	if st := m.Status(); st.State != StateNotConfigured {
		t.Fatalf("state = %s", st.State)
	}
}

func TestDisabledConfigurationIsSavedButNotRun(t *testing.T) {
	m := newTestManager(t)
	cfg := validConfig(t, unreachableAddress(t))
	cfg.Enabled = false
	if err := m.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st.State != StateDisabled {
		t.Fatalf("state = %s, want disabled", st.State)
	}
	if m.node != nil {
		t.Fatal("a disabled configuration must not start a node")
	}
	if _, err := LoadConfig(m.runDir); err != nil {
		t.Fatalf("the configuration should still be saved: %v", err)
	}
}

// TestClusterEditsAreHotAndConnectionEditsRebuild 是 SetConfig 的核心：
// 只改集群列表不能重建节点（会打断其他集群的在途传输），改连接必须重建。
func TestClusterEditsAreHotAndConnectionEditsRebuild(t *testing.T) {
	m := newTestManager(t)
	cfg := validConfig(t, unreachableAddress(t))
	markEnrolled(t, m, cfg)
	if err := m.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st.State != StateConnecting {
		t.Fatalf("state = %s, want connecting (nobody listens at the address)", st.State)
	}
	started := m.generation.Load()

	added := cfg.clone()
	added.Clusters = append(added.Clusters, ClusterRoot{ClusterID: "beta"})
	if err := m.SetConfig(added); err != nil {
		t.Fatal(err)
	}
	if got := m.generation.Load(); got != started {
		t.Fatal("adding a cluster rebuilt the node")
	}
	if got := strings.Join(rootIDs(m), ","); got != "alpha,beta" {
		t.Fatalf("roots = %s, want alpha,beta", got)
	}
	if _, err := os.Stat(clusterDir(m.baseDir, "beta")); err != nil {
		t.Fatalf("the cluster directory was not created: %v", err)
	}

	removed := added.clone()
	removed.Clusters = []ClusterRoot{{ClusterID: "beta"}}
	if err := m.SetConfig(removed); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rootIDs(m), ","); got != "beta" || m.generation.Load() != started {
		t.Fatalf("removing a cluster: roots = %s, rebuilt = %v", got, m.generation.Load() != started)
	}

	moved := removed.clone()
	moved.Address = unreachableAddress(t)
	if err := m.SetConfig(moved); err != nil {
		t.Fatal(err)
	}
	if m.generation.Load() == started {
		t.Fatal("changing the address must rebuild the node")
	}
	if got := strings.Join(rootIDs(m), ","); got != "beta" {
		t.Fatalf("roots after rebuild = %s, want beta", got)
	}
}

func TestStopAndRestart(t *testing.T) {
	m := newTestManager(t)
	cfg := validConfig(t, unreachableAddress(t))
	markEnrolled(t, m, cfg)
	if err := m.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st.State != StateStopped {
		t.Fatalf("state after Stop = %s", st.State)
	}
	if err := m.Stop(); err == nil {
		t.Fatal("stopping twice should report that nothing is running")
	}
	if err := m.Restart(); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st.State != StateConnecting {
		t.Fatalf("state after Restart = %s", st.State)
	}
}

// TestStartWithoutAnyCredentialExplains：没接入过、也没给引导凭据时，
// 报错要告诉用户缺的是什么，而不是同步库那句笼统的凭据错误。
func TestStartWithoutAnyCredentialExplains(t *testing.T) {
	m := newTestManager(t)
	cfg := validConfig(t, unreachableAddress(t))
	cfg.BootstrapCertPEM, cfg.BootstrapKeyPEM = "", ""
	err := m.SetConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "引导凭据") {
		t.Fatalf("expected an error asking for bootstrap credentials, got %v", err)
	}
	if st := m.Status(); st.State != StateFailed {
		t.Fatalf("state = %s, want failed", st.State)
	}
}

func TestResetIdentityNeedsBootstrapAndRemovesTheNodeCertificate(t *testing.T) {
	m := newTestManager(t)
	cfg := validConfig(t, unreachableAddress(t))
	withoutBootstrap := cfg.clone()
	withoutBootstrap.BootstrapCertPEM, withoutBootstrap.BootstrapKeyPEM = "", ""
	if err := SaveConfig(m.runDir, withoutBootstrap); err != nil {
		t.Fatal(err)
	}
	m.cfg = withoutBootstrap
	for _, name := range []string{nodeCertFileName, nodeKeyFileName} {
		if err := os.WriteFile(filepath.Join(m.runDir, name), []byte("placeholder"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.ResetIdentity(); err == nil {
		t.Fatal("resetting without bootstrap credentials would leave the machine unable to rejoin")
	}
	if !m.hasNodeCertificate() {
		t.Fatal("a refused reset must not delete the node certificate")
	}

	m.cfg = cfg
	if err := m.ResetIdentity(); err != nil {
		t.Fatal(err)
	}
	if m.hasNodeCertificate() {
		t.Fatal("the node certificate should be gone after a reset")
	}
}

// TestUnenrolledNodeKeepsTryingAnUnreachableCoordinator：还没接入过的机器开机时连不上协调端，
// 同步库（v0.4.1 起）自己退避重试 Enroll。这里要求的是：节点不会停下变成 failed，
// 状态停在"连接中"，而且连不上的原因能在 last_connect_error 里看到。
func TestUnenrolledNodeKeepsTryingAnUnreachableCoordinator(t *testing.T) {
	m := newTestManager(t)
	if err := m.SetConfig(validConfig(t, unreachableAddress(t))); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the enrollment failure to be reported", func() bool {
		return m.Status().LastConnectErr != ""
	})
	time.Sleep(200 * time.Millisecond) // 足够让一个"首次失败就退出"的节点退出
	if st := m.Status(); st.State != StateConnecting || m.node == nil {
		t.Fatalf("state = %s (message %q), want the node still connecting", st.State, st.Message)
	}
}
