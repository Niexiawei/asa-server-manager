//go:build linux

package xvfb

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestArgsShape: -displayfd 那一档的参数形态。
//
// 两条硬约束：
//   - **不能同时给显示号位置参数**，X server 里这两者互斥，给了就起不来；
//   - -nolisten tcp / -noreset / -ac 一个都不能少（不开网络监听、客户端断开不重置、
//     无认证——最后一条是 DisplayUsable 那个无认证握手能探测到自己的前提）。
func TestArgsShape(t *testing.T) {
	got := args(Config{}, displayFD)

	if got[0] != "-displayfd" || got[1] != "3" {
		t.Fatalf("args = %v, want it to start with -displayfd 3", got)
	}
	for _, a := range got {
		if strings.HasPrefix(a, ":") {
			t.Errorf("args carry an explicit display number %q, which is mutually exclusive with -displayfd: %v", a, got)
		}
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"-screen 0 " + defaultScreen, "-nolisten tcp", "-noreset", "-ac"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args are missing %q: %v", want, got)
		}
	}
	// -auth 是刻意不给的：带 cookie 之后我们自己的握手探测就连不上自己了。
	if strings.Contains(joined, "-auth") {
		t.Errorf("args carry -auth; the probe in DisplayUsable is unauthenticated: %v", got)
	}
}

// TestArgsForDisplay: 回退形态（老 X server 没有 -displayfd）显示号在第一位。
func TestArgsForDisplay(t *testing.T) {
	got := argsForDisplay(Config{Screen: "1024x768x16"}, ":100")

	if got[0] != ":100" {
		t.Errorf("args[0] = %q, want the display number first", got[0])
	}
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "-displayfd") {
		t.Errorf("the fallback form must not use -displayfd: %v", got)
	}
	if !strings.Contains(joined, "-screen 0 1024x768x16") {
		t.Errorf("Config.Screen was ignored: %v", got)
	}
}

