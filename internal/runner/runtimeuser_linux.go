//go:build linux

package runner

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
)

// This file implements "run the game instance as a dedicated non-root user"
// — see docs/UMU_RUNTIME_USER_PLAN.md. asa-server itself keeps running as
// root; only the umu-run child (and the whole bwrap/wine/ArkAscendedServer.exe
// tree below it) is dropped to asa-umu-runtime via SysProcAttr.Credential.

const defaultRuntimeUser = "asa-umu-runtime"

// runtimeUserManaged reports whether this process should drop the game child
// to a dedicated non-root user: only when asa-server is itself root and the
// operator hasn't explicitly opted into running the game as root.
func runtimeUserManaged(cfg Config) bool {
	return os.Geteuid() == 0 && !cfg.RunAsRoot
}

func runtimeUserName(cfg Config) string {
	if cfg.RuntimeUser != "" {
		return cfg.RuntimeUser
	}
	return defaultRuntimeUser
}

// runtimeUserName is also needed without a Config in hand (svcmgr / systemapi).
func RuntimeUserName() string { return runtimeUserName(getConfig()) }

// runtimeHomeDir resolves the HOME the dropped child must see. When we are not
// managing a separate user it's just this process's own home.
func runtimeHomeDir(cfg Config) string {
	if !runtimeUserManaged(cfg) {
		h, _ := os.UserHomeDir()
		return h
	}
	if u, err := user.Lookup(runtimeUserName(cfg)); err == nil && u.HomeDir != "" && u.HomeDir != "/" {
		return u.HomeDir
	}
	return filepath.Join(cfg.BaseDir, "runtime-home")
}

// noSuchID is a uid/gid that matches nothing on any Linux system (it is
// (uid_t)-1, the "no change" sentinel of setuid/chown, never a real account).
// runtimeChildIDs hands it back when the runtime user doesn't exist yet, so a
// permission judgement built on it lands on the conservative branch instead of
// silently assuming ownership.
const noSuchID = ^uint32(0)

