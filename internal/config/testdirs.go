package config

import "testing"

// UseTempDirsForTest 仅供测试使用：把 BaseDir 及全部派生目录变量（InstancesDir、
// ServerFilesDir、SteamCmdDir、BackupsDir）指向一个新的临时目录，用例结束时通过
// t.Cleanup 原样还原，返回这个临时 BaseDir。只设变量、不建目录（同 SetDirectories）。
//
// 测试各自「保存 → 替换 → 还原」其中几个变量时，没换的那几个仍是空串——读它们的
// 代码就会去读当前工作目录（即包目录）下的 instances/ 之类，结果取决于开发机上那里
// 恰好有什么；换了的那几个也各写一遍还原，漏一个就串到后面的用例
// （docs/TEST_ENV_COUPLING_PLAN.md T8）。要换就五个一起换。
func UseTempDirsForTest(t testing.TB) string {
	t.Helper()
	base, instances, serverFiles, steamCmd, backups := BaseDir, InstancesDir, ServerFilesDir, SteamCmdDir, BackupsDir
	t.Cleanup(func() {
		BaseDir, InstancesDir, ServerFilesDir, SteamCmdDir, BackupsDir = base, instances, serverFiles, steamCmd, backups
	})
	root := t.TempDir()
	SetDirectories(root)
	return root
}