// TestParseDisplayFD: X server 写回来的是一行数字。
func TestParseDisplayFD(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"100\n", "100", false},
		{"7", "7", false},
		{"  42 \n", "42", false},
		{"100\n101\n", "100", false}, // 只取第一行
		{"", "", true},
		{"\n", "", true},
		{"abc\n", "", true},
	}
	for _, tt := range tests {
		got, err := parseDisplayFD(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseDisplayFD(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("parseDisplayFD(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestRejectedDisplayFD: 只有「这个 X server 不认识 -displayfd」才该触发回退。
// 认错了会在缺字体一类的真故障上白试十个显示号。
func TestRejectedDisplayFD(t *testing.T) {
	yes := []string{
		"Unrecognized option: -displayfd\nuse: X [:<display>] [option]",
		"unknown option -displayfd",
	}
	no := []string{
		"",
		"Fatal server error:\ncould not open default font 'fixed'",
		"Server is already active for display 100",
	}
	for _, s := range yes {
		if !rejectedDisplayFD(s) {
			t.Errorf("rejectedDisplayFD(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if rejectedDisplayFD(s) {
			t.Errorf("rejectedDisplayFD(%q) = true, want false", s)
		}
	}
}

// TestFailureHintFonts: 最小化安装缺字体是已知的第一手失败，必须给出装哪个包。
// 这个故障在 xvfb-run 时代同样存在，只是输出被丢进了 /dev/null。
func TestFailureHintFonts(t *testing.T) {
	hint := failureHint("Fatal server error:\n(EE) could not open default font 'fixed'")
	if hint == "" {
		t.Fatal("failureHint gave nothing for the missing-font failure")
	}
	for _, pkg := range []string{"xfonts-base", "xorg-x11-fonts-misc", "xorg-fonts-misc"} {
		if !strings.Contains(hint, pkg) {
			t.Errorf("font hint doesn't name %s: %q", pkg, hint)
		}
	}
	if failureHint("nothing interesting here") != "" {
		t.Error("failureHint invented a hint for an unknown failure")
	}
}

// TestDisplayInUse: 换个显示号重试只对「号被占了」有意义。
func TestDisplayInUse(t *testing.T) {
	if !displayInUse("Server is already active for display 100") {
		t.Error("displayInUse missed the already-active message")
	}
	if displayInUse("could not open default font 'fixed'") {
		t.Error("displayInUse treated a font failure as a display clash; retrying would just fail 10 more times")
	}
}

// TestInstallHintCoversDistros: 提示必须覆盖三大包管理器，且指向**提供 Xvfb 的
// 包**。只写 Debian 那一家正是这次要修的 bug 的源头。
func TestInstallHintCoversDistros(t *testing.T) {
	for _, want := range []string{"apt install xvfb", "dnf install xorg-x11-server-Xvfb", "pacman -S xorg-server-xvfb"} {
		if !strings.Contains(InstallHint, want) {
			t.Errorf("InstallHint is missing %q: %s", want, InstallHint)
		}
	}
	if strings.Contains(InstallHint, "xvfb-run") {
		t.Error("InstallHint still tells people to install xvfb-run — that script only exists on Debian")
	}
}

// TestBinaryPathRejectsBadConfig: Config.Bin 指错了要当场说清楚，
// 而不是悄悄退回 PATH 上的另一个 Xvfb。
func TestBinaryPathRejectsBadConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "Xvfb")
	if _, err := binaryPath(Config{Bin: missing}); err == nil {
		t.Error("binaryPath accepted a Config.Bin that doesn't exist")
	}

	notExec := filepath.Join(t.TempDir(), "Xvfb")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := binaryPath(Config{Bin: notExec}); err == nil {
		t.Error("binaryPath accepted a non-executable Config.Bin")
	}
	if err := os.Chmod(notExec, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := binaryPath(Config{Bin: notExec}); err != nil || got != notExec {
		t.Errorf("binaryPath(%q) = (%q, %v), want it used verbatim", notExec, got, err)
	}
}

// TestStateRoundTrip: state 文件是「重启后别再多起一个 Xvfb」的全部依据。
func TestStateRoundTrip(t *testing.T) {
	cfg := Config{StatePath: filepath.Join(t.TempDir(), "xvfb.state")}
	writeState(cfg, &managedXvfb{display: ":100", pid: 4242})

	st, err := readState(cfg)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if st.Display != ":100" || st.PID != 4242 {
		t.Errorf("state = %+v, want display :100 / pid 4242", st)
	}
	if st.Started == "" {
		t.Error("state has no start time")
	}
}

// TestStateWithoutPath: 没有 StatePath（单测、早期启动）时不该乱写文件，
// 也不该 panic —— 认领功能降级即可。
func TestStateWithoutPath(t *testing.T) {
	writeState(Config{}, &managedXvfb{display: ":100", pid: 1})
	if _, err := readState(Config{}); err == nil {
		t.Error("readState succeeded with no StatePath")
	}
	if adopt(Config{}) != nil {
		t.Error("adopt invented a server with no state to read")
	}
}

// TestAdoptRejectsDeadPID: 认领要过三道关（进程活着、是 Xvfb、显示能握手）。
// pid 1 活着但不是 Xvfb，是最省事的一道反例。
func TestAdoptRejectsDeadPID(t *testing.T) {
	cfg := Config{StatePath: filepath.Join(t.TempDir(), "xvfb.state")}
	writeState(cfg, &managedXvfb{display: ":99999", pid: 1})
	if adopt(cfg) != nil {
		t.Error("adopt adopted pid 1, which is not an Xvfb")
	}
}

// TestLogTailOnlyThisRun: 日志是追加写的，诊断只能看本次启动之后的内容 ——
// 否则会把上几次的错误当成这次的。
func TestLogTailOnlyThisRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xvfb.log")
	if err := os.WriteFile(path, []byte("上一次的错误\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	off, _ := f.Seek(0, io.SeekEnd)
	if _, err := f.WriteString("这一次的错误\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	tail := (&managedXvfb{log: path, logStart: off}).logTail()
	if strings.Contains(tail, "上一次") {
		t.Errorf("logTail leaked a previous run's output: %q", tail)
	}
	if !strings.Contains(tail, "这一次") {
		t.Errorf("logTail = %q, want this run's output", tail)
	}
}

// TestOpenLogTruncatesOversized: 这个文件没有保留历史的价值，但不能无限长。
func TestOpenLogTruncatesOversized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xvfb.log")
	if err := os.WriteFile(path, make([]byte, logMaxBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 0 {
		t.Errorf("xvfb.log size = %d after reopening an oversized log, want it truncated", fi.Size())
	}
}

// TestSysProcAttrFollowsProcess: Pdeathsig 是「Xvfb 跟着宿主进程一起走」的
// 兜底层 —— 宿主进程被 SIGKILL / panic / OOM 时，显式停止那段代码根本没机会跑，
// 只剩内核能收拾。用 SIGTERM 而不是 SIGKILL，好让 X 服务端自己清掉
// SocketDir/X<n> 与 /tmp/.X<n>-lock。
func TestSysProcAttrFollowsProcess(t *testing.T) {
	attr := sysProcAttr(nil)

	if attr.Pdeathsig != syscall.SIGTERM {
		t.Errorf("Pdeathsig = %v, want SIGTERM —— 没有它，宿主进程被 kill -9 之后 Xvfb 会留下来", attr.Pdeathsig)
	}
	if !attr.Setsid {
		t.Error("Setsid = false: Xvfb 不该挂在宿主进程的控制终端上")
	}
}

// TestSpawnStartUsesDedicatedThread: 所有 Xvfb 都必须由那个 LockOSThread 且
// 永不返回的 goroutine 来 fork（否则 Pdeathsig 跟的线程可能先于进程退出，
// 把正在服务的 X 服务端莫名其妙杀掉）。这里跑一条最短的命令，验证这条通道
// 本身是通的 —— 派发、执行、把错误带回来。
func TestSpawnStartUsesDedicatedThread(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("没有 /bin/true，跳过")
	}
	m := New(Config{})

	cmd := exec.Command(bin)
	if err := m.spawnStart(cmd); err != nil {
		t.Fatalf("spawnStart: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("命令没有正常结束: %v", err)
	}

	// 派发第二条，确认那个 goroutine 是常驻的而不是一次性的。
	cmd2 := exec.Command(bin)
	if err := m.spawnStart(cmd2); err != nil {
		t.Fatalf("第二次 spawnStart 失败（spawn 循环退出了？）: %v", err)
	}
	_ = cmd2.Wait()
}

// TestStopClearsCurrent: 显式停止之后，单例必须被清空且打上
// intentional 标记 —— 否则看门狗会在关停途中抢着补起一个马上又要被杀的 X 服务。
func TestStopClearsCurrent(t *testing.T) {
	m := New(Config{})
	// adopted:true 让 stop() 走「不是我们的，不动它」那条短路，所以这里放本进程的
	// pid 也是安全的；同时它也钉住了「认领来的不归我们杀」这条不变量。
	x := &managedXvfb{display: ":99999", pid: os.Getpid(), adopted: true}
	m.current.Store(x)

	m.Stop()

	if m.Status().Running {
		t.Error("Stop 没有清空单例")
	}
	if !x.intentional.Load() {
		t.Error("Stop 没有打 intentional 标记，看门狗会来复活它")
	}
	m.Stop() // 幂等：退出路径可能被走两遍
}

// TestWatchIgnoresIntentionalStop: 看门狗对「我们自己停的」必须立刻收手。
// 否则宿主进程关停时它会去起一个新的 Xvfb，然后被留在机器上。
func TestWatchIgnoresIntentionalStop(t *testing.T) {
	m := New(Config{})
	x := &managedXvfb{display: ":99999", pid: -1, exited: make(chan struct{})}
	x.intentional.Store(true)
	close(x.exited)

	done := make(chan struct{})
	go func() {
		m.watch(x)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watch 在主动停止后仍在尝试重启")
	}
}

// TestWatchIgnoresReplaced: 已经被换掉的那个（current 指向别人）同样不该
// 由旧看门狗插手 —— 否则一次重启会留下两只看门狗互相打架。
func TestWatchIgnoresReplaced(t *testing.T) {
	m := New(Config{})
	old := &managedXvfb{display: ":99998", pid: -1, exited: make(chan struct{})}
	close(old.exited)
	m.current.Store(&managedXvfb{display: ":99997", pid: -1})

	done := make(chan struct{})
	go func() {
		m.watch(old)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watch 对一个已经被换掉的 Xvfb 仍在尝试重启")
	}
}

// --- socket 目录权限 -------------------------------------------------------------

// TestStatWritableByHonoursOwnerBit 是一次真机故障的回归测试。
//
// 现场：
//
//	drwxr-xr-x. 2 asa-umu-runtime asa-umu-runtime /tmp/.X11-unix
//
// 目录是**上一轮那个降权 Xvfb 自己建的**（非 root 的 X 服务端建不出 1777，落到
// umask 022 就是 0755，属主正是它）。那个用户是属主、写得进去，而旧代码只看 o+w，
// 于是判它写不进去。属主位必须优先于 other 位，这正是内核的判法。
func TestStatWritableByHonoursOwnerBit(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no syscall.Stat_t on this platform")
	}

	// 属主：0755 的 u+w 是有的。
	if !statWritableBy(fi, st.Uid, st.Gid) {
		t.Error("owner of a 0755 dir judged unwritable — 这正是旧的 o+w 判据犯的错")
	}
	// 既不是属主也不是属组：0755 没有 o+w，写不进去。
	if statWritableBy(fi, noSuchID, noSuchID) {
		t.Error("a stranger judged writable on a 0755 dir")
	}
	// 属主匹配时属组位不参与，哪怕属组更宽松也一样（内核的顺序）。
	if err := os.Chmod(dir, 0o477); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	fi, _ = os.Stat(dir)
	if statWritableBy(fi, st.Uid, st.Gid) {
		t.Error("owner bit must win even when the group bits are more permissive")
	}
}

// TestStatWritableByGroupBit: 属组这一档也要真的算，不能退回 o+w。
func TestStatWritableByGroupBit(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o070); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no syscall.Stat_t on this platform")
	}
	if !statWritableBy(fi, noSuchID, st.Gid) {
		t.Error("group member judged unwritable on a 0070 dir")
	}
	if statWritableBy(fi, noSuchID, noSuchID) {
		t.Error("a stranger judged writable on a 0070 dir")
	}
}

// TestSocketDirModeIsSticky1777: 必须是 os.ModeSticky|0777，不是字面量 0o1777 ——
// os.FileMode 的 sticky 位是 1<<20，写成八进制的 01000 会落进权限位以外的空档，
// 建出来的目录没有 sticky，别人就能删掉我们的 socket。
func TestSocketDirModeIsSticky1777(t *testing.T) {
	if socketDirMode.Perm() != 0o777 {
		t.Errorf("socketDirMode.Perm() = %04o, want 0777", socketDirMode.Perm())
	}
	if socketDirMode&os.ModeSticky == 0 {
		t.Error("socketDirMode 少了 sticky 位：谁的 socket 就该只有谁删得掉")
	}
}

// TestEnsureSocketDirIsRootOnly: 非 root 时它必须什么都不做 —— 既建不出 1777
// 也 chmod 不动别人的目录，硬试只会拿一个误导性的错误去挡住启动。
func TestEnsureSocketDirIsRootOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("以 root 运行，这条断言的前提不成立")
	}
	m := New(Config{})
	if err := m.ensureSocketDir(Config{}); err != nil {
		t.Errorf("ensureSocketDir 在非 root 下返回了错误: %v", err)
	}
}

// TestRemountRespectsAllowX11Remount: 开关关掉时**不许动宿主的挂载表**，并且要在
// 错误里点名是配置挡住的。这条能在任何机器上跑：目录可写时该函数是空操作，
// 只读时也在真正 mount 之前就返回了。
func TestRemountRespectsAllowX11Remount(t *testing.T) {
	m := New(Config{})
	before := m.remounted.Load()
	defer m.remounted.Store(before)

	err := m.remountSocketDirRW(Config{AllowX11Remount: false})
	if m.remounted.Load() != before {
		t.Error("AllowX11Remount=false 却改了挂载状态")
	}
	if err == nil {
		return // 这台机器上 SocketDir 本来就可写，没有可断言的错误
	}
	if !strings.Contains(err.Error(), "AllowX11Remount") {
		t.Errorf("拒绝原因 %q 没有点名是哪个配置项挡住的", err)
	}
}

// TestRemountIsNoOpWhenWritable: 常规路径（普通 Linux，目录本来就可写）一个 mount
// syscall 都不该多花，更不该因为开关关着就报错 —— 那会把 WSL 之外的所有机器
// 一起挡住。
func TestRemountIsNoOpWhenWritable(t *testing.T) {
	if syscall.Access(SocketDir, writeOK) != nil {
		t.Skip("这台机器上 SocketDir 不可写，前提不成立")
	}
	m := New(Config{})
	before := m.remounted.Load()
	defer m.remounted.Store(before)

	if err := m.remountSocketDirRW(Config{AllowX11Remount: false}); err != nil {
		t.Errorf("目录可写时 remountSocketDirRW 不该失败: %v", err)
	}
	if m.remounted.Load() != before {
		t.Error("目录可写时不该记下「我们改过挂载」")
	}
}

// TestRestoreSocketDirROOnlyTouchesOurOwn: 不是我们改的挂载点就不许碰 ——
// 把别人有意挂成可写的目录改回只读，是一个我们无权做的决定。
func TestRestoreSocketDirROOnlyTouchesOurOwn(t *testing.T) {
	m := New(Config{})
	m.remounted.Store(false)
	m.restoreSocketDirRO() // 必须是空操作，不 panic、不改状态
	if m.remounted.Load() {
		t.Error("restoreSocketDirRO 在没改过的情况下动了状态")
	}
}

// TestAcquireEndToEnd 是真机集成测试：真的 fork 一个 Xvfb、握手、Stop、确认幂等，
// 覆盖单测桩不到的部分——尤其是 WSLg 那种 SocketDir 只读挂载下的 remount 分支。
// 侵入性（可能 remount 一个系统级挂载点），opt-in 才跑，需要 root + Xvfb。
func TestAcquireEndToEnd(t *testing.T) {
	if os.Getenv("ASA_TEST_XVFB") == "" {
		t.Skip("set ASA_TEST_XVFB=1 to run (spawns a real Xvfb, may remount /tmp/.X11-unix)")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root (to remount SocketDir and/or drop to a runtime user)")
	}
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb not installed")
	}

	m := New(Config{
		StatePath:       filepath.Join(t.TempDir(), "xvfb.state"),
		AllowX11Remount: true,
		HomeDir:         func() string { return t.TempDir() },
		ChildIDs:        func() (uint32, uint32, bool) { return 0, 0, false },
		Credential:      func() (*syscall.Credential, error) { return nil, nil },
	})

	display, err := m.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if display == "" {
		t.Fatal("Acquire returned an empty display")
	}
	if !DisplayUsable(display) {
		t.Fatalf("Acquire returned %q but it doesn't handshake", display)
	}

	status := m.Status()
	if !status.Running || status.Display != display {
		t.Fatalf("Status() = %+v, want Running with Display %q", status, display)
	}

	// 第二次 Acquire 必须复用同一个显示，而不是再拉起一个。
	again, err := m.Acquire()
	if err != nil || again != display {
		t.Fatalf("second Acquire = (%q, %v), want the same display reused", again, err)
	}

	m.Stop()
	if m.Status().Running {
		t.Fatal("Stop left the singleton marked as running")
	}
	if DisplayUsable(display) {
		t.Fatal("display is still usable after Stop — Xvfb wasn't actually killed")
	}
	m.Stop() // 幂等：不许 panic 或第二次去杀一个已经不存在的进程
}
