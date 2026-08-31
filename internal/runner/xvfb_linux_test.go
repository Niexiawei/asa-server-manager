//go:build linux

package runner

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

// TestXvfbArgsShape: -displayfd 那一档的参数形态。
//
// 两条硬约束：
//   - **不能同时给显示号位置参数**，X server 里这两者互斥，给了就起不来；
//   - -nolisten tcp / -noreset / -ac 一个都不能少（不开网络监听、客户端断开不重置、
//     无认证——最后一条是 x11DisplayUsable 那个无认证握手能探测到自己的前提）。
func TestXvfbArgsShape(t *testing.T) {
	args := xvfbArgs(Config{}, xvfbDisplayFD)

	if args[0] != "-displayfd" || args[1] != "3" {
		t.Fatalf("args = %v, want it to start with -displayfd 3", args)
	}
	for _, a := range args {
		if strings.HasPrefix(a, ":") {
			t.Errorf("args carry an explicit display number %q, which is mutually exclusive with -displayfd: %v", a, args)
		}
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-screen 0 " + xvfbDefaultScreen, "-nolisten tcp", "-noreset", "-ac"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args are missing %q: %v", want, args)
		}
	}
	// -auth 是刻意不给的：带 cookie 之后我们自己的握手探测就连不上自己了。
	if strings.Contains(joined, "-auth") {
		t.Errorf("args carry -auth; the probe in x11DisplayUsable is unauthenticated: %v", args)
	}
}

// TestXvfbArgsForDisplay: 回退形态（老 X server 没有 -displayfd）显示号在第一位。
func TestXvfbArgsForDisplay(t *testing.T) {
	args := xvfbArgsForDisplay(Config{XvfbScreen: "1024x768x16"}, ":100")

	if args[0] != ":100" {
		t.Errorf("args[0] = %q, want the display number first", args[0])
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-displayfd") {
		t.Errorf("the fallback form must not use -displayfd: %v", args)
	}
	if !strings.Contains(joined, "-screen 0 1024x768x16") {
		t.Errorf("linux.xvfb_screen was ignored: %v", args)
	}
}

