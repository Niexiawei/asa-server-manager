//go:build windows

package mirror

import (
	"os"
	"path/filepath"
	"testing"

	cfgpkg "asa-server/internal/config"

	"golang.org/x/sys/windows"
)

// hasReparsePointAttr 直接读文件属性判断是否是重解析点，
// 不经过 isJunctionOrSymlink —— 用于给被测逻辑做独立的前置断言。
func hasReparsePointAttr(t *testing.T, path string) bool {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%q): %v", path, err)
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return false
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// 同步必须把镜像里的 junction 认成链接，并且全程不碰源目录。
//
// 两件事各自的失败后果不同，这里一起守住：
//
//  1. 识别失败（例如把 isJunctionOrSymlink 改回只查 os.ModeSymlink —— Go 1.23 起
//     真 junction 报的是 ModeIrregular）：collectMirrorEntries 会把 junction 归成
//     EntryTypeFile，而源侧意图是 EntryTypeSymlink，于是**每一轮同步都判定类型不匹配、
//     把所有 junction 删掉重建**；reconcileEntry 还可能对着一个目录算 MD5 而报错，
//     进而触发整个镜像重建。是性能与稳定性问题。
//  2. 穿透删除：源目录里的文件在任何情况下都不能因为同步而消失或改变。
//
// 需要说清的是：os.Lstat 对 junction 返回 IsDir()==false，filepath.Walk 因此不会
// 递归进 junction，migrateExceptionJunctions 的 !fi.IsDir() 也拦得住。所以即便识别
// 失效，实测也不会删到源数据——第 2 组断言是兜底，第 1 组才是识别失效的真实症状。
func TestSyncDoesNotDeleteThroughJunctions(t *testing.T) {
	root := t.TempDir()

	origBase, origServerFiles, origInstances := cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir
	t.Cleanup(func() {
		cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir = origBase, origServerFiles, origInstances
	})
	cfgpkg.BaseDir = root
	cfgpkg.ServerFilesDir = filepath.Join(root, "server-files")
	cfgpkg.InstancesDir = filepath.Join(root, "instances")

	// 造一棵有代表性的源目录树：
	//   Engine/            —— 普通顶层目录，会被整体 junction，子内容不进镜像
	//   ShooterGame/Binaries/Win64/  —— 完整复制
	//   ShooterGame/Saved/Config/WindowsServer/ —— exception，junction 到实例目录
	//   steamclient.dll    —— 根目录散落文件，复制
	srcFiles := map[string]string{
		"Engine/Content/heavy.bin":                         "engine-payload",
		"Engine/Binaries/ThirdParty/x.dll":                 "thirdparty",
		"ShooterGame/Binaries/Win64/ArkAscendedServer.exe": "fake-exe",
		"ShooterGame/Binaries/Win64/steamclient64.dll":     "fake-dll",
		"ShooterGame/Content/Mods/1234/mod.pak":            "modfile",
		"steamclient.dll":                                  "root-dll",
	}
	for rel, content := range srcFiles {
		p := filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("建源目录 %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatalf("写源文件 %s: %v", rel, err)
		}
	}
	// exception 与共享 mods 目录的源侧必须先存在
	for _, rel := range []string{"ShooterGame/Saved/Config/WindowsServer", win64SharedRelPath} {
		if err := os.MkdirAll(filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(rel)), 0755); err != nil {
			t.Fatalf("建源目录 %s: %v", rel, err)
		}
	}

	// 实例侧目录（exception junction 的落点）
	instName := "testinst"
	instCfg := filepath.Join(cfgpkg.InstancesDir, instName, "Config")
	instLogs := filepath.Join(cfgpkg.InstancesDir, instName, "Logs")
	instSave := filepath.Join(cfgpkg.InstancesDir, instName, "Save")
	for _, d := range []string{instCfg, instLogs, instSave} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("建实例目录 %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(instCfg, "GameUserSettings.ini"), []byte("[x]"), 0644); err != nil {
		t.Fatalf("写实例配置: %v", err)
	}

	exceptionTargets := map[string]string{
		"ShooterGame/Saved/Config/WindowsServer": instCfg,
		"ShooterGame/Saved/Logs":                 instLogs,
		"ShooterGame/Saved/" + instName:          instSave,
		win64SharedRelPath:                       filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath)),
	}

	mirrorDir := InstanceMirrorDir(instName)
	if _, err := createInstanceMirror(instName, mirrorDir, exceptionTargets); err != nil {
		t.Fatalf("创建镜像失败: %v", err)
	}

	// 前置断言刻意不用 isJunctionOrSymlink —— 那正是被测对象，用它判断会变成循环论证。
	// 直接查 Windows 的 FILE_ATTRIBUTE_REPARSE_POINT，这是地面真相。
	engineLink := filepath.Join(mirrorDir, "Engine")
	if !hasReparsePointAttr(t, engineLink) {
		t.Fatalf("Engine 应被建成 junction，用例前提不成立")
	}

	// 断言组 1：junction 必须被归类成 EntryTypeSymlink。
	// 归成别的类型就会与源侧的意图类型对不上，导致每轮同步都把它删掉重建。
	mirrorEntries, err := collectMirrorEntries(mirrorDir)
	if err != nil {
		t.Fatalf("collectMirrorEntries: %v", err)
	}
	engineType := -1
	for _, e := range mirrorEntries {
		if e.RelPath == "Engine" {
			engineType = e.EntryType
			break
		}
	}
	if engineType != EntryTypeSymlink {
		t.Errorf("镜像里的 Engine junction 被归类为 %d，期望 EntryTypeSymlink(%d)；"+
			"归类错误会让每轮同步都把 junction 删掉重建", engineType, EntryTypeSymlink)
	}

	// 连跑两轮增量同步（第二轮覆盖"镜像已存在"的稳定态）
	for i := 0; i < 2; i++ {
		if err := syncMirrorEntries(mirrorDir, exceptionTargets); err != nil {
			t.Fatalf("第 %d 轮同步失败: %v", i+1, err)
		}
	}

	// 断言组 2：源目录一个文件都不能少、内容不能变
	for rel, want := range srcFiles {
		p := filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(rel))
		got, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("源文件被删除或损坏 %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("源文件内容被改动 %s: 得到 %q，期望 %q", rel, got, want)
		}
	}

	// 实例侧的 exception 目标同样不能被穿透删除
	if _, err := os.Stat(filepath.Join(instCfg, "GameUserSettings.ini")); err != nil {
		t.Errorf("实例配置被穿过 junction 删掉了: %v", err)
	}
}

