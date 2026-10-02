package fsutil

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxPortableNameLen 是 ValidPortableName 允许的最长名字（按字符计）。远低于
// 文件系统 255 的上限：名字会被拼进更深的路径里，Windows 默认还有 260 的整路径限制。
const maxPortableNameLen = 100

// windowsReserved 是 Windows 的保留设备名（比较前先转大写）。带不带扩展名都算：
// CON.txt 一样打开的是控制台。COM/LPT 后接上标数字 ¹²³ 也是保留的。
var windowsReserved = func() map[string]bool {
	m := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}
	for _, d := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³"} {
		m["COM"+d] = true
		m["LPT"+d] = true
	}
	return m
}()

// ValidPortableName 校验一个**单级**文件或目录名在 Windows 与 Linux 上都能原样建出来、
// 原样找回来。Linux 允许而 Windows 不允许的名字（保留设备名、尾随点或空格、<>:"|?*）
// 在 Linux 上建得出来，数据目录搬到 Windows 上就打不开了；在 Windows 上则是建的时候
// 报一个让人看不懂的错，或者被悄悄规范化成另一个名字之后再也找不到。
func ValidPortableName(name string) error {
	if name == "" {
		return fmt.Errorf("名字不能为空")
	}
	if n := utf8.RuneCountInString(name); n > maxPortableNameLen {
		return fmt.Errorf("名字 %q 太长（%d 个字符，上限 %d）", name, n, maxPortableNameLen)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("名字 %q 含控制字符", name)
		}
		if strings.ContainsRune(`<>:"/\|?*`, r) {
			return fmt.Errorf("名字 %q 含 Windows 不允许的字符 %q", name, r)
		}
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return fmt.Errorf("名字 %q 不能以点或空格结尾", name)
	}
	if strings.HasPrefix(name, " ") {
		return fmt.Errorf("名字 %q 不能以空格开头", name)
	}
	stem, _, _ := strings.Cut(name, ".")
	if windowsReserved[strings.ToUpper(strings.TrimRight(stem, " "))] {
		return fmt.Errorf("名字 %q 是 Windows 的保留设备名", name)
	}
	return nil
}
