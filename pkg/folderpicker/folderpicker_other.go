//go:build !windows

package folderpicker

// ForegroundWindow 在非 Windows 平台上恒为 0。
func ForegroundWindow() uintptr { return 0 }

// Pick 在非 Windows 平台上恒返回 ErrUnsupported。
func Pick(title, initialDir string, owner uintptr) (string, bool, error) {
	return "", false, ErrUnsupported
}
