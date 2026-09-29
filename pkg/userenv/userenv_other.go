//go:build !windows

package userenv

// Get 在非 Windows 平台上恒返回 ErrUnsupported。
func Get(scope Scope, name string) (string, bool, error) { return "", false, ErrUnsupported }

// SetUser 在非 Windows 平台上恒返回 ErrUnsupported。
func SetUser(name, value string) error { return ErrUnsupported }

// UnsetUser 在非 Windows 平台上恒返回 ErrUnsupported。
func UnsetUser(name string) error { return ErrUnsupported }

// Broadcast 在非 Windows 平台上是空操作。
func Broadcast() {}
