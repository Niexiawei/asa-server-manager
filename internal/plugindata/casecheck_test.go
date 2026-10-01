package plugindata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cfgpkg "asa-server/internal/config"
)

// 镜像里的插件目录按盘上实际大小写解析：手工解压出来的 arkapi/plugins 也能找到。
func TestMirrorPluginsDirFollowsActualCase(t *testing.T) {
	mirrorDir := t.TempDir()
	mustMkdir(t, filepath.Join(win64DirFromMirror(mirrorDir), "arkapi", "plugins", "Permissions"))

	got := MirrorPluginsDir(mirrorDir)
	if want := filepath.Join(win64DirFromMirror(mirrorDir), "arkapi", "plugins"); got != want {
		t.Fatalf("MirrorPluginsDir = %q, want %q", got, want)
	}
	if plugins := listMirrorPlugins(mirrorDir); len(plugins) != 1 || plugins[0] != "Permissions" {
		t.Fatalf("listMirrorPlugins = %v", plugins)
	}
}

func TestMirrorPluginsDirDefaultsToCanonicalCase(t *testing.T) {
	mirrorDir := t.TempDir()
	want := filepath.Join(mirrorDir, filepath.FromSlash(pluginsRelPath))
	if got := MirrorPluginsDir(mirrorDir); got != want {
		t.Fatalf("MirrorPluginsDir = %q, want %q", got, want)
	}
}

func TestIsProtectedRelPathIgnoresCase(t *testing.T) {
	rel := strings.Replace(pluginsRelPath, "ArkApi/Plugins", "arkapi/plugins", 1) + "/Permissions/config.json"
	if !IsProtectedRelPath(t.TempDir(), rel) {
		t.Fatalf("%s 应当受保护", rel)
	}
	if IsProtectedRelPath(t.TempDir(), pluginsRelPath+"/Permissions") {
		t.Fatal("插件目录本身不算受保护文件")
	}
}

// 大小写不同的布局下，迁移同样要把镜像里崩溃遗留的新数据抢救回来
// （docs/PLAN_IMPLEMENTATION_AUDIT_2026-09-29.md §7.2 P1-21）。
func TestMigrateRescuesMirrorDataWithLowercasePluginsDir(t *testing.T) {
	root := setupLayoutEnv(t)
	const inst = "alpha"

	srcPlugins := filepath.Join(filepath.Join(cfgpkg.ServerFilesDir, filepath.FromSlash(win64RelPath)), "arkapi", "plugins")
	writeFile(t, filepath.Join(srcPlugins, "Permissions", "Permissions.dll"), "MZ v1")

	legacyDB := filepath.Join(legacyPluginsDir(inst), "Permissions", "ArkDB.db")
	writeSQLite(t, legacyDB, "old-instance-copy")
	touch(t, legacyDB, time.Now().Add(-2*time.Hour))

	mirrorDir := filepath.Join(root, "server-files-tmp-"+inst)
	mirrorPlugin := filepath.Join(win64DirFromMirror(mirrorDir), "arkapi", "plugins", "Permissions")
	writeSQLite(t, filepath.Join(mirrorPlugin, "ArkDB.db"), "crashed-main")

	mustMigrate(t, inst, mirrorDir)

	dst := filepath.Join(InstancePluginsDir(inst), "Permissions")
	if got := readFile(t, filepath.Join(dst, "ArkDB.db")); !strings.HasSuffix(got, "crashed-main") {
		t.Errorf("镜像里的崩溃现场没有被抢救，实际 %q", got)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
