package process

import (
	"os"
	"path/filepath"
	"testing"

	cfgpkg "asa-server/internal/config"
)

// 命令行形态取自真机快照（pkg/procmatch/procmatch_test.go 的同一批），外加 Windows
// 上 exec 给带空格参数加的引号形态。
func TestCmdlineHasSaveDir(t *testing.T) {
	const (
		linuxGame = `Z:\opt\asa\…\Win64\ArkAscendedServer.exe ` +
			"TheIsland_WP?listen?AltSaveDirectoryName=srv -Port=41778"
		linuxGame2 = `Z:\opt\asa\…\Win64\ArkAscendedServer.exe ` +
			"TheIsland_WP?listen?AltSaveDirectoryName=srv2 -Port=41779"
		windowsQuoted = `"D:\asa\Win64\ArkAscendedServer.exe" ` +
			`"TheIsland_WP?listen?SessionName=\"My Server\"?AltSaveDirectoryName=srv" -Port=7777`
		atEnd       = "ArkAscendedServer.exe TheIsland_WP?listen?AltSaveDirectoryName=srv"
		moreOptions = "ArkAscendedServer.exe TheIsland_WP?AltSaveDirectoryName=srv?Foo=1 -log"
		nulSplit    = "ArkAscendedServer.exe\x00TheIsland_WP?AltSaveDirectoryName=srv\x00-log"
	)
	cases := []struct {
		name, cmdline, saveDir string
		want                   bool
	}{
		{"own instance", linuxGame, "srv", true},
		{"prefix of another instance must not match", linuxGame2, "srv", false},
		{"the longer name still matches itself", linuxGame2, "srv2", true},
		{"windows quoted arg", windowsQuoted, "srv", true},
		{"end of command line", atEnd, "srv", true},
		{"followed by another URL option", moreOptions, "srv", true},
		{"raw NUL separators", nulSplit, "srv", true},
		{"empty save dir never matches", linuxGame, "", false},
		{"absent", linuxGame, "other", false},
		// 第一处命中没有边界，第二处有：必须继续往后找。
		{"second occurrence has the boundary", linuxGame2 + " " + linuxGame, "srv", true},
	}
	for _, c := range cases {
		if got := CmdlineHasSaveDir(c.cmdline, c.saveDir); got != c.want {
			t.Errorf("%s: CmdlineHasSaveDir(%q) = %v, want %v", c.name, c.saveDir, got, c.want)
		}
	}
}

func TestClearInstancePIDs(t *testing.T) {
	withTempInstancesDir(t)
	const name = "inst"
	if err := SaveInstancePID(name, 111); err != nil {
		t.Fatal(err)
	}
	if err := SaveLauncherPID(name, 222); err != nil {
		t.Fatal(err)
	}
	// asa_api_pid 不存在也不算错误。
	if err := ClearInstancePIDs(name); err != nil {
		t.Fatalf("ClearInstancePIDs: %v", err)
	}
	for _, f := range []string{"pid", "launcher_pid", "asa_api_pid"} {
		if _, err := os.Stat(filepath.Join(cfgpkg.InstancesDir, name, f)); !os.IsNotExist(err) {
			t.Errorf("%s still exists after ClearInstancePIDs (err=%v)", f, err)
		}
	}
	if _, err := GetInstancePID(name); err == nil {
		t.Error("GetInstancePID succeeded after ClearInstancePIDs")
	}
}

// A saved PID that no longer exists (the normal state after a crash) must
// never be handed back to a caller about to kill it.
func TestVerifiedPID_RejectsDeadPID(t *testing.T) {
	withTempInstancesDir(t)
	const name = "inst"
	// PID 文件指向一个几乎不可能存在的号码。
	if err := SaveInstancePID(name, 0x3FFFFFF0); err != nil {
		t.Fatal(err)
	}
	if pid, ok := VerifiedPID(name, PIDGame); ok {
		t.Fatalf("VerifiedPID returned %d for a dead PID", pid)
	}
}

// The ownership judgement itself: a dead PID, and a live process that isn't
// one of ours (this test binary), both belong to nobody.
func TestPIDBelongsTo_RejectsDeadAndUnrelated(t *testing.T) {
	if PIDBelongsTo(0x3FFFFFF0, "srv") {
		t.Error("dead PID reported as belonging to an instance")
	}
	if PIDBelongsTo(os.Getpid(), "srv") {
		t.Error("the test process itself reported as an instance's game process")
	}
	if PIDBelongsTo(1, "srv") {
		t.Error("PID 1 must never be reported as an instance process")
	}
}
