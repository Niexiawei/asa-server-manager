//go:build linux

package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"asa-server/pkg/fsutil"
	"asa-server/pkg/shareacl"
	"asa-server/pkg/sysuser"
)

// This file implements "run the game instance as a dedicated non-root user"
// — see docs/UMU_RUNTIME_USER_PLAN.md. asa-server itself keeps running as
// root; only the umu-run child (and the whole bwrap/wine/ArkAscendedServer.exe
// tree below it) is dropped to asa-umu-runtime via SysProcAttr.Credential.
//
// The account/credential/chown mechanism itself lives in pkg/sysuser (it
// doesn't know about ASA, mirrors, or overlays). This file supplies that
// mechanism with the directory lists and business decisions that do know:
// which paths are shared with root (sharedaccess_linux.go), which are
// exclusively the runtime user's (rwSubtrees below), and how a runner.Config
// maps onto a sysuser.Config.
//
// noSuchID is a uid/gid that matches nothing on any Linux system (it is
// (uid_t)-1, the "no change" sentinel of setuid/chown, never a real
// account). runtimeChildIDs hands it back when the runtime user doesn't
// exist yet, so a permission judgement built on it lands on the conservative
// branch instead of silently assuming ownership. Mirrors the identically-
// named sentinel in pkg/sysuser (unexported there): xvfb_linux.go compares
// runtimeChildIDs' result against it directly.
const noSuchID = ^uint32(0)

// sysUserFor builds a fresh *sysuser.Manager from the live Config on every
// call, deliberately not a package-level singleton: unlike xvfb's persistent
// spawn-loop goroutine, sysuser.Manager holds no state worth keeping across
// calls, so there is no "New vs Reconfigure" question here — a Config change
// between two calls (e.g. the GUI re-applying settings) just takes effect on
// the next one.
func sysUserFor(cfg Config) *sysuser.Manager {
	return sysuser.New(sysuser.Config{
		Name:         cfg.RuntimeUser,
		UID:          cfg.RuntimeUID,
		GID:          cfg.RuntimeGID,
		RunAsRoot:    cfg.RunAsRoot,
		HomeFallback: filepath.Join(cfg.BaseDir, "runtime-home"),
		DeepProbe:    cfg.RuntimeDeepProbe,
	})
}

func runtimeUserManaged(cfg Config) bool { return sysUserFor(cfg).Managed() }

func runtimeUserName(cfg Config) string { return sysUserFor(cfg).UserName() }

// runtimeUserName is also needed without a Config in hand (svcmgr / systemapi).
func RuntimeUserName() string { return runtimeUserName(getConfig()) }

// runtimeHomeDir resolves the HOME the dropped child must see. When we are not
// managing a separate user it's just this process's own home.
func runtimeHomeDir(cfg Config) string { return sysUserFor(cfg).HomeDir() }

// runtimeChildIDs is the **read-only** counterpart of resolveRuntimeCredential:
// the uid/gid the game child — and the Xvfb we start for it — will run as.
// managed is false when there is no drop at all, i.e. the child runs as this
// very process.
func runtimeChildIDs(cfg Config) (uid, gid uint32, managed bool) {
	return sysUserFor(cfg).ChildIDs()
}

// resolveRuntimeCredential returns the Credential to drop the child to, plus
// that user's HOME. Returns (nil, "", nil) when no drop should happen
// (euid != 0, or umu_run_as_root=true, or a non-linux build).
func resolveRuntimeCredential(cfg Config) (*syscall.Credential, string, error) {
	return sysUserFor(cfg).ResolveCredential()
}

// --- ownership reconcile -------------------------------------------------------

func ensureRuntimeUser(ctx context.Context) error {
	cfg := getConfig()
	su := sysUserFor(cfg)
	if err := su.EnsureUser(ctx); err != nil {
		return err
	}
	return reconcileRuntimeOwnership(cfg, su)
}

func reconcileRuntimeOwnership(cfg Config, su *sysuser.Manager) error {
	// Mirror dirs (server-files-tmp-*) are deliberately excluded here: they're
	// rebuilt per instance start and chowned then by ChownMirrorForRuntime, so
	// walking their tens of thousands of symlinks on every asa-server startup
	// would be pure overhead. verifyRuntimeAccess still samples them for drift.
	if err := su.ChownTree(rwSubtrees(cfg, false)...); err != nil {
		return err
	}

	uid, gid, managed := su.ChildIDs()
	if !managed {
		return nil
	}

	// server-files / instances are shared with root rather than handed over —
	// see sharedaccess_linux.go for why. Walking server-files (~50k entries)
	// on every startup would be pure overhead once it's already set up, so a
	// cheap sample decides whether the full pass is worth doing; the
	// authoritative unconditional pass runs after every SteamCMD update and
	// before verification (installer.go).
	group := shareacl.GroupName(strconv.Itoa(int(gid)))
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
		if !shareacl.NeedsPass(dir, int(gid)) && !shareacl.DefaultACLMissing(dir, group) {
			continue
		}
		if err := applySharedAccess(dir, int(uid), int(gid), group); err != nil {
			return fmt.Errorf("prepare shared access on %s: %w", dir, err)
		}
	}

	umuRT := umuRuntimeFor(cfg)
	if proton := umuRT.ProtonPath(); pathExists(proton) {
		if err := fsutil.EnsureWorldReadable(proton); err != nil {
			return fmt.Errorf("chmod %s: %w", proton, err)
		}
	}
	if umuDir := umuRT.Dir(); pathExists(umuDir) {
		if err := fsutil.EnsureWorldReadable(umuDir); err != nil {
			return fmt.Errorf("chmod %s: %w", umuDir, err)
		}
	}
	return nil
}

