//go:build linux

package runner

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// When this process isn't root there's nothing to drop to and nothing to
// check — every entry point must be an inert no-op. CI runs as non-root, so
// this is the path that actually gets exercised there.
func TestRuntimeUser_NoopWhenNotRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("this test asserts the non-root behaviour")
	}
	Configure(Config{BaseDir: t.TempDir()})

	if err := ensureRuntimeUser(context.Background()); err != nil {
		t.Fatalf("ensureRuntimeUser as non-root: want nil, got %v", err)
	}
	if p := verifyRuntimeAccess(false); p != nil {
		t.Fatalf("verifyRuntimeAccess as non-root: want nil, got %v", p)
	}
	if p := verifyRuntimeAccess(true); p != nil {
		t.Fatalf("verifyRuntimeAccess(deep) as non-root: want nil, got %v", p)
	}
	if err := chownMirrorForRuntime(t.TempDir()); err != nil {
		t.Fatalf("chownMirrorForRuntime as non-root: want nil, got %v", err)
	}
	if got := runtimeUserInfo(); !got.Ready || got.Managed || got.Bypassed {
		t.Fatalf("runtimeUserInfo as non-root: want {Ready:true,Managed:false,Bypassed:false}, got %+v", got)
	}
}

func TestRuntimeUser_ManagedOnlyWhenRootAndNotOptedOut(t *testing.T) {
	Configure(Config{BaseDir: t.TempDir(), RunAsRoot: true})
	if sysUserFor(getConfig()).Managed() {
		t.Fatal("RunAsRoot=true must disable management even as root")
	}
	Configure(Config{BaseDir: t.TempDir()})
	if got, want := sysUserFor(getConfig()).Managed(), os.Geteuid() == 0; got != want {
		t.Fatalf("sysuser.Manager.Managed: got %v, want %v (euid=%d)", got, want, os.Geteuid())
	}
}

func TestRuntimeEnv_RewritesHomeAndStripsXDG(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"HOME=/root",
		"USER=root",
		"LOGNAME=root",
		"XDG_RUNTIME_DIR=/run/user/0",
		"XDG_CACHE_HOME=/root/.cache",
		"WINEPREFIX=/x",
	}
	got := runtimeEnv(base, "/var/lib/asa-umu-runtime", "asa-umu-runtime")

	for _, kv := range got {
		if strings.HasPrefix(kv, "XDG_") {
			t.Errorf("XDG_ var survived: %q", kv)
		}
		if kv == "HOME=/root" || kv == "USER=root" || kv == "LOGNAME=root" {
			t.Errorf("root identity var survived: %q", kv)
		}
	}
	for _, want := range []string{
		"HOME=/var/lib/asa-umu-runtime",
		"USER=asa-umu-runtime",
		"LOGNAME=asa-umu-runtime",
		"PATH=/usr/bin",
		"WINEPREFIX=/x",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}

func TestRuntimeEnv_NoHomeIsPassthrough(t *testing.T) {
	base := []string{"HOME=/root", "PATH=/usr/bin"}
	if got := runtimeEnv(base, "", "x"); !slices.Equal(got, base) {
		t.Fatalf("empty home should pass env through unchanged: got %v", got)
	}
}

// TestRuntimeUser_CreateReconcileVerify exercises the real root path: create
// the account, chown a fake BaseDir's runtime subtrees to it, self-check, then
// userdel. Invasive (touches /etc/passwd), so it's opt-in via
// ASA_TEST_RUNTIME_USER=1 and needs root + useradd/userdel.
func TestRuntimeUser_CreateReconcileVerify(t *testing.T) {
	if os.Getenv("ASA_TEST_RUNTIME_USER") == "" {
		t.Skip("set ASA_TEST_RUNTIME_USER=1 to run (creates+deletes a real system user)")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if findAdminTool("useradd") == "" || findAdminTool("userdel") == "" {
		t.Skip("needs useradd/userdel")
	}

	base := t.TempDir()
	const name = "asa-umu-test"
	Configure(Config{BaseDir: base, RuntimeUser: name})
	t.Cleanup(func() {
		_ = exec.Command(findAdminTool("userdel"), "-r", name).Run()
	})

	// Pre-create the dirs the reconcile chowns.
	for _, d := range []string{"umu-prefix", "clusters", "proton/GE-Proton10-34"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "proton/GE-Proton10-34/proton"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ensureRuntimeUser(context.Background()); err != nil {
		t.Fatalf("ensureRuntimeUser: %v", err)
	}
	u, err := user.Lookup(name)
	if err != nil {
		t.Fatalf("user not created: %v", err)
	}
	uid, _ := strconv.Atoi(u.Uid)

	// prefix + clusters must now be owned by the new user.
	for _, d := range []string{"umu-prefix", "clusters", "runtime-home"} {
		p := filepath.Join(base, d)
		if d == "runtime-home" {
			p = u.HomeDir
		}
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if st := fi.Sys().(*syscall.Stat_t); int(st.Uid) != uid {
			t.Errorf("%s owned by uid %d, want %d", p, st.Uid, uid)
		}
	}

	if probs := verifyRuntimeAccess(false); len(probs) != 0 {
		t.Fatalf("verifyRuntimeAccess after reconcile: %v", probs)
	}

	// Break ownership, self-check must catch it.
	_ = os.Lchown(filepath.Join(base, "umu-prefix"), 0, 0)
	probs := verifyRuntimeAccess(false)
	found := false
	for _, p := range probs {
		if p.Name == "umu-runtime-owner-drift" {
			found = true
		}
	}
	if !found {
		t.Fatalf("owner drift not detected, got %v", probs)
	}
}