// runtimeChildIDs is the **read-only** counterpart of resolveRuntimeCredential:
// the uid/gid the game child — and the Xvfb we start for it — will run as.
// managed is false when there is no drop at all, i.e. the child runs as this
// very process.
//
// The split matters: resolveRuntimeCredential *creates* the account (useradd,
// via lookupOrCreateRuntimeUser), so anything that only needs to judge must
// not call it. Creating a system user as a side effect of a permission check
// is the same class of mistake planDisplay/acquire exists to prevent.
func runtimeChildIDs(cfg Config) (uid, gid uint32, managed bool) {
	if !runtimeUserManaged(cfg) {
		return 0, 0, false
	}
	u, err := user.Lookup(runtimeUserName(cfg))
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

// --- credential resolution ---------------------------------------------------

// resolveRuntimeCredential returns the Credential to drop the child to, plus
// that user's HOME. Returns (nil, "", nil) when no drop should happen
// (euid != 0, or umu_run_as_root=true, or a non-linux build).
func resolveRuntimeCredential(cfg Config) (*syscall.Credential, string, error) {
	if !runtimeUserManaged(cfg) {
		return nil, "", nil
	}
	u, err := lookupOrCreateRuntimeUser(cfg)
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

// --- user creation ---------------------------------------------------------

func lookupOrCreateRuntimeUser(cfg Config) (*user.User, error) {
	name := runtimeUserName(cfg)
	if u, err := user.Lookup(name); err == nil {
		if cfg.RuntimeUID != 0 {
			if uid, _ := strconv.Atoi(u.Uid); uid != cfg.RuntimeUID {
				return nil, fmt.Errorf(
					"用户 %s 已存在但 uid=%s，与配置 linux.umu_runtime_uid=%d 不一致；"+
						"请改配置以匹配现有用户，或迁移 BaseDir 下相关子树的属主", name, u.Uid, cfg.RuntimeUID)
			}
		}
		return u, nil
	}

	home := filepath.Join(cfg.BaseDir, "runtime-home")
	if err := createSystemUser(name, home, cfg.RuntimeUID, cfg.RuntimeGID); err != nil {
		return nil, err
	}
	u, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("创建用户 %s 后仍无法解析: %w", name, err)
	}
	return u, nil
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

// --- ownership reconcile -------------------------------------------------------

func ensureRuntimeUser(ctx context.Context) error {
	cfg := getConfig()
	if !runtimeUserManaged(cfg) {
		return nil
	}
	u, err := lookupOrCreateRuntimeUser(cfg)
	if err != nil {
		return err
	}
	return reconcileRuntimeOwnership(cfg, u)
}

func reconcileRuntimeOwnership(cfg Config, u *user.User) error {
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)

	if u.HomeDir != "" && u.HomeDir != "/" {
		if err := os.MkdirAll(u.HomeDir, 0o700); err != nil {
			return err
		}
		if err := chownTree(u.HomeDir, uid, gid); err != nil {
			return fmt.Errorf("chown runtime home %s: %w", u.HomeDir, err)
		}
	}

	// Mirror dirs (server-files-tmp-*) are deliberately excluded here: they're
	// rebuilt per instance start and chowned then by ChownMirrorForRuntime, so
	// walking their tens of thousands of symlinks on every asa-server startup
	// would be pure overhead. verifyRuntimeAccess still samples them for drift.
	for _, dir := range rwSubtrees(cfg, false) {
		if !pathExists(dir) {
			continue
		}
		if err := chownTree(dir, uid, gid); err != nil {
			return fmt.Errorf("chown %s: %w", dir, err)
		}
	}

	// server-files / instances are shared with root rather than handed over —
	// see sharedaccess_linux.go for why. Walking server-files (~50k entries)
	// on every startup would be pure overhead once it's already set up, so a
	// cheap sample decides whether the full pass is worth doing; the
	// authoritative unconditional pass runs after every SteamCMD update and
	// before verification (installer.go).
	group := runtimeGroupName(u.Gid)
	for _, dir := range sharedSubtrees(cfg) {
		if !pathExists(dir) {
			continue
		}
		// Two independent reasons to run the pass: ownership/mode drift (the
		// common one, after an update or a manual chown), and "the ACLs were
		// never applied" — which is what a machine that had been running the
		// degraded fallback looks like once the acl package gets installed.
		// The first check can't see the second: a degraded tree has perfectly
		// correct ownership and mode bits and no ACLs at all.
		if !sharedAccessNeeded(dir, gid) && !defaultACLMissing(dir, group) {
			continue
		}
		if err := applySharedAccess(dir, uid, gid, group); err != nil {
			return fmt.Errorf("prepare shared access on %s: %w", dir, err)
		}
	}

	if proton := protonPath(cfg); pathExists(proton) {
		if err := ensureWorldReadExec(proton); err != nil {
			return fmt.Errorf("chmod %s: %w", proton, err)
		}
	}
	if umu := umuDir(cfg); pathExists(umu) {
		if err := ensureWorldReadExec(umu); err != nil {
			return fmt.Errorf("chmod %s: %w", umu, err)
		}
	}
	return nil
}

// rwSubtrees are the directories the dropped child writes to and therefore
// must own — see docs/UMU_RUNTIME_USER_PLAN.md §5.1. includeMirrors adds the
// per-instance server-files-tmp-* dirs (wanted for the verify sampling, not
// for the startup reconcile — see reconcileRuntimeOwnership).
func rwSubtrees(cfg Config, includeMirrors bool) []string {
	out := []string{
		prefixDir(cfg, ""),
		filepath.Join(cfg.BaseDir, "clusters"),
	}
	overlays := overlayRoot(cfg)
	if m, _ := filepath.Glob(prefixDir(cfg, "") + "-*"); len(m) > 0 {
		for _, p := range m {
			// The overlay root is not a prefix, and walking it here would be
			// actively harmful — see overlayRWSubtrees.
			if p == overlays {
				continue
			}
			out = append(out, p)
		}
	}
	out = append(out, overlayRWSubtrees(cfg)...)
	if includeMirrors {
		if m, _ := filepath.Glob(filepath.Join(cfg.BaseDir, "server-files-tmp-*")); len(m) > 0 {
			out = append(out, m...)
		}
	}
	return out
}

// overlayRWSubtrees lists the parts of prefix_mode "overlay" that this program
// owns and must keep chowned to the runtime user.
//
// The answer is: only a `merged` that is NOT mounted — i.e. the copy fallback
// (§6.3), an ordinary directory of real files. A mounted layer contributes
// nothing to this list, and all three of its directories are excluded for
// different reasons:
//
//   - `merged` (mounted): chown is a metadata write, and a metadata write
//     through an overlay **copies the file up**. Walking it would copy the
//     entire shared lower into that instance's private layer, on every startup
//     reconcile, for every instance — silently undoing the one thing this mode
//     exists to do.
//   - `upper`: modifying the upper layer from the side while the overlay is
//     mounted is explicitly unsupported by overlayfs. It also needs no pass:
//     copy-ups preserve the lower's ownership (already the runtime user's) and
//     anything new is created by the game process itself.
//   - `work`: the kernel's private scratch area. It creates `work/work` inside
//     it at mount time, owned by root with mode 000, and userspace is not
//     supposed to touch any of it. Listing `work` here is what made the very
//     first real-hardware launch fail — the ownership-drift sampler found
//     `work/work`, reported it as drift, and blocked the start with a "restart
//     asa-server to fix" that could never have fixed it.
func overlayRWSubtrees(cfg Config) []string {
	entries, err := os.ReadDir(overlayRoot(cfg))
	if err != nil {
		return nil
	}
	mounts := listOverlayMounts()

	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if merged := overlayMergedDir(cfg, e.Name()); !mounts[merged] {
			out = append(out, merged)
		}
	}
	return out
}

