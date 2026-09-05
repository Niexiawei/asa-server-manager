//go:build linux

// Package shareacl grants a directory tree write access to two different
// Unix identities at once: whichever process owns it now (typically root)
// and a group it did not previously belong to. It knows nothing about ASA,
// instances, or which trees need this treatment — the caller decides that
// and passes in a root path, uid/gid, and group name.
//
// Two inheritance rules make it stick for files created *after* the pass
// runs, not just the ones that existed at the time:
//
//   - setgid on every directory, so entries created later inherit the group
//     no matter who creates them;
//   - a POSIX *default* ACL granting the group rwX, because setgid inherits
//     the group but NOT the permission bits — a root umask of 022 would
//     otherwise produce rw-r--r-- files the group still can't write.
//
// Filesystems without ACL support (or missing the acl package's setfacl
// binary) degrade to a plain recursive chown to the given uid/gid — that
// fixes what exists right now but does not survive the next root-created
// file, which is why the caller is expected to re-run Prepare periodically
// (see NeedsPass) rather than treat one pass as permanent.
package shareacl

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrUnsupported means setfacl is missing or the filesystem rejected ACLs.
// Prepare falls back to a plain chown when it sees this internally; Supported
// returns it directly so callers can decide what to tell a human.
var ErrUnsupported = errors.New("shareacl: POSIX ACLs unavailable")

// ChownFallback chowns root (recursively) to uid:gid. Prepare calls this
// itself when ACLs are unavailable; it is exported for callers that want to
// log the fallback and perform it explicitly rather than let Prepare do it
// silently.
type ChownFallback func(root string, uid, gid int) error

// Prepare grants group rwX on root: group ownership + setgid + g+rwX on
// everything that exists now, then a default ACL so everything created later
// inherits it. When ACLs are unavailable it falls back to chownFallback
// (chowning the tree to uid) instead of returning ErrUnsupported — that
// keeps the tree usable in the degraded case, at the cost of the caller no
// longer being a co-writer.
func Prepare(root string, uid, gid int, group string, chownFallback ChownFallback) error {
	dirs, err := chgrpSetgidTree(root, gid)
	if err != nil {
		return fmt.Errorf("chgrp/setgid %s: %w", root, err)
	}

	aclErr := applyDefaultACL(root, group, dirs)
	if aclErr == nil {
		return nil
	}
	if !errors.Is(aclErr, ErrUnsupported) {
		return aclErr
	}
	if chownFallback == nil {
		return aclErr
	}
	return chownFallback(root, uid, gid)
}

// NeedsPass reports whether Prepare is worth running again: any entry whose
// group isn't gid, or that the group can't write, or a directory missing its
// setgid bit, means the inheritance chain is broken somewhere. Sampled
// (capped at 400 entries) rather than exhaustive — meant for a cheap check on
// every startup, with the authoritative unconditional pass run after updates.
func NeedsPass(root string, gid int) bool {
	const sampleCap = 400
	n := 0
	needed := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		n++
		if n > sampleCap {
			return filepath.SkipAll
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Gid) != gid {
			needed = true
			return filepath.SkipAll
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // no permission bits of its own
		}
		if info.Mode().Perm()&0o060 != 0o060 {
			needed = true
			return filepath.SkipAll
		}
		if d.IsDir() && info.Mode()&os.ModeSetgid == 0 {
			needed = true
			return filepath.SkipAll
		}
		return nil
	})
	return needed
}

// DefaultACLMissing reports whether root lacks the inheritable ACL entry
// Prepare installs for group.
//
// This exists because NeedsPass's sampling only looks at ownership and mode
// bits, and those are *already correct* on a tree that went through the
// degraded chown fallback. Without this check, installing the acl package on
// a machine that had been running degraded would never take effect: every
// subsequent pass would sample a clean-looking tree and skip the step that
// would finally add the ACLs.
//
// Returns false when ACLs aren't available at all — there is nothing to fix
// then, and saying "missing" would make the degraded path re-walk the whole
// tree on every check forever.
func DefaultACLMissing(root, group string) bool {
	if findAdminTool("setfacl") == "" {
		return false
	}
	tool := findAdminTool("getfacl")
	if tool == "" {
		return false
	}
	// -c drops the header comments, leaving just the entries.
	out, err := exec.Command(tool, "-c", root).Output()
	if err != nil {
		return false
	}
	return !strings.Contains(string(out), "default:group:"+group+":")
}

