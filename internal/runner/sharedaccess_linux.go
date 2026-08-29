//go:build linux

package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"asa-server/pkg/logger"
)

// Shared-access trees: directories that BOTH root and the dropped runtime user
// have to be able to write.
//
// The rest of the drop-privileges design (docs/UMU_RUNTIME_USER_PLAN.md) works
// by handing a subtree to the runtime user outright — the Wine prefix and the
// per-instance mirror have exactly one writer, so `chown -R` is the whole
// story there. server-files and instances are different: SteamCMD runs as
// root, admins upload ArkApi plugins over SFTP as root, asa-server itself
// writes instance config as root — and the game, dropped to asa-umu-runtime,
// has to write saves, logs, ModsUserData and crash dumps into the same trees.
//
// Chowning those to the runtime user "works" until the next update or upload
// re-creates root-owned files, at which point the game silently loses write
// access again (this is exactly how the failure in
// docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §3.6 was produced). So they
// get group access instead, with two inheritance rules that make it stick:
//
//   - setgid on every directory, so entries created later inherit the group
//     no matter who creates them;
//   - a POSIX *default* ACL granting the group rwX, because setgid inherits
//     the group but NOT the permission bits — root's default umask of 022
//     would otherwise produce rw-r--r-- files the group still can't write.
//
// root needs no membership in that group: it holds CAP_DAC_OVERRIDE and
// bypasses the checks entirely.
//
// The group is the runtime user's *primary* group on purpose. resolveRuntimeCredential
// sets Credential.Groups to that gid alone — supplementary groups are not
// populated — so a dedicated group would simply never be present on the
// dropped process, and the symptom would be "permissions look right but writes
// still fail". See docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §3.7.

// errACLUnsupported means setfacl is missing or the filesystem rejected ACLs.
// Callers fall back to plain chown (the "A" path: fixes what exists now,
// doesn't survive the next root-created file).
var errACLUnsupported = errors.New("runner: POSIX ACLs unavailable")

// sharedSubtrees are the trees given the treatment above. Paths are derived
// from BaseDir rather than imported from the config package — runner
// deliberately has no dependency on it (see Config.BaseDir's doc comment).
func sharedSubtrees(cfg Config) []string {
	return []string{
		filepath.Join(cfg.BaseDir, "server-files"),
		filepath.Join(cfg.BaseDir, "instances"),
	}
}

// prepareSharedTree makes root writable by both root and the runtime user.
// No-op when we aren't managing a dropped user. Idempotent.
func prepareSharedTree(root string) error {
	cfg := getConfig()
	if !runtimeUserManaged(cfg) {
		return nil
	}
	u, err := lookupOrCreateRuntimeUser(cfg)
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return applySharedAccess(root, uid, gid, runtimeGroupName(u.Gid))
}

// applySharedAccess is the two-step worker: group ownership + setgid + g+rwX
// on everything that exists now, then a default ACL so everything created
// later inherits it. Falls back to chowning the tree to the runtime user when
// ACLs aren't available.
func applySharedAccess(root string, uid, gid int, group string) error {
	dirs, err := chgrpSetgidTree(root, gid)
	if err != nil {
		return fmt.Errorf("chgrp/setgid %s: %w", root, err)
	}

	aclErr := applyDefaultACL(root, group, dirs)
	if aclErr == nil {
		return nil
	}
	if !errors.Is(aclErr, errACLUnsupported) {
		return aclErr
	}

	// Degraded mode: without inheritable ACLs the only way the dropped user
	// can write is to own the files. This is re-applied on every asa-server
	// start (reconcileRuntimeOwnership) and after every SteamCMD update, which
	// is what keeps it usable — but a file uploaded as root mid-run stays
	// unwritable until one of those runs again.
	logger.Warnf("POSIX ACLs unavailable on %s (%v); falling back to chown -R to the runtime user. "+
		"Install the 'acl' package (or mount with acl support) so plugin/mod files uploaded as root "+
		"stay writable by the game process", root, aclErr)
	return chownTree(root, uid, gid)
}

