package mirror

import "testing"

// exception target 之内的条目绝不能被 removeMirrorEntry 碰到——那是穿过 junction
// 删源目录/实例目录的内容。这条边界（严格前缀、不含 target 自身）值得钉住。
func TestIsUnderExceptionTarget(t *testing.T) {
	targets := map[string]string{
		"ShooterGame/Saved/Config/WindowsServer": `D:\instances\a\Config`,
		win64SharedRelPath:                       `D:\server-files\` + win64SharedRelPath,
	}

	tests := []struct {
		relPath string
		want    bool
	}{
		// target 自身是镜像里的一条 junction，可以正常增删
		{win64SharedRelPath, false},
		{"ShooterGame/Saved/Config/WindowsServer", false},

		// target 之内 → 属于链接目标，不可删
		{win64SharedRelPath + "/Mods", true},
		{win64SharedRelPath + "/Mods/83374/1382641_8460768/x.pak", true},
		{win64SharedRelPath + "/ModsUserData", true},
		{"ShooterGame/Saved/Config/WindowsServer/Game.ini", true},

		// 祖先目录与同前缀的兄弟路径都不算
		{win64RelPath, false},
		{"ShooterGame/Saved/Config", false},
		{"ShooterGame/Saved/Config/WindowsServerOther/Game.ini", false},
		{win64RelPath + "/ArkApi/Cache/x.dll", false},
	}

	for _, tt := range tests {
		if got := isUnderExceptionTarget(tt.relPath, targets); got != tt.want {
			t.Errorf("isUnderExceptionTarget(%q) = %v, want %v", tt.relPath, got, tt.want)
		}
	}
}

// win64SharedRelPath 必须仍落在 Win64 判定之内：collectSourceEntries 的目录分支
// 先查 exception 再查 isUnderWin64，顺序反了就会退回「完整复制」的老行为。
func TestWin64SharedRelPathIsUnderWin64(t *testing.T) {
	if !isUnderWin64(win64SharedRelPath) {
		t.Fatalf("expected %q to be under %q", win64SharedRelPath, win64RelPath)
	}
	if !isWin64Ancestor("ShooterGame/Binaries") {
		t.Fatalf("expected ShooterGame/Binaries to be a Win64 ancestor")
	}
}
