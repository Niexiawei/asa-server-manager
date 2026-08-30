package instance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeLog(t *testing.T, dir, name string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
	return path
}

func TestNewestArkApiLogPicksTheNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)

	writeLog(t, dir, "ArkApi_100_2026-08-30_10-00.log", base)
	want := writeLog(t, dir, "ArkApi_368_2026-08-30_12-28.log", base.Add(30*time.Minute))

	got, err := newestArkApiLog(dir, base.Add(-time.Minute))
	if err != nil {
		t.Fatalf("newestArkApiLog: %v", err)
	}
	if got != want {
		t.Errorf("newestArkApiLog = %q, want %q", got, want)
	}
}

// TestNewestArkApiLogIgnoresPreviousLaunches 是 notBefore 存在的理由：镜像是**增量**
// 同步的，上几次启动留下的日志还在原地。没有这个闸门，转抄协程会在本次日志出现之前
// 一直误认上一次那份，把陈旧内容当成实时输出贴给用户。
func TestNewestArkApiLogIgnoresPreviousLaunches(t *testing.T) {
	dir := t.TempDir()
	launchedAt := time.Now()

	writeLog(t, dir, "ArkApi_100_2026-08-29_09-00.log", launchedAt.Add(-24*time.Hour))

	if got, err := newestArkApiLog(dir, launchedAt); !errors.Is(err, ErrNoArkApiLog) {
		t.Errorf("newestArkApiLog = (%q, %v), want ErrNoArkApiLog for a stale log", got, err)
	}

	want := writeLog(t, dir, "ArkApi_368_2026-08-30_12-28.log", launchedAt.Add(time.Second))
	got, err := newestArkApiLog(dir, launchedAt)
	if err != nil || got != want {
		t.Errorf("newestArkApiLog = (%q, %v), want (%q, nil) once this launch's log lands", got, err, want)
	}
}

func TestNewestArkApiLogFiltersNames(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)

	// 同目录下的其它文件不能被当成 ArkApi 日志。
	writeLog(t, dir, "ShooterGame.log", base.Add(time.Minute))
	writeLog(t, dir, "ArkApi_368_2026-08-30_12-28.log.bak", base.Add(time.Minute))
	writeLog(t, dir, "notArkApi_1.log", base.Add(time.Minute))
	want := writeLog(t, dir, "ArkApi_1_x.log", base)

	got, err := newestArkApiLog(dir, base.Add(-time.Minute))
	if err != nil {
		t.Fatalf("newestArkApiLog: %v", err)
	}
	if got != want {
		t.Errorf("newestArkApiLog = %q, want %q — only ArkApi_*.log counts", got, want)
	}
}

func TestNewestArkApiLogMissingDir(t *testing.T) {
	got, err := newestArkApiLog(filepath.Join(t.TempDir(), "does-not-exist"), time.Time{})
	if !errors.Is(err, ErrNoArkApiLog) {
		t.Errorf("newestArkApiLog = (%q, %v), want ErrNoArkApiLog — 目录不存在是正常状态，不是故障", got, err)
	}
}

// TestNewestArkApiLogIgnoresDirs: 目录名恰好长得像日志时不能被选中（选中会让后续的
// os.Open 拿到一个读不出内容的句柄）。
func TestNewestArkApiLogIgnoresDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "ArkApi_9_dir.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := newestArkApiLog(dir, time.Time{}); !errors.Is(err, ErrNoArkApiLog) {
		t.Errorf("newestArkApiLog = (%q, %v), want ErrNoArkApiLog", got, err)
	}
}

func TestArkApiLogDir(t *testing.T) {
	got := arkApiLogDir(filepath.Join("base", "server-files-tmp-inst"))
	want := filepath.Join("base", "server-files-tmp-inst", "ShooterGame", "Binaries", "Win64", "logs")
	if got != want {
		t.Errorf("arkApiLogDir = %q, want %q", got, want)
	}
}