// TestParseXvfbDisplayFD: X server 写回来的是一行数字。
func TestParseXvfbDisplayFD(t *testing.T) {
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
		got, err := parseXvfbDisplayFD(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseXvfbDisplayFD(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("parseXvfbDisplayFD(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestXvfbRejectedDisplayFD: 只有「这个 X server 不认识 -displayfd」才该触发回退。
// 认错了会在缺字体一类的真故障上白试十个显示号。
func TestXvfbRejectedDisplayFD(t *testing.T) {
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
		if !xvfbRejectedDisplayFD(s) {
			t.Errorf("xvfbRejectedDisplayFD(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if xvfbRejectedDisplayFD(s) {
			t.Errorf("xvfbRejectedDisplayFD(%q) = true, want false", s)
		}
	}
}

// TestXvfbFailureHintFonts: 最小化安装缺字体是已知的第一手失败，必须给出装哪个包。
// 这个故障在 xvfb-run 时代同样存在，只是输出被丢进了 /dev/null。
func TestXvfbFailureHintFonts(t *testing.T) {
	hint := xvfbFailureHint("Fatal server error:\n(EE) could not open default font 'fixed'")
	if hint == "" {
		t.Fatal("xvfbFailureHint gave nothing for the missing-font failure")
	}
	for _, pkg := range []string{"xfonts-base", "xorg-x11-fonts-misc", "xorg-fonts-misc"} {
		if !strings.Contains(hint, pkg) {
			t.Errorf("font hint doesn't name %s: %q", pkg, hint)
		}
	}
	if xvfbFailureHint("nothing interesting here") != "" {
		t.Error("xvfbFailureHint invented a hint for an unknown failure")
	}
}

// TestXvfbDisplayInUse: 换个显示号重试只对「号被占了」有意义。
func TestXvfbDisplayInUse(t *testing.T) {
	if !xvfbDisplayInUse("Server is already active for display 100") {
		t.Error("xvfbDisplayInUse missed the already-active message")
	}
	if xvfbDisplayInUse("could not open default font 'fixed'") {
		t.Error("xvfbDisplayInUse treated a font failure as a display clash; retrying would just fail 10 more times")
	}
}

// TestXvfbInstallHintCoversDistros: 提示必须覆盖三大包管理器，且指向**提供 Xvfb 的
// 包**。只写 Debian 那一家正是这次要修的 bug 的源头。
func TestXvfbInstallHintCoversDistros(t *testing.T) {
	for _, want := range []string{"apt install xvfb", "dnf install xorg-x11-server-Xvfb", "pacman -S xorg-server-xvfb"} {
		if !strings.Contains(xvfbInstallHint, want) {
			t.Errorf("xvfbInstallHint is missing %q: %s", want, xvfbInstallHint)
		}
	}
	if strings.Contains(xvfbInstallHint, "xvfb-run") {
		t.Error("xvfbInstallHint still tells people to install xvfb-run — that script only exists on Debian")
	}
}

// TestXvfbBinaryRejectsBadConfig: linux.xvfb_bin 指错了要当场说清楚，
// 而不是悄悄退回 PATH 上的另一个 Xvfb。
func TestXvfbBinaryRejectsBadConfig(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "Xvfb")
	if _, err := xvfbBinary(Config{XvfbBin: missing}); err == nil {
		t.Error("xvfbBinary accepted a linux.xvfb_bin that doesn't exist")
	}

	notExec := filepath.Join(t.TempDir(), "Xvfb")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := xvfbBinary(Config{XvfbBin: notExec}); err == nil {
		t.Error("xvfbBinary accepted a non-executable linux.xvfb_bin")
	}
	if err := os.Chmod(notExec, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := xvfbBinary(Config{XvfbBin: notExec}); err != nil || got != notExec {
		t.Errorf("xvfbBinary(%q) = (%q, %v), want it used verbatim", notExec, got, err)
	}
}

// TestXvfbStateRoundTrip: state 文件是「重启后别再多起一个 Xvfb」的全部依据。
func TestXvfbStateRoundTrip(t *testing.T) {
	cfg := Config{BaseDir: t.TempDir()}
	writeXvfbState(cfg, &managedXvfb{display: ":100", pid: 4242})

	st, err := readXvfbState(cfg)
	if err != nil {
		t.Fatalf("readXvfbState: %v", err)
	}
	if st.Display != ":100" || st.PID != 4242 {
		t.Errorf("state = %+v, want display :100 / pid 4242", st)
	}
	if st.Started == "" {
		t.Error("state has no start time")
	}
}

// TestXvfbStateWithoutBaseDir: 没有 BaseDir（单测、早期启动）时不该乱写文件，
// 也不该 panic —— 认领功能降级即可。
func TestXvfbStateWithoutBaseDir(t *testing.T) {
	writeXvfbState(Config{}, &managedXvfb{display: ":100", pid: 1})
	if _, err := readXvfbState(Config{}); err == nil {
		t.Error("readXvfbState succeeded with no BaseDir")
	}
	if adoptXvfb(Config{}) != nil {
		t.Error("adoptXvfb invented a server with no state to read")
	}
}

// TestAdoptXvfbRejectsDeadPID: 认领要过三道关（进程活着、是 Xvfb、显示能握手）。
// pid 1 活着但不是 Xvfb，是最省事的一道反例。
func TestAdoptXvfbRejectsDeadPID(t *testing.T) {
	cfg := Config{BaseDir: t.TempDir()}
	writeXvfbState(cfg, &managedXvfb{display: ":99999", pid: 1})
	if adoptXvfb(cfg) != nil {
		t.Error("adoptXvfb adopted pid 1, which is not an Xvfb")
	}
}

// TestXvfbLogTailOnlyThisRun: 日志是追加写的，诊断只能看本次启动之后的内容 ——
// 否则会把上几次的错误当成这次的。
func TestXvfbLogTailOnlyThisRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xvfb.log")
	if err := os.WriteFile(path, []byte("上一次的错误\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openXvfbLog(path)
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

// TestOpenXvfbLogTruncatesOversized: 这个文件没有保留历史的价值，但不能无限长。
func TestOpenXvfbLogTruncatesOversized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xvfb.log")
	if err := os.WriteFile(path, make([]byte, xvfbLogMaxBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openXvfbLog(path)
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

// TestXvfbSysProcAttrFollowsProcess: Pdeathsig 是「Xvfb 跟着 asa-server 一起走」的
// 兜底层 —— asa-server 被 SIGKILL / panic / OOM 时，显式停止那段代码根本没机会跑，
// 只剩内核能收拾。用 SIGTERM 而不是 SIGKILL，好让 X 服务端自己清掉
// /tmp/.X11-unix/X<n> 与 /tmp/.X<n>-lock。
func TestXvfbSysProcAttrFollowsProcess(t *testing.T) {
	attr := xvfbSysProcAttr(nil)

	if attr.Pdeathsig != syscall.SIGTERM {
		t.Errorf("Pdeathsig = %v, want SIGTERM —— 没有它，asa-server 被 kill -9 之后 Xvfb 会留下来", attr.Pdeathsig)
	}
	if !attr.Setsid {
		t.Error("Setsid = false: Xvfb 不该挂在 asa-server 的控制终端上")
	}
}

// TestXvfbSpawnStartUsesDedicatedThread: 所有 Xvfb 都必须由那个 LockOSThread 且
// 永不返回的 goroutine 来 fork（否则 Pdeathsig 跟的线程可能先于进程退出，
// 把正在服务实例的 X 服务端莫名其妙杀掉）。这里跑一条最短的命令，验证这条通道
// 本身是通的 —— 派发、执行、把错误带回来。
func TestXvfbSpawnStartUsesDedicatedThread(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("没有 /bin/true，跳过")
	}
	cmd := exec.Command(bin)
	if err := xvfbSpawnStart(cmd); err != nil {
		t.Fatalf("xvfbSpawnStart: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("命令没有正常结束: %v", err)
	}

	// 派发第二条，确认那个 goroutine 是常驻的而不是一次性的。
	cmd2 := exec.Command(bin)
	if err := xvfbSpawnStart(cmd2); err != nil {
		t.Fatalf("第二次 xvfbSpawnStart 失败（spawn 循环退出了？）: %v", err)
	}
	_ = cmd2.Wait()
}

// TestStopManagedXvfbClearsCurrent: 显式停止之后，单例必须被清空且打上
// intentional 标记 —— 否则看门狗会在关停途中抢着补起一个马上又要被杀的 X 服务。
func TestStopManagedXvfbClearsCurrent(t *testing.T) {
	// adopted:true 让 stop() 走「不是我们的，不动它」那条短路，所以这里放本进程的
	// pid 也是安全的；同时它也钉住了「认领来的不归我们杀」这条不变量。
	x := &managedXvfb{display: ":99999", pid: os.Getpid(), adopted: true}
	xvfbCurrent.Store(x)
	t.Cleanup(func() { xvfbCurrent.Store(nil) })

	stopManagedXvfb()

	if currentManagedXvfb() != nil {
		t.Error("stopManagedXvfb 没有清空单例")
	}
	if !x.intentional.Load() {
		t.Error("stopManagedXvfb 没有打 intentional 标记，看门狗会来复活它")
	}
	stopManagedXvfb() // 幂等：退出路径可能被走两遍
}

// TestWatchXvfbIgnoresIntentionalStop: 看门狗对「我们自己停的」必须立刻收手。
// 否则 asa-server 关停时它会去起一个新的 Xvfb，然后被留在机器上。
func TestWatchXvfbIgnoresIntentionalStop(t *testing.T) {
	x := &managedXvfb{display: ":99999", pid: -1, exited: make(chan struct{})}
	x.intentional.Store(true)
	close(x.exited)

	done := make(chan struct{})
	go func() {
		watchXvfb(Config{}, "/nonexistent/Xvfb", x)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchXvfb 在主动停止后仍在尝试重启")
	}
}

// TestWatchXvfbIgnoresReplaced: 已经被换掉的那个（xvfbCurrent 指向别人）同样不该
// 由旧看门狗插手 —— 否则一次重启会留下两只看门狗互相打架。
func TestWatchXvfbIgnoresReplaced(t *testing.T) {
	old := &managedXvfb{display: ":99998", pid: -1, exited: make(chan struct{})}
	close(old.exited)
	xvfbCurrent.Store(&managedXvfb{display: ":99997", pid: -1})
	t.Cleanup(func() { xvfbCurrent.Store(nil) })

	done := make(chan struct{})
	go func() {
		watchXvfb(Config{}, "/nonexistent/Xvfb", old)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchXvfb 对一个已经被换掉的 Xvfb 仍在尝试重启")
	}
}
