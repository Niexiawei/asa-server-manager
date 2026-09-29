package appconfig

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// 模板语言。中文是本项目的主力语言；英文版是纯 ASCII，给终端 / SSH 客户端不是
// UTF-8 的用户兜底——那种终端里任何非 ASCII 字符都是乱码，文件本身写成什么编码
// 都救不了（BOM、vim modeline 管不到终端怎么解码），见
// docs/SETUP_FLOW_OPTIMIZATION_PLAN.md Part 2 §P2-1.2。
const (
	LangZH = "zh"
	LangEN = "en"
)

// utf8BOM 是 Windows 上写出的 config.yaml 开头的字节序标记。
const utf8BOM = "\xef\xbb\xbf"

// quoteYAML 把字符串渲染成 YAML 双引号标量。strconv.Quote 的转义规则（反斜杠、
// 双引号、控制字符）在路径会用到的字符集上与 YAML 双引号标量一致；非 ASCII 的
// 可打印字符原样保留，中文路径不会变成 \uXXXX。
func quoteYAML(s string) string { return strconv.Quote(s) }

// NormalizeLang 校验并规范化语言参数：空串原样返回（= 调用方自己决定默认值），
// 其余只接受 zh / en（大小写不敏感）。
func NormalizeLang(lang string) (string, error) {
	switch l := strings.ToLower(strings.TrimSpace(lang)); l {
	case "", LangZH, LangEN:
		return l, nil
	default:
		return "", fmt.Errorf("不支持的模板语言 %q（可选 zh / en）", lang)
	}
}

// DefaultTemplateLang 是没人可问时（非交互 / api 与服务模式自动生成）的模板语言。
//
// Windows 恒为中文。Linux 看 locale：LC_ALL > LC_CTYPE > LANG 第一个非空值带
// UTF-8 → 中文，否则（未设置、C、POSIX 等）→ 英文。这只是尽力而为：服务器
// locale 是 UTF-8、SSH 客户端却按 GBK 解码的情况（实际遇到过的那种）在服务器
// 这一侧根本探测不到，交互模式下应该直接问用户。
func DefaultTemplateLang() string {
	return templateLangFor(runtime.GOOS, os.Getenv)
}

func templateLangFor(goos string, getenv func(string) string) string {
	if goos != "linux" {
		return LangZH
	}
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := getenv(key)
		if v == "" {
			continue
		}
		l := strings.ToLower(v)
		if strings.Contains(l, "utf-8") || strings.Contains(l, "utf8") {
			return LangZH
		}
		return LangEN
	}
	return LangEN
}

// renderTemplate 渲染当前平台的 config.yaml。lang 为空时用 DefaultTemplateLang()。
func renderTemplate(lang, baseDir string) []byte {
	return renderTemplateFor(runtime.GOOS, lang, baseDir)
}

// renderTemplateFor 是可以在任一平台上测试另一平台产物的纯函数版本。
//
// 刻意手写模板而不是用 viper.SafeWriteConfigAs 生成：这份文件的目标读者是人，
// 注释里的那些警告（尤其 lan_bypass）比字段本身更重要。
//
// 字节层按平台区分：Windows 输出 UTF-8 BOM + CRLF——Windows Server 2016/2019
// 自带记事本对无 BOM 的文件会猜成 ANSI(CP936)，2016 的记事本还不认 LF 换行；
// Linux 输出无 BOM + LF，BOM 在终端里没有任何作用，还会干扰 grep '^basedir'
// 这类脚本。两种形态 viper 都能正确解析（TestLoadAcceptsBOMAndCRLF）。
func renderTemplateFor(goos, lang, baseDir string) []byte {
	if lang == "" {
		lang = templateLangFor(goos, os.Getenv)
	}
	tpl, trust := templateZH, trustLocalCABlockZH(goos)
	if lang == LangEN {
		tpl, trust = templateEN, trustLocalCABlockEN(goos)
	}
	content := fmt.Sprintf(tpl, quoteYAML(baseDir), trust)
	if goos == "windows" {
		return []byte(utf8BOM + strings.ReplaceAll(content, "\n", "\r\n"))
	}
	return []byte(content)
}