// rwSubtrees are the directories the dropped child writes to and therefore
// must own — see docs/UMU_RUNTIME_USER_PLAN.md §5.1. includeMirrors adds the
// per-instance server-files-tmp-* dirs (wanted for the verify sampling, not
// for the startup reconcile — see reconcileRuntimeOwnership).
func rwSubtrees(cfg Config, includeMirrors bool) []string {
	wp := wineprefixMgrFor(cfg)
	out := []string{
		wp.Dir(""),
		filepath.Join(cfg.BaseDir, "clusters"),
	}
	overlays := wp.OverlayRoot()
	if m, _ := filepath.Glob(wp.Dir("") + "-*"); len(m) > 0 {
		for _, p := range m {
			// The overlay root is not a prefix, and walking it here would be
			// actively harmful — see wineprefix.Manager.UnmountedOverlayDirs.
			if p == overlays {
				continue
			}
			out = append(out, p)
		}
	}
	// wineprefix.Manager.UnmountedOverlayDirs lists only the parts of
	// prefix_mode "overlay" that are ours to chown: a `merged` that is NOT
	// mounted (the copy fallback, an ordinary directory of real files). A
	// mounted layer contributes nothing — chown is a metadata write, and a
	// metadata write through an overlay **copies the file up**, silently
	// duplicating the shared lower into every instance's private layer on
	// every startup reconcile. `upper` and `work` are excluded for their own
	// reasons (see that method's doc) — `work` in particular is the
	// kernel's private scratch area (root-owned, mode 000), and listing it
	// is what made the very first real-hardware launch fail.
	out = append(out, wp.UnmountedOverlayDirs()...)
	if includeMirrors {
		if m, _ := filepath.Glob(filepath.Join(cfg.BaseDir, "server-files-tmp-*")); len(m) > 0 {
			out = append(out, m...)
		}
	}
	return out
}

// ChownMirrorForRuntime is called from instance start after the per-instance
// mirror is (re)built, before runner.Run. No-op unless we're managing a
// dropped user. The mirror is mostly symlinks into root-owned server-files;
// Lchown flips the links, real copied files get chowned for real.
func chownMirrorForRuntime(mirrorDir string) error {
	return sysUserFor(getConfig()).ChownTree(mirrorDir)
}

// ChownTreeForRuntime chowns an arbitrary path (recursively) to the runtime
// user. Used by installer fixups for the ~/.steam SDK symlink dir. No-op
// unless we're managing a dropped user.
func chownTreeForRuntime(root string) error {
	return sysUserFor(getConfig()).ChownTree(root)
}

// chownPathForRuntime chowns a single path (non-recursive) to the runtime
// user, so a freshly MkdirAll'd dir is writable by the dropped child.
func chownPathForRuntime(path string) error {
	return sysUserFor(getConfig()).ChownOne(path)
}

// --- access self-check ------------------------------------------------------

func verifyRuntimeAccess(forceDeep bool) []Problem {
	cfg := getConfig()
	return sysUserFor(cfg).Problems(sysuser.AccessCheck{
		OwnershipDirs:  rwSubtrees(cfg, true),
		TraversableDir: cfg.BaseDir,
		ReadableEntry:  filepath.Join(umuRuntimeFor(cfg).ProtonPath(), "proton"),
		ProbeDir:       wineprefixMgrFor(cfg).Dir(""),
	}, forceDeep)
}

// runtimeUserInfo is the preflight-facing summary of the drop-privileges state.
func runtimeUserInfo() RuntimeUserInfo {
	cfg := getConfig()
	status := sysUserFor(cfg).Status()
	info := RuntimeUserInfo{
		Managed:  status.Managed,
		Bypassed: status.Bypassed,
		Name:     status.Name,
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

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// runtimeEnv rewrites HOME/USER/LOGNAME to the dropped user and strips
// root-inherited XDG_* so the child's runtime cache lands under the right home.
func runtimeEnv(base []string, home, userName string) []string {
	return sysuser.Env(base, home, userName)
}