// sharedAccessNeeded samples root and reports whether the (expensive) full
// applySharedAccess pass is worth running: any entry whose group isn't gid, or
// that the group can't write, or a directory missing its setgid bit, means the
// inheritance chain is broken somewhere and the tree needs another pass.
//
// Sampled rather than exhaustive, on the same reasoning as
// sampleOwnerMismatch: the authoritative pass is the unconditional one the
// installer runs after each update. A clean tree costs a few hundred stats
// here instead of a full walk on every asa-server start.
func sharedAccessNeeded(root string, gid int) bool {
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

// chownTree Lchowns every entry under root (does NOT follow symlinks — the
// prefix is full of them, and their targets in server-files stay root-owned).
//
// Entries that already have the wanted owner are skipped. lchown(2) is a
// metadata write even when it sets the owner an entry already has, and this
// runs over the shared Wine prefix on every startup — which under prefix_mode
// "overlay" is the lowerdir of however many mounted writable layers, and
// modifying a lowerdir under a live mount is undefined behaviour. Skipping
// also turns the common case (nothing drifted) from a full write pass over a
// multi-GB prefix into a read-only walk.
func chownTree(root string, uid, gid int) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
}

// ensureWorldReadExec makes a read-only subtree traversable/readable by any
// user: o+r on files, o+rx on dirs and owner-executable files.
func ensureWorldReadExec(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		want := mode | 0o044
		if d.IsDir() || mode&0o100 != 0 {
			want |= 0o011
		}
		if want != mode {
			return os.Chmod(path, want)
		}
		return nil
	})
}

