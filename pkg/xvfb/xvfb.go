// Package xvfb manages a single self-managed Xvfb (X virtual framebuffer)
// server: the process's own way of handing a Wine process a display on
// Linux, instead of relying on the Debian-only xvfb-run wrapper script (see
// docs/XVFB_CROSS_DISTRO_DISPLAY_PLAN.md). It does not know about ASA,
// instances, or ArkApi — the caller supplies the runtime-user hooks
// (HomeDir/ChildIDs/Credential) needed to spawn Xvfb under the same identity
// as the Wine process it serves.
//
// # Lifecycle: follows the host process
//
// Process-wide singleton (multiple callers share one display), health-
// checked before use, resurrected by a watchdog if it dies, and torn down
// when the host process exits — see Manager's doc comment for the full
// three-layer guarantee (explicit Stop, Pdeathsig, orphan adoption).
package xvfb

import (
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// SocketDir is the **only** place an X server publishes its local socket —
// the path is hardcoded into xtrans, unaffected by any environment variable.
// Every writability/existence check in this package (and in a caller's own
// "is there already an X server running" scan) starts here.
const SocketDir = "/tmp/.X11-unix"

// ProbeTimeout bounds a single "can I connect" handshake. A local unix
// socket handshake is microsecond-scale; a full second just guards against a
// half-dead peer on the other end.
const ProbeTimeout = time.Second

// InstallHint is the per-distro install line for Xvfb — the X.Org virtual
// framebuffer server, not Debian's xvfb-run wrapper script, which this
// package deliberately does not use (Fedora/RHEL/Arch don't ship it; see the
// package doc). Shared verbatim across preflight, a display-unavailable
// explanation, and a launch-time error, so it lives in one place.
const InstallHint = "安装 Xvfb（Debian/Ubuntu: sudo apt install xvfb  |  " +
	"Fedora/RHEL: sudo dnf install xorg-x11-server-Xvfb  |  " +
	"Arch: sudo pacman -S xorg-server-xvfb  |  " +
	"openSUSE: sudo zypper install xorg-x11-server-extra）"

// FontHint covers a failure only a self-managed Xvfb can even see: a minimal
// install with no fonts makes the X server exit outright ("could not open
// default font 'fixed'"). Under xvfb-run this happened too, it just went to
// /dev/null along with everything else the server said.
const FontHint = "Xvfb 缺少基础字体，装上即可（Debian/Ubuntu: sudo apt install xfonts-base  |  " +
	"Fedora/RHEL: sudo dnf install xorg-x11-fonts-misc  |  Arch: sudo pacman -S xorg-fonts-misc）"

// IsLocalDisplay distinguishes a local display like ":0" from a remote
// "host:0" or path form. Only the local form can be judged by socket +
// handshake; a caller has to try a remote one itself.
func IsLocalDisplay(display string) bool {
	return strings.HasPrefix(display, ":")
}

// SocketPath converts a local DISPLAY like ":0" / ":0.0" to its socket file
// path, returning "" when the file doesn't exist or display isn't local
// form.
func SocketPath(display string) string {
	if !IsLocalDisplay(display) {
		return ""
	}
	num := strings.TrimPrefix(display, ":")
	if i := strings.Index(num, "."); i >= 0 {
		num = num[:i] // 去掉 screen 号，socket 只按 display 号命名
	}
	if num == "" {
		return ""
	}
	if _, err := strconv.Atoi(num); err != nil {
		return ""
	}
	path := SocketDir + "/X" + num
	if !pathExists(path) {
		return ""
	}
	return path
}

// DisplayUsable really connects to the X server and runs the **unauthenticated**
// connection handshake once.
//
// Why go this far instead of stopping at "does the socket file exist":
// callers of this package deliberately don't pass an X auth cookie (see
// pkg/xvfb's caller docs), so a display that requires one is unusable to
// them even though its socket file is right there. Taking file existence as
// the test would pick a display we can't actually use, reproducing the
// "loader exits with zero output" mystery. A single handshake turns the
// guess into a fact, at a cost of microseconds.
//
// This is also exactly what Manager's own readiness check uses — "did it
// come up" and "can it be used" must be the same judgement.
//
// Protocol (X11 §connection setup): the client sends a 12-byte setup
// request, and the server's first response byte is 0=Failed / 1=Success /
// 2=Authenticate. Only that one byte is inspected — no further server info
// is parsed, and no X client library is needed.
func DisplayUsable(display string) bool {
	if !IsLocalDisplay(display) {
		return true // 远程显示无法本地判断，放行让调用方去试
	}
	path := SocketPath(display)
	if path == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", path, ProbeTimeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(ProbeTimeout))

	// 'l' = 小端；protocol-major=11, minor=0；auth 名与 auth 数据长度都是 0。
	req := []byte{'l', 0, 11, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if _, err := conn.Write(req); err != nil {
		return false
	}
	resp := make([]byte, 8)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return false
	}
	return resp[0] == 1
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
