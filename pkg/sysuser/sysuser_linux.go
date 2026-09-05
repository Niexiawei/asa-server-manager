//go:build linux

package sysuser

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"asa-server/pkg/problem"
)

// noSuchID is a uid/gid that matches nothing on any Linux system (it is
// (uid_t)-1, the "no change" sentinel of setuid/chown, never a real
// account). ChildIDs hands it back when the managed user doesn't exist yet,
// so a permission judgement built on it lands on the conservative branch
// instead of silently assuming ownership.
const noSuchID = ^uint32(0)

// Managed reports whether this process should drop its child to a dedicated
// non-root user: only when it is itself root and RunAsRoot hasn't opted out.
func (m *Manager) Managed() bool {
	return os.Geteuid() == 0 && !m.cfg.RunAsRoot
}

// UserName is the account name this Manager manages.
func (m *Manager) UserName() string { return m.cfg.userName() }

// HomeDir resolves the HOME a dropped child must see. When this Manager
// isn't managing a drop, it's this process's own home.
func (m *Manager) HomeDir() string {
	if !m.Managed() {
		h, _ := os.UserHomeDir()
		return h
	}
	if u, err := user.Lookup(m.UserName()); err == nil && u.HomeDir != "" && u.HomeDir != "/" {
		return u.HomeDir
	}
	return m.cfg.HomeFallback
}

// ChildIDs is the **read-only** counterpart of ResolveCredential: the uid/gid
// a dropped child (and anything else run on its behalf, e.g. a helper X
// server) will run as. managed is false when there is no drop at all, i.e.
// the child runs as this very process.
//
// The split matters: ResolveCredential *creates* the account, so anything
// that only needs to judge must not call it — creating a system user as a
// side effect of a permission check is the same class of mistake a read-only
// preflight must avoid.
func (m *Manager) ChildIDs() (uid, gid uint32, managed bool) {
	if !m.Managed() {
		return 0, 0, false
	}
	u, err := user.Lookup(m.UserName())
	if err != nil {
		return noSuchID, noSuchID, true
	}
	n, errUID := strconv.Atoi(u.Uid)
	g, errGID := strconv.Atoi(u.Gid)
	if errUID != nil || errGID != nil {
		return noSuchID, noSuchID, true
	}
	return uint32(n), uint32(g), true
}

