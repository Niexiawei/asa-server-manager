package process

import (
	cfgpkg "asa-server/internal/config"
	"path/filepath"
	"testing"
)

// withTempInstancesDir points cfgpkg.InstancesDir at a fresh temp directory
// for the duration of the test, restoring the previous value afterward —
// same pattern used by internal/mirror's tests.
func withTempInstancesDir(t *testing.T) {
	t.Helper()
	orig := cfgpkg.InstancesDir
	cfgpkg.InstancesDir = filepath.Join(t.TempDir(), "instances")
	t.Cleanup(func() { cfgpkg.InstancesDir = orig })
}

func TestSaveAndGetLauncherPID(t *testing.T) {
	withTempInstancesDir(t)

	if err := SaveLauncherPID("myinstance", 12345); err != nil {
		t.Fatalf("SaveLauncherPID: %v", err)
	}

	got, err := GetLauncherPID("myinstance")
	if err != nil {
		t.Fatalf("GetLauncherPID: %v", err)
	}
	if got != 12345 {
		t.Errorf("GetLauncherPID() = %d, want 12345", got)
	}
}

func TestGetLauncherPID_MissingFile(t *testing.T) {
	withTempInstancesDir(t)

	if _, err := GetLauncherPID("never-started"); err == nil {
		t.Fatal("expected an error reading launcher_pid for an instance that never started, got nil")
	}
}

// TestLauncherPID_IndependentFromInstancePID guards the reason
// launcher_pid is a distinct file rather than reusing "pid": the two must
// be independently settable (Linux's umu-run PID vs the real game PID —
// see docs/LINUX_COMPATIBILITY_PLAN.md §5.3).
func TestLauncherPID_IndependentFromInstancePID(t *testing.T) {
	withTempInstancesDir(t)

	if err := SaveInstancePID("myinstance", 111); err != nil {
		t.Fatalf("SaveInstancePID: %v", err)
	}
	if err := SaveLauncherPID("myinstance", 222); err != nil {
		t.Fatalf("SaveLauncherPID: %v", err)
	}

	gamePID, err := GetInstancePID("myinstance")
	if err != nil {
		t.Fatalf("GetInstancePID: %v", err)
	}
	launcherPID, err := GetLauncherPID("myinstance")
	if err != nil {
		t.Fatalf("GetLauncherPID: %v", err)
	}

	if gamePID != 111 {
		t.Errorf("GetInstancePID() = %d, want 111", gamePID)
	}
	if launcherPID != 222 {
		t.Errorf("GetLauncherPID() = %d, want 222", launcherPID)
	}
}