// Supported probes whether a default ACL can actually be set inside dir, by
// doing it for real on a throwaway subdirectory. Checking that setfacl exists
// is not enough — the binary is frequently present on filesystems mounted
// without ACL support.
func Supported(dir, group string) error {
	tool := findAdminTool("setfacl")
	if tool == "" {
		return fmt.Errorf("%w: setfacl not found in PATH", ErrUnsupported)
	}
	probe, err := os.MkdirTemp(dir, ".shareacl-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(probe)

	out, err := exec.Command(tool, "-d", "-m", "g:"+group+":rwX", probe).CombinedOutput()
	if err != nil {
		return classifyACLError(fmt.Errorf("setfacl probe in %s: %v (%s)",
			dir, err, strings.TrimSpace(string(out))), out)
	}
	return nil
}

// GroupName resolves a gid to its group name for setfacl, which wants a name
// or a numeric id — the numeric form is always safe, so an unresolvable gid
// degrades to that rather than failing.
func GroupName(gid string) string {
	if g, err := user.LookupGroupId(gid); err == nil && g.Name != "" {
		return g.Name
	}
	return gid
}

// chgrpSetgidTree sets the group of every entry under root to gid, adds group
// read/write (and execute where it makes sense — the `chmod g+rwX` rule), and
// marks directories setgid. It returns the directories it saw, so the ACL
// pass doesn't have to walk the tree a second time.
//
// Symlinks are Lchown'd only: a symlink has no permission bits of its own,
// and following it would silently apply the change to whatever it points at.
func chgrpSetgidTree(root string, gid int) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// -1 leaves the owner untouched: these trees stay root-owned on
		// purpose, so admin tooling and backups keep behaving as before.
		if err := os.Lchown(path, -1, gid); err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		perm := mode.Perm()
		want := perm | 0o060 // g+rw
		if d.IsDir() || perm&0o100 != 0 {
			want |= 0o010 // g+x, the "X" in chmod g+rwX
		}
		newMode := mode&^fs.ModePerm | want
		if d.IsDir() {
			dirs = append(dirs, path)
			newMode |= os.ModeSetgid
		}
		if newMode != mode {
			return os.Chmod(path, newMode)
		}
		return nil
	})
	return dirs, err
}

// applyDefaultACL grants group rwX as both an access ACL (everything that
// exists) and a default ACL (everything created later).
//
// The default pass is applied to directories explicitly rather than via
// `setfacl -R -d`: default ACLs only exist on directories, and a recursive
// invocation that meets a regular file reports an error for it. dirs comes
// from the walk chgrpSetgidTree already did.
func applyDefaultACL(root, group string, dirs []string) error {
	tool := findAdminTool("setfacl")
	if tool == "" {
		return fmt.Errorf("%w: setfacl not found in PATH", ErrUnsupported)
	}
	spec := "g:" + group + ":rwX"

	if out, err := exec.Command(tool, "-R", "-m", spec, root).CombinedOutput(); err != nil {
		return classifyACLError(fmt.Errorf("setfacl -R -m %s %s: %v (%s)",
			spec, root, err, strings.TrimSpace(string(out))), out)
	}

	// ARG_MAX is generous but a tree can have thousands of directories;
	// batching keeps the argument list bounded without one exec per directory.
	const batch = 500
	for i := 0; i < len(dirs); i += batch {
		end := min(i+batch, len(dirs))
		args := append([]string{"-d", "-m", spec}, dirs[i:end]...)
		if out, err := exec.Command(tool, args...).CombinedOutput(); err != nil {
			return classifyACLError(fmt.Errorf("setfacl -d -m %s: %v (%s)",
				spec, err, strings.TrimSpace(string(out))), out)
		}
	}
	return nil
}

// classifyACLError turns "this filesystem doesn't do ACLs" into
// ErrUnsupported (so the caller degrades gracefully) and leaves anything
// else — a genuinely broken tree, a missing group — as a hard error.
func classifyACLError(err error, out []byte) error {
	s := strings.ToLower(string(out))
	switch {
	case strings.Contains(s, "operation not supported"),
		strings.Contains(s, "not supported"),
		strings.Contains(s, "read-only file system"):
		return fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	return err
}

// findAdminTool resolves an ACL binary. LookPath first (honors PATH), then
// the sbin dirs it commonly lives in — a systemd service's PATH doesn't
// always include /usr/sbin. Empty string = not found anywhere.
func findAdminTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range []string{"/usr/sbin", "/sbin", "/usr/local/sbin", "/usr/bin"} {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}
