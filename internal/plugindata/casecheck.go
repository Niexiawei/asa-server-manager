package plugindata

import (
	"os"
	"path/filepath"
	"strings"
)

// actualChildName 在 dir 下找名字等于 want 的子目录并返回它在盘上的实际名字：
// 精确匹配优先，其次不区分大小写匹配；都找不到时返回 want。
//
// 用在「按实际大小写拼路径」的地方：Walk 出来的相对路径反映的是盘上的大小写，
// 拿常量去做精确匹配，遇到手工解压出来的 arkapi/ 就永远对不上。
func actualChildName(dir, want string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return want
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() == want {
			return want
		}
	}
	for _, e := range entries {
		if e.IsDir() && strings.EqualFold(e.Name(), want) {
			return e.Name()
		}
	}
	return want
}

// win64DirFromMirror 是 pluginsRelPath 去掉 "ArkApi/Plugins" 尾巴后的那一级。
func win64DirFromMirror(mirrorDir string) string {
	return filepath.Join(mirrorDir, "ShooterGame", "Binaries", "Win64")
}
