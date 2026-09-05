package runner

import "testing"

// Model is what `asa-server perms status` prints and what setup's closing tip
// keys off, so the three states have to stay distinct. "chown" in particular
// must not be reported as a failure — it is a working, if degraded, regime.
func TestSharedAccessInfoModel(t *testing.T) {
	tests := []struct {
		name string
		info SharedAccessInfo
		want string
	}{
		{
			name: "not dropping privileges",
			info: SharedAccessInfo{Managed: false},
			want: "n/a",
		},
		{
			// A machine with the acl package and an ACL-capable filesystem:
			// group + setgid + default ACL, inheritance handled by the kernel.
			name: "acl available",
			info: SharedAccessInfo{Managed: true, ACLTool: "/usr/bin/setfacl"},
			want: "acl",
		},
		{
			name: "acl unavailable, degraded to chown",
			info: SharedAccessInfo{Managed: true, ACLError: "setfacl not found in PATH"},
			want: "chown",
		},
		{
			// Managed but nothing probed yet (runtime user missing, say):
			// still "chown", because that is what applySharedAccess would do.
			name: "managed with no probe result",
			info: SharedAccessInfo{Managed: true},
			want: "chown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.info.Model(); got != tt.want {
				t.Errorf("Model() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Not dropping privileges means there is nothing to share: no trees, and a
// zero-valued report. Windows always lands here.
func TestSharedAccessStatusUnmanaged(t *testing.T) {
	// getConfig() has no BaseDir in tests, and on Windows sharedAccessStatus
	// is a no-op returning the zero value, so this exercises the unmanaged
	// path on both platforms (on Linux CI it also needs euid != 0, the norm).
	if info := SharedAccessStatus(); !info.Managed {
		if info.Model() != "n/a" {
			t.Errorf("Model() = %q for an unmanaged report, want %q", info.Model(), "n/a")
		}
		if len(info.Trees) != 0 {
			t.Errorf("Trees = %v for an unmanaged report, want none", info.Trees)
		}
		if len(SharedTrees()) != 0 {
			t.Errorf("SharedTrees() = %v when not managing a runtime user, want none", SharedTrees())
		}
	}
}
