package mirror

import (
	"path/filepath"
	"slices"
	"testing"

	cfgpkg "asa-server/internal/config"
)

// ExceptionTargets is what the launch path iterates to hand every junction
// target over to the dropped runtime user. Missing an entry here means the
// game can see a directory but not write into it, which is exactly the failure
// docs/ACL_PERMISSION_HARDENING_PLAN.md §3.2 catalogues — so the set itself is
// worth pinning.
func TestExceptionTargets(t *testing.T) {
	base := t.TempDir()
	oldInstances, oldServerFiles := cfgpkg.InstancesDir, cfgpkg.ServerFilesDir
	cfgpkg.InstancesDir = filepath.Join(base, "instances")
	cfgpkg.ServerFilesDir = filepath.Join(base, "server-files")
	t.Cleanup(func() {
		cfgpkg.InstancesDir, cfgpkg.ServerFilesDir = oldInstances, oldServerFiles
	})

	got := ExceptionTargets("alpha", &cfgpkg.InstanceConfig{SaveDir: "SaveAlpha"})

	want := []string{
		filepath.Join(cfgpkg.InstancesDir, "alpha", "Config"),
		filepath.Join(cfgpkg.InstancesDir, "alpha", "Logs"),
		filepath.Join(cfgpkg.InstancesDir, "alpha", "Save"),
		filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath)),
	}
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("ExceptionTargets =\n  %v\nwant\n  %v", got, want)
	}
}

// The Mods/ModsUserData target lives under server-files, not under the
// instance — it is shared by every instance. Missing it is what produced the
// "LogCFCore: Unable to create a directory .../ModsUserData" hang.
func TestExceptionTargetsIncludesSharedModsDir(t *testing.T) {
	base := t.TempDir()
	oldInstances, oldServerFiles := cfgpkg.InstancesDir, cfgpkg.ServerFilesDir
	cfgpkg.InstancesDir = filepath.Join(base, "instances")
	cfgpkg.ServerFilesDir = filepath.Join(base, "server-files")
	t.Cleanup(func() {
		cfgpkg.InstancesDir, cfgpkg.ServerFilesDir = oldInstances, oldServerFiles
	})

	shared := filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath))
	for _, instance := range []string{"alpha", "beta"} {
		got := ExceptionTargets(instance, &cfgpkg.InstanceConfig{})
		if !slices.Contains(got, shared) {
			t.Errorf("ExceptionTargets(%q) = %v, missing the shared mods dir %s", instance, got, shared)
		}
	}
}

// An empty SaveDir falls back to the instance name (buildExceptionTargets'
// existing rule); the result must still be a sorted, duplicate-free list.
func TestExceptionTargetsSortedAndDeduped(t *testing.T) {
	base := t.TempDir()
	oldInstances, oldServerFiles := cfgpkg.InstancesDir, cfgpkg.ServerFilesDir
	cfgpkg.InstancesDir = filepath.Join(base, "instances")
	cfgpkg.ServerFilesDir = filepath.Join(base, "server-files")
	t.Cleanup(func() {
		cfgpkg.InstancesDir, cfgpkg.ServerFilesDir = oldInstances, oldServerFiles
	})

	got := ExceptionTargets("alpha", &cfgpkg.InstanceConfig{})
	if !slices.IsSorted(got) {
		t.Errorf("ExceptionTargets = %v, want sorted output for reproducible logs", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i] == got[i-1] {
			t.Errorf("ExceptionTargets = %v, contains a duplicate", got)
			break
		}
	}
}