// 文件不再建符号链接，一律复制：镜像里的文件必须是真实副本。
// 这样两种权限下行为一致，也不会再出现"有时链接有时复制"的类型抖动。
func TestMirrorFilesAreRealCopies(t *testing.T) {
	root := t.TempDir()

	origBase, origServerFiles, origInstances := cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir
	t.Cleanup(func() {
		cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir = origBase, origServerFiles, origInstances
	})
	cfgpkg.BaseDir = root
	cfgpkg.ServerFilesDir = filepath.Join(root, "server-files")
	cfgpkg.InstancesDir = filepath.Join(root, "instances")

	// 根目录散落文件走的正是原先 createFileSymlink 那条分支
	rootFile := filepath.Join(cfgpkg.ServerFilesDir, "steamclient.dll")
	if err := os.MkdirAll(cfgpkg.ServerFilesDir, 0755); err != nil {
		t.Fatalf("建源目录: %v", err)
	}
	if err := os.WriteFile(rootFile, []byte("dll-bytes"), 0644); err != nil {
		t.Fatalf("写源文件: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath)), 0755); err != nil {
		t.Fatalf("建共享 mods 目录: %v", err)
	}

	instName := "copyinst"
	instCfg := filepath.Join(cfgpkg.InstancesDir, instName, "Config")
	if err := os.MkdirAll(instCfg, 0755); err != nil {
		t.Fatalf("建实例目录: %v", err)
	}
	exceptionTargets := map[string]string{
		"ShooterGame/Saved/Config/WindowsServer": instCfg,
		win64SharedRelPath:                       filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64SharedRelPath)),
	}

	mirrorDir := InstanceMirrorDir(instName)
	if _, err := createInstanceMirror(instName, mirrorDir, exceptionTargets); err != nil {
		t.Fatalf("创建镜像失败: %v", err)
	}

	mirrored := filepath.Join(mirrorDir, "steamclient.dll")
	if isJunctionOrSymlink(mirrored) {
		t.Error("镜像里的文件应是真实副本，不应是符号链接")
	}
	b, err := os.ReadFile(mirrored)
	if err != nil {
		t.Fatalf("读镜像文件: %v", err)
	}
	if string(b) != "dll-bytes" {
		t.Errorf("镜像文件内容 = %q，期望 %q", b, "dll-bytes")
	}

	// 改镜像副本不得影响源文件（这正是不用硬链接的原因）
	if err := os.WriteFile(mirrored, []byte("tampered"), 0644); err != nil {
		t.Fatalf("改写镜像文件: %v", err)
	}
	src, err := os.ReadFile(rootFile)
	if err != nil {
		t.Fatalf("读源文件: %v", err)
	}
	if string(src) != "dll-bytes" {
		t.Errorf("改镜像副本污染了源文件: %q", src)
	}
}
