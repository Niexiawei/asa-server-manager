//go:build linux

package sysuser

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func TestConfig_UserNameDefault(t *testing.T) {
	if got := New(Config{}).UserName(); got != DefaultName {
		t.Errorf("UserName() with no override = %q, want %q", got, DefaultName)
	}
	if got := New(Config{Name: "custom-runtime"}).UserName(); got != "custom-runtime" {
		t.Errorf("UserName() with override = %q, want custom-runtime", got)
	}
}

func TestManaged_FalseWhenRunAsRoot(t *testing.T) {
	// RunAsRoot must disable management regardless of euid — the one branch
	// this test can assert unconditionally, root or not.
	if New(Config{RunAsRoot: true}).Managed() {
		t.Error("Managed() with RunAsRoot=true must be false even as root")
	}
}

func TestManaged_MatchesEuidWhenNotOptedOut(t *testing.T) {
	want := os.Geteuid() == 0
	if got := New(Config{}).Managed(); got != want {
		t.Errorf("Managed() = %v, want %v (euid=%d)", got, want, os.Geteuid())
	}
}

func TestHomeDir_FallsBackToProcessHomeWhenNotManaged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("this test asserts the not-managed branch")
	}
	want, _ := os.UserHomeDir()
	if got := New(Config{}).HomeDir(); got != want {
		t.Errorf("HomeDir() = %q, want this process's own home %q", got, want)
	}
}

func TestChildIDs_ZeroWhenNotManaged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("this test asserts the not-managed branch")
	}
	uid, gid, managed := New(Config{}).ChildIDs()
	if managed || uid != 0 || gid != 0 {
		t.Errorf("ChildIDs() = (%d, %d, %v), want (0, 0, false)", uid, gid, managed)
	}
}

// TestChownTreeAs_SelfOwnedIsANoopWalk exercises the walk/skip machinery
// without needing root: chowning to the current process's own uid/gid is
// always permitted, so this only proves the walk completes and leaves the
// tree readable — not the privileged "different owner" path, which is
// covered by internal/runner's opt-in root test (ASA_TEST_RUNTIME_USER=1).
func TestChownTreeAs_SelfOwnedIsANoopWalk(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	uid := os.Getuid()
	gid := os.Getgid()
	if err := ChownTreeAs(uid, gid, dir); err != nil {
		t.Fatalf("ChownTreeAs to own uid/gid: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nested, "f")); err != nil {
		t.Fatalf("tree unreadable after ChownTreeAs: %v", err)
	}
}

func TestChownTreeAs_MissingPathSkipped(t *testing.T) {
	if err := ChownTreeAs(os.Getuid(), os.Getgid(), filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Errorf("ChownTreeAs on a missing path: want nil, got %v", err)
	}
}

// ownerDrift is the shared judgement behind Problems' owner-drift check and
// OwnerDrift's advisory report. Needs no root: the temp tree is owned by the
// test's own uid, so "want that uid" is clean and "want any other uid" drifts.
func TestOwnerDrift(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "does-not-exist")
	me := os.Getuid()

	if d, bad := ownerDrift(me, []string{missing, dir}); bad != "" {
		t.Errorf("own tree reported as drifted: dir=%q sample=%q", d, bad)
	}
	d, bad := ownerDrift(me+1, []string{missing, dir})
	if d != dir || bad == "" {
		t.Errorf("foreign-owned tree not reported: dir=%q sample=%q, want dir=%q", d, bad, dir)
	}
}

// OwnerDrift is advisory-only and must stay silent when there is no managed
// user to compare against.
func TestOwnerDrift_NoopWhenNotManaged(t *testing.T) {
	if d, bad := New(Config{RunAsRoot: true}).OwnerDrift(t.TempDir()); d != "" || bad != "" {
		t.Errorf("OwnerDrift with RunAsRoot=true = (%q, %q), want empty", d, bad)
	}
}

// effectiveHome 是「受管账号实际的 HOME」唯一的判定：passwd 家目录为空或为 "/"
// （nobody 之类的系统账号）时用 HomeFallback。
func TestEffectiveHome(t *testing.T) {
	m := New(Config{HomeFallback: "/srv/asa/runtime-home"})
	cases := map[string]string{
		"":             "/srv/asa/runtime-home",
		"/":            "/srv/asa/runtime-home",
		"/home/asa/":   "/home/asa",
		"/var/lib/asa": "/var/lib/asa",
	}
	for in, want := range cases {
		if got := m.effectiveHome(&user.User{HomeDir: in}); got != want {
			t.Errorf("effectiveHome(%q) = %q, want %q", in, got, want)
		}
	}
}

// 回归 docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §2.2 P1-3：受管账号解析为 uid 0
// 时，凭证解析与自检都必须拒绝——否则游戏以 root 运行，而所有状态都报告「已降权」。
// 只读：root 账号本来就存在，不会创建任何东西。
func TestRootAccountIsRefused(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root (Managed() is false otherwise)")
	}
	m := New(Config{Name: "root"})
	if cred, _, err := m.ResolveCredential(); err == nil {
		t.Fatalf("ResolveCredential for root succeeded with %+v", cred)
	}
	if err := m.EnsureUser(context.Background()); err == nil {
		t.Fatal("EnsureUser for root succeeded")
	}
	probs := m.Problems(AccessCheck{}, false)
	if len(probs) != 1 || probs[0].Name != "umu-runtime-user-is-root" {
		t.Fatalf("Problems for root = %+v, want exactly umu-runtime-user-is-root", probs)
	}
}
