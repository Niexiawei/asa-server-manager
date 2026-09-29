//go:build !linux

package userenv

// SetProfileScript 只在 Linux 上有实现（/etc/profile.d），其余平台返回 ErrUnsupported。
func SetProfileScript(file, name, value string) (string, error) { return "", ErrUnsupported }