// ResolveCredential returns the Credential to drop a child to, plus that
// user's HOME. Returns (nil, "", nil) when no drop should happen (not
// managing a user at all).
func (m *Manager) ResolveCredential() (*syscall.Credential, string, error) {
	if !m.Managed() {
		return nil, "", nil
	}
	u, err := m.lookupOrCreate()
	if err != nil {
		return nil, "", err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, "", fmt.Errorf("用户 %s 的 uid %q 非数值: %w", u.Username, u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, "", fmt.Errorf("用户 %s 的 gid %q 非数值: %w", u.Username, u.Gid, err)
	}
	cred := &syscall.Credential{
		Uid:    uint32(uid),
		Gid:    uint32(gid),
		Groups: []uint32{uint32(gid)}, // explicit, avoids setgroups([]) ambiguity
	}
	return cred, u.HomeDir, nil
}

// EnsureUser makes sure the managed account exists (creating it if needed)
// and that its own home directory exists and is owned by it. A no-op when
// this Manager isn't managing a drop.
func (m *Manager) EnsureUser(ctx context.Context) error {
	if !m.Managed() {
		return nil
	}
	u, err := m.lookupOrCreate()
	if err != nil {
		return err
	}
	if u.HomeDir == "" || u.HomeDir == "/" {
		return nil
	}
	if err := os.MkdirAll(u.HomeDir, 0o700); err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if err := ChownTreeAs(uid, gid, u.HomeDir); err != nil {
		return fmt.Errorf("chown runtime home %s: %w", u.HomeDir, err)
	}
	return nil
}

func (m *Manager) lookupOrCreate() (*user.User, error) {
	name := m.UserName()
	if u, err := user.Lookup(name); err == nil {
		if m.cfg.UID != 0 {
			if uid, _ := strconv.Atoi(u.Uid); uid != m.cfg.UID {
				return nil, fmt.Errorf(
					"用户 %s 已存在但 uid=%s，与配置的 uid=%d 不一致；"+
						"请改配置以匹配现有用户，或迁移相关子树的属主", name, u.Uid, m.cfg.UID)
			}
		}
		return u, nil
	}

	home := m.cfg.HomeFallback
	if err := createSystemUser(name, home, m.cfg.UID, m.cfg.GID); err != nil {
		return nil, err
	}
	u, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("创建用户 %s 后仍无法解析: %w", name, err)
	}
	return u, nil
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func nologinShell() string {
	for _, s := range []string{"/usr/sbin/nologin", "/sbin/nologin", "/usr/bin/nologin", "/bin/false"} {
		if fileExists(s) {
			return s
		}
	}
	return "/bin/false"
}

// findAdminTool resolves a user-admin binary. LookPath first (honors PATH),
// then the sbin dirs it commonly lives in — a systemd service's PATH doesn't
// always include /usr/sbin. Empty string = not found anywhere.
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

func createSystemUser(name, home string, uid, gid int) error {
	useradd := findAdminTool("useradd")
	if useradd == "" {
		return fmt.Errorf(
			"需要一个非 root 用户 %s 来运行游戏进程，但系统上找不到 useradd。三条出路：\n"+
				"  1. 手动创建：useradd -r -m -d %s -s /usr/sbin/nologin %s\n"+
				"  2. config.yaml 设 linux.umu_runtime_uid 指向一个已存在的非 root 账号\n"+
				"  3. config.yaml 设 linux.umu_run_as_root: true 明确接受以 root 运行游戏",
			name, home, name)
	}

	if gid != 0 {
		if groupadd := findAdminTool("groupadd"); groupadd != "" {
			out, err := exec.Command(groupadd, "-r", "-g", strconv.Itoa(gid), name).CombinedOutput()
			if err != nil && !strings.Contains(string(out), "already exists") {
				return fmt.Errorf("groupadd %s 失败: %v (%s)", name, err, strings.TrimSpace(string(out)))
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(home), 0o755); err != nil {
		return err
	}

	args := []string{"-r", "-m", "-d", home, "-s", nologinShell()}
	if uid != 0 {
		args = append(args, "-u", strconv.Itoa(uid))
	}
	if gid != 0 {
		args = append(args, "-g", strconv.Itoa(gid))
	} else {
		args = append(args, "-U") // create a matching primary group
	}
	args = append(args, name)

	if out, err := exec.Command(useradd, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("useradd %s 失败: %v (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ChownAs Lchowns a single path (non-recursive) to uid/gid. A free function,
// not a Manager method: the walk/chown mechanism doesn't need to know whose
// uid/gid this is or why, and a caller that already resolved a uid/gid some
// other way (e.g. a group shared with a second identity that isn't this
// Manager's) shouldn't have to fight the Manager's own resolution to reuse
// it.
func ChownAs(uid, gid int, path string) error {
	return os.Lchown(path, uid, gid)
}

// ChownTreeAs recursively Lchowns each of paths to uid/gid (does NOT follow
// symlinks). Entries that already have the wanted owner are skipped:
// lchown(2) is a metadata write even when it sets the owner an entry already
// has, and skipping turns the common case (nothing drifted) from a full
// write pass into a read-only walk — important when a path sits under a
// mounted overlay, where a metadata write to the wrong layer is undefined
// behaviour. Missing paths are silently skipped — a caller's list is often
// "everything that might exist", not "everything that must".
func ChownTreeAs(uid, gid int, paths ...string) error {
	for _, root := range paths {
		if !pathExists(root) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if info, ierr := d.Info(); ierr == nil {
				if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) == uid && int(st.Gid) == gid {
					return nil
				}
			}
			return os.Lchown(path, uid, gid)
		})
		if err != nil {
			return fmt.Errorf("chown %s: %w", root, err)
		}
	}
	return nil
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ChownOne chowns a single path (non-recursive) to the managed user, so a
// freshly created directory becomes writable by the dropped child. A no-op
// when this Manager isn't managing a drop.
func (m *Manager) ChownOne(path string) error {
	if !m.Managed() {
		return nil
	}
	uid, gid, _ := m.ChildIDs()
	if uid == noSuchID {
		return fmt.Errorf("runtime user %s not found", m.UserName())
	}
	return ChownAs(int(uid), int(gid), path)
}

// ChownTree recursively chowns each of paths to the managed user (skipping
// entries that already have the right owner). A no-op when this Manager
// isn't managing a drop.
func (m *Manager) ChownTree(paths ...string) error {
	if !m.Managed() {
		return nil
	}
	uid, gid, _ := m.ChildIDs()
	if uid == noSuchID {
		return fmt.Errorf("runtime user %s not found", m.UserName())
	}
	return ChownTreeAs(int(uid), int(gid), paths...)
}

// Status is the drop-privileges state, safe to expose to a status/preflight
// API. Ready is always false here — the caller sets it from a Problems()
// call, since only the caller knows which paths to check.
func (m *Manager) Status() Info {
	return Info{
		Managed:  os.Geteuid() == 0 && !m.cfg.RunAsRoot,
		Bypassed: os.Geteuid() == 0 && m.cfg.RunAsRoot,
		Name:     m.UserName(),
	}
}

// Problems runs the drop-privileges self-check: does the managed account
// exist, does it match the configured uid, are check's paths owned/
// accessible the way the dropped child needs. forceDeep (or Config.DeepProbe)
// makes it actually write a probe file as the dropped user.
func (m *Manager) Problems(check AccessCheck, forceDeep bool) []problem.Problem {
	if !m.Managed() {
		return nil
	}

	name := m.UserName()
	u, err := user.Lookup(name)
	if err != nil {
		return []problem.Problem{{
			Name:   "umu-runtime-user-missing",
			Detail: fmt.Sprintf("游戏实例需要以非 root 用户 %s 运行，但该用户不存在", name),
			Fix: fmt.Sprintf("重启 asa-server 会尝试自动创建；或手动 useradd -r -m %s；"+
				"或在 config.yaml 设 linux.umu_run_as_root: true 以 root 运行游戏", name),
		}}
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)

	var problems []problem.Problem

	if m.cfg.UID != 0 && uid != m.cfg.UID {
		problems = append(problems, problem.Problem{
			Name:   "umu-runtime-uid-mismatch",
			Detail: fmt.Sprintf("用户 %s 的 uid=%d，与配置的 uid=%d 不一致", name, uid, m.cfg.UID),
			Fix:    "改配置以匹配现有用户，或迁移相关子树属主到配置指定的 uid",
		})
	}

	if p := checkOwnedDir(u.HomeDir, uid, "umu-runtime-home-bad", "runtime 用户家目录"); p != nil {
		problems = append(problems, *p)
	}

	for _, dir := range check.OwnershipDirs {
		if !pathExists(dir) {
			continue
		}
		if bad := sampleOwnerMismatch(dir, uid); bad != "" {
			problems = append(problems, problem.Problem{
				Name:   "umu-runtime-owner-drift",
				Detail: fmt.Sprintf("%s 下存在非 %s 拥有的条目（例：%s）", dir, name, bad),
				Fix:    "重启 asa-server 会自动 chown 修复；修不回来多半是 SELinux / 只读挂载 / NFS root_squash",
			})
			break
		}
	}

	if check.TraversableDir != "" {
		if fi, err := os.Stat(check.TraversableDir); err == nil && fi.Mode().Perm()&0o001 == 0 {
			problems = append(problems, problem.Problem{
				Name:   "basedir-not-traversable",
				Detail: fmt.Sprintf("%s 缺少 o+x，非 root 的 %s 无法穿过它访问下层目录", check.TraversableDir, name),
				Fix:    fmt.Sprintf("chmod o+x %s", check.TraversableDir),
			})
		}
	}

	if check.ReadableEntry != "" && pathExists(check.ReadableEntry) {
		if fi, err := os.Stat(check.ReadableEntry); err == nil && fi.Mode().Perm()&0o005 != 0o005 {
			problems = append(problems, problem.Problem{
				Name:   "umu-runtime-ro-perm",
				Detail: fmt.Sprintf("%s 不是所有人可读+执行，降权后的游戏进程无法启动它", check.ReadableEntry),
				Fix:    fmt.Sprintf("chmod -R a+rX %s", filepath.Dir(check.ReadableEntry)),
			})
		}
	}

	if (forceDeep || m.cfg.DeepProbe) && check.ProbeDir != "" {
		if p := deepProbeWrite(check.ProbeDir, uint32(uid), uint32(gid)); p != nil {
			problems = append(problems, *p)
		}
	}
	return problems
}

func checkOwnedDir(path string, wantUID int, id, label string) *problem.Problem {
	if path == "" {
		return &problem.Problem{Name: id, Detail: label + "：路径为空"}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return &problem.Problem{
			Name:   id,
			Detail: fmt.Sprintf("%s（%s）不可用: %v", label, path, err),
			Fix:    "重启 asa-server 会尝试重建；或手动创建并 chown 给 runtime 用户",
		}
	}
	if !fi.IsDir() {
		return &problem.Problem{Name: id, Detail: fmt.Sprintf("%s（%s）不是目录", label, path)}
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != wantUID {
		return &problem.Problem{
			Name:   id,
			Detail: fmt.Sprintf("%s（%s）属主 uid=%d，应为 %d", label, path, st.Uid, wantUID),
			Fix:    fmt.Sprintf("chown -R %d %s", wantUID, path),
		}
	}
	return nil
}

// sampleOwnerMismatch walks up to a cap of entries under dir and returns the
// first whose owner uid != wantUID (empty string = all sampled entries OK).
// Sampled, not exhaustive: the authoritative full-tree fix is ChownTree,
// which the caller runs just before this in the same startup action.
func sampleOwnerMismatch(dir string, wantUID int) string {
	const cap = 400
	n := 0
	var bad string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		n++
		if n > cap {
			return filepath.SkipAll
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != wantUID {
			bad = path
			return filepath.SkipAll
		}
		return nil
	})
	return bad
}

// deepProbeWrite actually writes a probe file under dir as the dropped
// user — catches "owner looks right but a write still fails" (SELinux,
// ACLs, noexec/ro mount) that a stat-only check can't see.
func deepProbeWrite(dir string, uid, gid uint32) *problem.Problem {
	if !pathExists(dir) {
		return nil
	}
	probe := filepath.Join(dir, ".asa-umu-write-probe")
	cmd := exec.Command("/bin/sh", "-c", `touch "$0" && rm -f "$0"`, probe)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
		Uid: uid, Gid: gid, Groups: []uint32{gid},
	}}
	if out, err := cmd.CombinedOutput(); err != nil {
		return &problem.Problem{
			Name:   "umu-runtime-probe-failed",
			Detail: fmt.Sprintf("以 uid=%d 实际写 %s 失败：%v (%s)", uid, dir, err, strings.TrimSpace(string(out))),
			Fix:    "属主看着对但实际写不了——检查 SELinux (setenforce 0 验证) / ACL / 挂载选项 (ro, noexec)",
		}
	}
	return nil
}

// Env rewrites HOME/USER/LOGNAME to the managed user and strips
// root-inherited XDG_* so the child's runtime cache lands under the right
// home.
func Env(base []string, home, userName string) []string {
	if home == "" {
		return base
	}
	out := make([]string, 0, len(base)+3)
	for _, kv := range base {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		switch {
		case k == "HOME", k == "USER", k == "LOGNAME":
			continue
		case strings.HasPrefix(k, "XDG_"):
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "HOME="+home, "USER="+userName, "LOGNAME="+userName)
	return out
}
