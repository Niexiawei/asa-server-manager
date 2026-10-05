//go:build !windows

package atomicfile

func isChmodUnsupported(error) bool { return false }
