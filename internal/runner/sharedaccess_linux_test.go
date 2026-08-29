//go:build linux

package runner

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

// TestChgrpSetgidTree checks the three things the shared-access design rests
// on: the group is set, group read/write is granted (with execute only where
// `chmod g+X` would grant it), and directories come out setgid so entries
// created later inherit the group no matter who creates them.
//
// It chgrps to the test process's own gid, which needs no privileges.
func TestChgrpSetgidTree(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(sub, "plain.txt")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(sub, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sub, "link")
	if err := os.Symlink(plain, link); err != nil {
		t.Fatal(err)
	}

	gid := os.Getgid()
	dirs, err := chgrpSetgidTree(root, gid)
	if err != nil {
		t.Fatalf("chgrpSetgidTree: %v", err)
	}

	if !slices.Contains(dirs, root) || !slices.Contains(dirs, sub) {
		t.Errorf("returned dirs = %v, want both %s and %s", dirs, root, sub)
	}
	if slices.Contains(dirs, plain) {
		t.Errorf("returned dirs = %v, must not contain the regular file %s", dirs, plain)
	}

	for _, dir := range []string{root, sub} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSetgid == 0 {
			t.Errorf("%s: setgid bit not set (mode %v)", dir, info.Mode())
		}
		if info.Mode().Perm()&0o070 != 0o070 {
			t.Errorf("%s: want g+rwx, got %v", dir, info.Mode().Perm())
		}
	}

	// A file with no owner-execute must not gain group-execute — that's the
	// "X" in `chmod g+rwX`, and blanket g+x on data files would be wrong.
	if info, err := os.Stat(plain); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got&0o060 != 0o060 || got&0o010 != 0 {
		t.Errorf("%s: want g+rw and not g+x, got %v", plain, got)
	}

	// An already-executable file keeps (and shares) its executability.
	if info, err := os.Stat(script); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got&0o070 != 0o070 {
		t.Errorf("%s: want g+rwx, got %v", script, got)
	}

	// The symlink's own group is set; its target must not be followed.
	if info, err := os.Lstat(link); err != nil {
		t.Fatal(err)
	} else if info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("%s stopped being a symlink", link)
	} else if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Gid) != gid {
		t.Errorf("%s: gid = %d, want %d", link, st.Gid, gid)
	}
}

// TestSharedAccessNeeded is the cheap probe that decides whether the full
// (expensive) pass has to run at all.
func TestSharedAccessNeeded(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	gid := os.Getgid()
	if !sharedAccessNeeded(root, gid) {
		t.Fatal("a 0600 file must be reported as needing the shared-access pass")
	}
	if _, err := chgrpSetgidTree(root, gid); err != nil {
		t.Fatal(err)
	}
	if sharedAccessNeeded(root, gid) {
		t.Error("after chgrpSetgidTree the tree must be reported as already prepared")
	}
}
