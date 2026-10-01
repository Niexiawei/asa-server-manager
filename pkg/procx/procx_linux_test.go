//go:build linux

package procx

import (
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestParsePPIDFromStat(t *testing.T) {
	tests := []struct {
		name string
		line string
		want int
		ok   bool
	}{
		{
			name: "plain comm",
			line: "1234 (bash) S 1000 1234 1234 34816 1234 4194304 ...",
			want: 1000,
			ok:   true,
		},
		{
			// The whole reason this isn't a strings.Fields split: comm can
			// contain both spaces and a closing paren, and only the LAST one
			// ends the field.
			name: "comm with spaces and parens",
			line: "42 (Web Content (x)) S 7 42 42 0 -1 4194560 ...",
			want: 7,
			ok:   true,
		},
		{
			name: "no closing paren",
			line: "42 broken",
			ok:   false,
		},
		{
			name: "truncated after comm",
			line: "42 (x) S",
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parsePPIDFromStat([]byte(tt.line))
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("ppid = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestReadPPIDMatchesGetppid(t *testing.T) {
	got, ok := readPPID(os.Getpid())
	if !ok {
		t.Fatal("readPPID failed for our own pid")
	}
	if want := os.Getppid(); got != want {
		t.Errorf("readPPID = %d, want %d", got, want)
	}
}

// TestProcessTreeFindsGrandchild is the regression this whole change exists
// for: a descendant that put itself in its own session (exactly what
// pressure-vessel and Wine do on the way to ArkAscendedServer.exe) must still
// be found, even though kill(-pgid) on the root would never reach it.
func TestProcessTreeFindsGrandchild(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh available")
	}

	// sh spawns sleep and waits: sleep is a grandchild of this test process.
	cmd := exec.Command(sh, "-c", "sleep 30 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = KillTree(cmd.Process.Pid)
		_, _ = cmd.Process.Wait()
	})

	var tree []int
	for range 50 { // the grandchild needs a moment to appear
		tree = processTree(cmd.Process.Pid)
		if len(tree) > 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !slices.Contains(tree, cmd.Process.Pid) {
		t.Fatalf("processTree(%d) = %v, missing the root itself", cmd.Process.Pid, tree)
	}
	if len(tree) < 2 {
		t.Fatalf("processTree(%d) = %v, expected the grandchild too", cmd.Process.Pid, tree)
	}
	if tree[0] != cmd.Process.Pid {
		t.Errorf("tree[0] = %d, want the root %d (parents must come first)", tree[0], cmd.Process.Pid)
	}
}

func TestSignalTreeRefusesInit(t *testing.T) {
	if err := signalTree(1, syscall.SIGTERM); err == nil {
		t.Error("signalTree(1) returned nil; it must refuse to signal init")
	}
}

func TestParseStat(t *testing.T) {
	// A comm with spaces and a ')' of its own, like Wine's renamed threads.
	line := "4242 (Game) Thread) S 1 4242 4242 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 8 0 987654 1000 10 18446744073709551615"
	st, ok := parseStat([]byte(line))
	if !ok {
		t.Fatal("parseStat failed")
	}
	if st.state != 'S' || st.start != 987654 {
		t.Errorf("parseStat = %+v, want state S start 987654", st)
	}
	if _, ok := parseStat([]byte("4242 (x) S 1")); ok {
		t.Error("parseStat accepted a truncated line")
	}
}

// The regression: a tree that ignores SIGTERM must still be gone after the
// grace period. TerminateTree alone returns nil (delivery succeeded) and the
// old "escalate on error" never fired.
func TestTerminateTreeGracefullyEscalates(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh available")
	}
	// Both the shell and its child ignore SIGTERM.
	cmd := exec.Command(sh, "-c", `trap "" TERM; sh -c 'trap "" TERM; sleep 60' & wait`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	reaped := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(reaped) }()
	t.Cleanup(func() { _ = KillTree(cmd.Process.Pid) })

	var tree []int
	for range 100 {
		if tree = processTree(cmd.Process.Pid); len(tree) >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(tree) < 3 {
		t.Fatalf("tree did not grow: %v", tree)
	}
	time.Sleep(100 * time.Millisecond) // let the traps install

	start := time.Now()
	if err := TerminateTreeGracefully(cmd.Process.Pid, 500*time.Millisecond); err != nil {
		t.Fatalf("TerminateTreeGracefully: %v", err)
	}
	if d := time.Since(start); d < 400*time.Millisecond {
		t.Errorf("returned after %v; SIGTERM-ignoring tree should have used the grace period", d)
	}
	select {
	case <-reaped:
	case <-time.After(5 * time.Second):
		t.Fatal("root still running after escalation")
	}
	for _, p := range tree[1:] {
		if st, ok := readStat(p); ok && st.state != 'Z' {
			t.Errorf("descendant %d survived (state %c)", p, st.state)
		}
	}
}

// A tree that honours SIGTERM must not be held for the whole grace period.
func TestTerminateTreeGracefullyReturnsEarly(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skip("no sleep available")
	}
	go func() { _, _ = cmd.Process.Wait() }()
	t.Cleanup(func() { _ = KillTree(cmd.Process.Pid) })

	start := time.Now()
	if err := TerminateTreeGracefully(cmd.Process.Pid, 10*time.Second); err != nil {
		t.Fatalf("TerminateTreeGracefully: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v for a process that exits on SIGTERM", d)
	}
}
