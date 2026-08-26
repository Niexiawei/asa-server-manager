package plugindata

import (
	"os"
	"path/filepath"
	"strings"
)

// detectPluginsCaseMismatch 在 win64Dir 下用大小写不敏感的方式找一个名字是
// "ArkApi"（忽略大小写）但精确大小写又不是 "ArkApi" 的子目录。
//
// 纯逻辑、不认识平台：之所以只在 Linux 侧（casecheck_linux.go）被调用，是因为这个
// 问题只可能发生在大小写敏感的文件系统上（NTFS 不敏感，不可能出现这种落盘结果），
// 不是因为逻辑本身有平台依赖——所以拆出来单独测，两平台都能跑。
//
// 返回找到的实际目录名与 ok；win64Dir 不存在（ARK 还没装）时 ok 为 false。
func detectPluginsCaseMismatch(win64Dir string) (actualName string, ok bool) {
	entries, err := os.ReadDir(win64Dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "ArkApi" {
			continue
		}
		if strings.EqualFold(e.Name(), "ArkApi") {
			return e.Name(), true
		}
	}
	return "", false
}

// win64DirFromMirror 是 pluginsRelPath 去掉 "ArkApi/Plugins" 尾巴后的那一级——
// 单独抽出来只是为了 mirrorDir 和 win64Dir 的拼接逻辑只写一处。
func win64DirFromMirror(mirrorDir string) string {
	return filepath.Join(mirrorDir, "ShooterGame", "Binaries", "Win64")
}
