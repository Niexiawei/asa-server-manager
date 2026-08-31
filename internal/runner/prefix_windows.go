//go:build windows

package runner

import (
	"context"
	"io"
)

// There is no Wine prefix on Windows: the ARK exe runs natively. Every entry
// point here is inert, which is what keeps prefix_mode a Linux-only concept
// that the shared start path can call into unconditionally.

func prefixKeyFor(string) string { return "" }

func ensurePrefix(context.Context, string, io.Writer) error { return nil }

func removeInstancePrefix(string) error { return nil }

func prefixStatus() []PrefixInfo { return nil }