// ChownMirrorForRuntime is called from instance start after the per-instance
// mirror is (re)built, before runner.Run. No-op unless we're managing a
// dropped user. The mirror is mostly symlinks into root-owned server-files;
// Lchown flips the links, real copied files get chowned for real.
func chownMirrorForRuntime(mirrorDir string) error {
	cfg := getConfig()
	if !runtimeUserManaged(cfg) {
		return nil
	}
	u, err := user.Lookup(runtimeUserName(cfg))
	if err != nil {
		return fmt.Errorf("runtime user %s not found: %w", runtimeUserName(cfg), err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return chownTree(mirrorDir, uid, gid)
}

// ChownTreeForRuntime chowns an arbitrary path (recursively) to the runtime
// user. Used by installer fixups for the ~/.steam SDK symlink dir. No-op
// unless we're managing a dropped user.
func chownTreeForRuntime(root string) error {
	cfg := getConfig()
	if !runtimeUserManaged(cfg) {
		return nil
	}
	u, err := user.Lookup(runtimeUserName(cfg))
	if err != nil {
		return fmt.Errorf("runtime user %s not found: %w", runtimeUserName(cfg), err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return chownTree(root, uid, gid)
}

// chownPathForRuntime chowns a single path (non-recursive) to the runtime
// user, so a freshly MkdirAll'd dir is writable by the dropped child.
func chownPathForRuntime(path string) error {
	cfg := getConfig()
	if !runtimeUserManaged(cfg) {
		return nil
	}
	u, err := user.Lookup(runtimeUserName(cfg))
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return os.Lchown(path, uid, gid)
}

// --- access self-check ------------------------------------------------------

func verifyRuntimeAccess(forceDeep bool) []Problem {
	cfg := getConfig()
	if !runtimeUserManaged(cfg) {
		return nil
	}

	name := runtimeUserName(cfg)
	u, err := user.Lookup(name)
	if err != nil {
		return []Problem{{
			Name:   "umu-runtime-user-missing",
			Detail: fmt.Sprintf("游戏实例需要以非 root 用户 %s 运行，但该用户不存在", name),
			Fix: fmt.Sprintf("重启 asa-server 会尝试自动创建；或手动 useradd -r -m %s；"+
				"或在 config.yaml 设 linux.umu_run_as_root: true 以 root 运行游戏", name),
		}}
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)

	var problems []Problem

	if cfg.RuntimeUID != 0 && uid != cfg.RuntimeUID {
		problems = append(problems, Problem{
			Name:   "umu-runtime-uid-mismatch",
			Detail: fmt.Sprintf("用户 %s 的 uid=%d，与 linux.umu_runtime_uid=%d 不一致", name, uid, cfg.RuntimeUID),
			Fix:    "改配置以匹配现有用户，或迁移相关子树属主到配置指定的 uid",
		})
	}

	if p := checkOwnedDir(u.HomeDir, uid, "umu-runtime-home-bad", "runtime 用户家目录"); p != nil {
		problems = append(problems, *p)
	}

	for _, dir := range rwSubtrees(cfg, true) {
		if !pathExists(dir) {
			continue
		}
		if bad := sampleOwnerMismatch(dir, uid); bad != "" {
			problems = append(problems, Problem{
				Name:   "umu-runtime-owner-drift",
				Detail: fmt.Sprintf("%s 下存在非 %s 拥有的条目（例：%s）", dir, name, bad),
				Fix:    "重启 asa-server 会自动 chown 修复；修不回来多半是 SELinux / 只读挂载 / NFS root_squash",
			})
			break
		}
	}

	if fi, err := os.Stat(cfg.BaseDir); err == nil && fi.Mode().Perm()&0o001 == 0 {
		problems = append(problems, Problem{
			Name:   "basedir-not-traversable",
			Detail: fmt.Sprintf("%s 缺少 o+x，非 root 的 %s 无法穿过它访问下层目录", cfg.BaseDir, name),
			Fix:    fmt.Sprintf("chmod o+x %s", cfg.BaseDir),
		})
	}

	if entry := filepath.Join(protonPath(cfg), "proton"); pathExists(entry) {
		if fi, err := os.Stat(entry); err == nil && fi.Mode().Perm()&0o005 != 0o005 {
			problems = append(problems, Problem{
				Name:   "umu-runtime-ro-perm",
				Detail: fmt.Sprintf("%s 不是所有人可读+执行，降权后的游戏进程无法启动 Proton", entry),
				Fix:    fmt.Sprintf("chmod -R a+rX %s", protonPath(cfg)),
			})
		}
	}

	if forceDeep || cfg.RuntimeDeepProbe {
		if p := deepProbeWrite(cfg, uid, gid); p != nil {
			problems = append(problems, *p)
		}
	}
	return problems
}

func checkOwnedDir(path string, wantUID int, id, label string) *Problem {
	if path == "" {
		return &Problem{Name: id, Detail: label + "：路径为空"}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return &Problem{
			Name:   id,
			Detail: fmt.Sprintf("%s（%s）不可用: %v", label, path, err),
			Fix:    "重启 asa-server 会尝试重建；或手动创建并 chown 给 runtime 用户",
		}
	}
	if !fi.IsDir() {
		return &Problem{Name: id, Detail: fmt.Sprintf("%s（%s）不是目录", label, path)}
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != wantUID {
		return &Problem{
			Name:   id,
			Detail: fmt.Sprintf("%s（%s）属主 uid=%d，应为 %d", label, path, st.Uid, wantUID),
			Fix:    fmt.Sprintf("chown -R %d %s", wantUID, path),
		}
	}
	return nil
}

// sampleOwnerMismatch walks up to a cap of entries under dir and returns the
// first whose owner uid != wantUID (empty string = all sampled entries OK).
// Sampled, not exhaustive: the authoritative full-tree fix is
// reconcileRuntimeOwnership's WalkDir, which runs just before this in the
// same startup action. See docs/UMU_RUNTIME_USER_PLAN.md §9 risk 9.
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

// deepProbeWrite actually writes a probe file under the shared prefix as the
// dropped user — catches "owner looks right but a write still fails" (SELinux,
// ACLs, noexec/ro mount) that a stat-only check can't see.
func deepProbeWrite(cfg Config, uid, gid int) *Problem {
	dir := prefixDir(cfg, "")
	if !pathExists(dir) {
		return nil
	}
	probe := filepath.Join(dir, ".asa-umu-write-probe")
	cmd := exec.Command("/bin/sh", "-c", `touch "$0" && rm -f "$0"`, probe)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
		Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{uint32(gid)},
	}}
	if out, err := cmd.CombinedOutput(); err != nil {
		return &Problem{
			Name:   "umu-runtime-probe-failed",
			Detail: fmt.Sprintf("以 uid=%d 实际写 %s 失败：%v (%s)", uid, dir, err, strings.TrimSpace(string(out))),
			Fix:    "属主看着对但实际写不了——检查 SELinux (setenforce 0 验证) / ACL / 挂载选项 (ro, noexec)",
		}
	}
	return nil
}

// RuntimeUserInfo is the preflight-facing summary of the drop-privileges state.
func runtimeUserInfo() RuntimeUserInfo {
	cfg := getConfig()
	info := RuntimeUserInfo{
		Managed:  os.Geteuid() == 0 && !cfg.RunAsRoot,
		Bypassed: os.Geteuid() == 0 && cfg.RunAsRoot,
		Name:     runtimeUserName(cfg),
	}
	if !info.Managed {
		info.Ready = true
		return info
	}
	info.Ready = len(verifyRuntimeAccess(false)) == 0
	return info
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// runtimeEnv rewrites HOME/USER/LOGNAME to the dropped user and strips
// root-inherited XDG_* so the child's runtime cache lands under the right home.
func runtimeEnv(base []string, home, userName string) []string {
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
