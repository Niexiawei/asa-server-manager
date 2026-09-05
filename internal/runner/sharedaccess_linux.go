//go:build linux

package runner

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"asa-server/pkg/logger"
	"asa-server/pkg/shareacl"
	"asa-server/pkg/sysuser"
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
// get group access instead via pkg/shareacl (group + setgid + POSIX default
// ACL, falling back to plain chown when ACLs aren't available).
//
// root needs no membership in that group: it holds CAP_DAC_OVERRIDE and
// bypasses the checks entirely.
//
// The group is the runtime user's *primary* group on purpose. resolveRuntimeCredential
// sets Credential.Groups to that gid alone — supplementary groups are not
// populated — so a dedicated group would simply never be present on the
// dropped process, and the symptom would be "permissions look right but writes
// still fail". See docs/LINUX_KILLTREE_AND_VERIFY_HANG_DIAGNOSIS.md §3.7.

// sharedSubtrees are the trees given the treatment above. Paths are derived
// from BaseDir rather than imported from the config package — runner
// deliberately has no dependency on it (see Config.BaseDir's doc comment).
func sharedSubtrees(cfg Config) []string {
	return []string{
		filepath.Join(cfg.BaseDir, "server-files"),
		filepath.Join(cfg.BaseDir, "instances"),
	}
}

// sharedTrees is sharedSubtrees for the active config, filtered to what
// actually exists on disk.
func sharedTrees() []string {
	cfg := getConfig()
	if !sysUserFor(cfg).Managed() {
		return nil
	}
	var out []string
	for _, dir := range sharedSubtrees(cfg) {
		if pathExists(dir) {
			out = append(out, dir)
		}
	}
	return out
}

// sharedAccessStatus answers "what regime is actually in force right now",
// entirely read-only — the whole point is to be safe to run against a live
// server. The ACL probe is the one exception and it creates/removes its own
// throwaway directory (shareacl.Supported).
func sharedAccessStatus() SharedAccessInfo {
	cfg := getConfig()
	su := sysUserFor(cfg)
	info := SharedAccessInfo{Managed: su.Managed()}
	if !info.Managed {
		return info
	}

	info.User = su.UserName()
	u, err := user.Lookup(info.User)
	if err != nil {
		info.ACLError = fmt.Sprintf("运行时用户 %s 不存在: %v", info.User, err)
		return info
	}
	info.UID, _ = strconv.Atoi(u.Uid)
	info.GID, _ = strconv.Atoi(u.Gid)
	info.Group = shareacl.GroupName(u.Gid)

	if err := shareacl.Supported(cfg.BaseDir, info.Group); err != nil {
		info.ACLError = err.Error()
	} else {
		info.ACLTool = findAdminTool("setfacl")
	}

	for _, dir := range sharedSubtrees(cfg) {
		t := TreeAccessInfo{Path: dir, Exists: pathExists(dir)}
		if t.Exists {
			t.Prepared = !shareacl.NeedsPass(dir, info.GID)
			t.DefaultACL = info.ACLTool != "" && !shareacl.DefaultACLMissing(dir, info.Group)
		}
		info.Trees = append(info.Trees, t)
	}
	return info
}

// prepareSharedTree makes root writable by both root and the runtime user.
// No-op when we aren't managing a dropped user. Idempotent.
func prepareSharedTree(root string) error {
	cfg := getConfig()
	su := sysUserFor(cfg)
	if !su.Managed() {
		return nil
	}
	if err := su.EnsureUser(context.Background()); err != nil {
		return err
	}
	uid, gid, _ := su.ChildIDs()
	return applySharedAccess(root, int(uid), int(gid), shareacl.GroupName(strconv.Itoa(int(gid))))
}

// applySharedAccess prepares root via pkg/shareacl, warning and degrading to
// a plain chown of the runtime user when ACLs aren't available.
func applySharedAccess(root string, uid, gid int, group string) error {
	return shareacl.Prepare(root, uid, gid, group, func(root string, uid, gid int) error {
		// Degraded mode: without inheritable ACLs the only way the dropped
		// user can write is to own the files. This is re-applied on every
		// asa-server start (reconcileRuntimeOwnership) and after every
		// SteamCMD update, which is what keeps it usable — but a file
		// uploaded as root mid-run stays unwritable until one of those runs
		// again.
		logger.Warnf("POSIX ACLs unavailable on %s; falling back to chown -R to the runtime user. "+
			"Install the 'acl' package (or mount with acl support) so plugin/mod files uploaded as root "+
			"stay writable by the game process", root)
		return sysuser.ChownTreeAs(uid, gid, root)
	})
}

// findAdminTool resolves a user-admin/ACL binary. LookPath first (honors
// PATH), then the sbin dirs it commonly lives in — a systemd service's PATH
// doesn't always include /usr/sbin. Empty string = not found anywhere.
//
// A small duplicate of pkg/shareacl's own internal copy (which needs the same
// lookup for setfacl/getfacl) and pkg/sysuser's (useradd/groupadd): each
// package is meant to stand alone with zero pkg-to-pkg dependencies, so a few
// lines duplicated three times beats a shared micro-package. This copy's one
// use here is diagnostic (reporting which tool sharedAccessStatus found), not
// a dependency of the ACL mechanism itself.
func findAdminTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range []string{"/usr/sbin", "/sbin", "/usr/local/sbin", "/usr/bin"} {
		if p := filepath.Join(dir, name); fileExists(p) {
			return p
		}
	}
	return ""
}

// checkACLSupport is the Preflight-facing form of shareacl.Supported. It is
// advisory: a missing ACL layer degrades to chown rather than blocking
// anything, so it belongs in Preflight (surfaced through
// GET /api/system/preflight) and NOT in verifyRuntimeAccess, whose non-empty
// result makes asa-server refuse to start.
func checkACLSupport() *Problem {
	cfg := getConfig()
	su := sysUserFor(cfg)
	if !su.Managed() || cfg.BaseDir == "" {
		return nil
	}
	if !pathExists(cfg.BaseDir) {
		return nil // BaseDir not created yet — setup hasn't run, nothing to say
	}
	// Probe against the runtime user's primary group when it exists, and fall
	// back to this process's own gid so `setup` can still answer before the
	// account is created.
	group := su.UserName()
	if u, err := user.Lookup(group); err == nil {
		group = shareacl.GroupName(u.Gid)
	} else {
		group = strconv.Itoa(syscall.Getgid())
	}

	if err := shareacl.Supported(cfg.BaseDir, group); err != nil {
		if errors.Is(err, shareacl.ErrUnsupported) {
			return &Problem{
				// Advisory, not a blocker: applySharedAccess degrades to a
				// plain chown and everything keeps working. Marking it as a
				// blocker would make `asa-server setup` refuse to run on any
				// machine without the acl package.
				Warning: true,
				Name:    "posix-acl",
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