// chgrpSetgidTree sets the group of every entry under root to gid, adds group
// read/write (and execute where it makes sense — the `chmod g+rwX` rule), and
// marks directories setgid. It returns the directories it saw, so the ACL pass
// doesn't have to walk the tree a second time.
//
// Symlinks are Lchown'd only: a symlink has no permission bits of its own, and
// following it would silently apply the change to whatever it points at
// (server-files is full of links into other trees).
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
		return fmt.Errorf("%w: setfacl not found in PATH", errACLUnsupported)
	}
	spec := "g:" + group + ":rwX"

	if out, err := exec.Command(tool, "-R", "-m", spec, root).CombinedOutput(); err != nil {
		return classifyACLError(fmt.Errorf("setfacl -R -m %s %s: %v (%s)",
			spec, root, err, strings.TrimSpace(string(out))), out)
	}

	// ARG_MAX is generous but server-files has thousands of directories;
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
// errACLUnsupported (so the caller degrades gracefully) and leaves anything
// else — a genuinely broken tree, a missing group — as a hard error.
func classifyACLError(err error, out []byte) error {
	s := strings.ToLower(string(out))
	switch {
	case strings.Contains(s, "operation not supported"),
		strings.Contains(s, "not supported"),
		strings.Contains(s, "read-only file system"):
		return fmt.Errorf("%w: %v", errACLUnsupported, err)
	}
	return err
}

// aclSupported probes whether a default ACL can actually be set inside dir,
// by doing it for real on a throwaway subdirectory. Checking that setfacl
// exists is not enough — the binary is frequently present on filesystems
// mounted without ACL support.
func aclSupported(dir, group string) error {
	tool := findAdminTool("setfacl")
	if tool == "" {
		return fmt.Errorf("%w: setfacl not found in PATH", errACLUnsupported)
	}
	probe, err := os.MkdirTemp(dir, ".asa-acl-probe-")
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

// runtimeGroupName resolves a gid to its group name for setfacl, which wants
// a name or a numeric id — the numeric form is always safe, so an unresolvable
// gid degrades to that rather than failing.
func runtimeGroupName(gid string) string {
	if g, err := user.LookupGroupId(gid); err == nil && g.Name != "" {
		return g.Name
	}
	return gid
}

// checkACLSupport is the Preflight-facing form of aclSupported. It is
// advisory: a missing ACL layer degrades to chown rather than blocking
// anything, so it belongs in Preflight (surfaced through
// GET /api/system/preflight) and NOT in verifyRuntimeAccess, whose non-empty
// result makes asa-server refuse to start.
func checkACLSupport() *Problem {
	cfg := getConfig()
	if !runtimeUserManaged(cfg) || cfg.BaseDir == "" {
		return nil
	}
	if _, err := os.Stat(cfg.BaseDir); err != nil {
		return nil // BaseDir not created yet — setup hasn't run, nothing to say
	}
	// Probe against the runtime user's primary group when it exists, and fall
	// back to this process's own gid so `setup` can still answer before the
	// account is created.
	group := runtimeUserName(cfg)
	if u, err := user.Lookup(group); err == nil {
		group = runtimeGroupName(u.Gid)
	} else {
		group = strconv.Itoa(syscall.Getgid())
	}

	if err := aclSupported(cfg.BaseDir, group); err != nil {
		if errors.Is(err, errACLUnsupported) {
			return &Problem{
				Name: "posix-acl",
				Detail: fmt.Sprintf("%s 不支持 POSIX ACL（%v）。asa-server 会退回到"+
					"「把 server-files/instances 整体 chown 给运行时用户」的兜底方案："+
					"当前能用，但之后以 root 上传的 ArkApi 插件、mod 文件，以及 SteamCMD "+
					"更新产生的新文件，游戏进程都会写不了，直到下次重启 asa-server 或重跑更新",
					cfg.BaseDir, err),
				Fix: "安装 acl 包（Debian/Ubuntu: apt install acl；Fedora: dnf install acl；" +
					"Arch: pacman -S acl），并确认所在文件系统挂载时启用了 acl",
			}
		}
		return nil // a transient/local error, not a capability statement
	}
	return nil
}
