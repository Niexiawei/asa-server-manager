package main

import (
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	for in, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "0d": 0, "24h": 24 * time.Hour, "30m": 30 * time.Minute} {
		if got, err := parseSince(in); err != nil || got != want {
			t.Errorf("parseSince(%q) = %v, %v；期望 %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "7", "xd", "-1d", "-5h", "1w"} {
		if _, err := parseSince(bad); err == nil {
			t.Errorf("parseSince(%q) 应报错", bad)
		}
	}
}

func TestFmtBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB", 2 << 40: "2.0 TiB"} {
		if got := fmtBytes(n); got != want {
			t.Errorf("fmtBytes(%d) = %q，期望 %q", n, got, want)
		}
	}
}
