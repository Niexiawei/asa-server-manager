//go:build linux

package sysuser

import (
	"os"
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
