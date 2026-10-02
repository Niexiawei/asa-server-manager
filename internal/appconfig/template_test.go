package appconfig

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/spf13/viper"
)

var templateGOOSes = []string{"windows", "linux"}

// parseTemplate 只解析模板本身（不叠默认值、不叠环境变量），返回文件里写了什么。
func parseTemplate(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(raw)); err != nil {
		t.Fatalf("模板无法解析: %v", err)
	}
	return v.AllSettings()
}

// 中英两份模板必须逐个 key、逐个值完全一致——英文版只是注释换了语言。
// 漏一个 key 在 Load 里不会报错（默认值会补上），只能靠这里兜住。
func TestTemplatesAreEquivalent(t *testing.T) {
	for _, goos := range templateGOOSes {
		for _, baseDir := range []string{"", `C:\ASA 数据\data`, "/srv/asa 数据"} {
			zh := parseTemplate(t, renderTemplateFor(goos, LangZH, baseDir))
			en := parseTemplate(t, renderTemplateFor(goos, LangEN, baseDir))
			if !reflect.DeepEqual(zh, en) {
				t.Errorf("[%s basedir=%q] 中英模板解析结果不一致\nzh: %#v\nen: %#v", goos, baseDir, zh, en)
			}
			if got := zh["basedir"]; got != baseDir {
				t.Errorf("[%s] basedir 应原样写入 %q，解析得到 %#v", goos, baseDir, got)
			}
		}
	}
}

// 模板里的值必须等于内置默认值：用户拿到的「默认配置」和不写配置文件时的行为
// 不能是两回事。对照组是「空的 config.yaml」——两边都经过 viper 解码，slice 的
// nil / 空之类的形态差异不会造成误报。
func TestTemplateMatchesBuiltinDefaults(t *testing.T) {
	for _, lang := range []string{LangZH, LangEN} {
		dir := t.TempDir()
		writeConfig(t, dir, string(renderTemplate(lang, "")))
		if _, err := loadFrom(t, dir); err != nil {
			t.Fatalf("[%s] 加载模板: %v", lang, err)
		}
		fromTemplate := *Get()

		emptyDir := t.TempDir()
		writeConfig(t, emptyDir, "")
		if _, err := loadFrom(t, emptyDir); err != nil {
			t.Fatalf("加载空配置: %v", err)
		}
		fromDefaults := *Get()

		if !reflect.DeepEqual(fromTemplate, fromDefaults) {
			t.Errorf("[%s] 模板里的值与内置默认值不一致\n模板:   %+v\n默认值: %+v", lang, fromTemplate, fromDefaults)
		}
	}
}

// 英文模板存在的全部意义就是在非 UTF-8 终端里不乱码，一个全角标点混进去就白做了。
func TestEnglishTemplateIsASCII(t *testing.T) {
	for _, goos := range templateGOOSes {
		raw := bytes.TrimPrefix(renderTemplateFor(goos, LangEN, ""), []byte(utf8BOM))
		for i, b := range raw {
			if b > 0x7F {
				line := bytes.Count(raw[:i], []byte("\n")) + 1
				t.Fatalf("[%s] 英文模板第 %d 行含非 ASCII 字节 0x%02x", goos, line, b)
			}
		}
	}
}

func TestTemplateByteLayoutPerPlatform(t *testing.T) {
	for _, lang := range []string{LangZH, LangEN} {
		win := renderTemplateFor("windows", lang, "")
		if !bytes.HasPrefix(win, []byte(utf8BOM)) {
			t.Errorf("[%s] Windows 模板应以 UTF-8 BOM 开头", lang)
		}
		if n, crlf := bytes.Count(win, []byte("\n")), bytes.Count(win, []byte("\r\n")); n != crlf {
			t.Errorf("[%s] Windows 模板应全部是 CRLF 换行：%d 个换行里只有 %d 个 CRLF", lang, n, crlf)
		}

		lin := renderTemplateFor("linux", lang, "")
		if bytes.HasPrefix(lin, []byte(utf8BOM)) {
			t.Errorf("[%s] Linux 模板不应带 BOM", lang)
		}
		if bytes.Contains(lin, []byte("\r")) {
			t.Errorf("[%s] Linux 模板不应含 CR", lang)
		}

		// 模板只允许两个 %s 占位；多出来的 % 会被 Sprintf 渲染成 %!x(MISSING) 之类。
		for _, raw := range [][]byte{win, lin} {
			if bytes.Contains(raw, []byte("%!")) {
				t.Errorf("[%s] 模板渲染出了 Sprintf 的错误标记，模板里混进了多余的 %%", lang)
			}
		}
	}
}

func TestTrustLocalCAPerPlatform(t *testing.T) {
	for _, lang := range []string{LangZH, LangEN} {
		if got := parseTemplate(t, renderTemplateFor("windows", lang, ""))["server"].(map[string]any)["tls"].(map[string]any)["trust_local_ca"]; got != true {
			t.Errorf("[%s] Windows 模板 trust_local_ca 应为 true，实际 %v", lang, got)
		}
		if got := parseTemplate(t, renderTemplateFor("linux", lang, ""))["server"].(map[string]any)["tls"].(map[string]any)["trust_local_ca"]; got != false {
			t.Errorf("[%s] Linux 模板 trust_local_ca 应为 false，实际 %v", lang, got)
		}
	}
}

// basedir 经 quoteYAML 写入后必须能被 fileOnlyBaseDir 原样读回：反斜杠、空格、中文。
func TestRenderedBaseDirRoundTrips(t *testing.T) {
	for _, baseDir := range []string{`D:\ASA 数据\服务器`, "/srv/asa data/数据", `\\?\C:\long`} {
		dir := t.TempDir()
		writeConfig(t, dir, string(renderTemplate(LangZH, baseDir)))
		if got := fileOnlyBaseDir(dir); got != baseDir {
			t.Errorf("basedir 写入 %q，读回 %q", baseDir, got)
		}
	}
}

func TestTemplateLangFor(t *testing.T) {
	cases := []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{"windows 恒中文", "windows", map[string]string{"LANG": "C"}, LangZH},
		{"linux 未设置", "linux", nil, LangEN},
		{"linux C", "linux", map[string]string{"LANG": "C"}, LangEN},
		{"linux POSIX", "linux", map[string]string{"LANG": "POSIX"}, LangEN},
		{"linux C.UTF-8", "linux", map[string]string{"LANG": "C.UTF-8"}, LangZH},
		{"linux en_US.utf8", "linux", map[string]string{"LANG": "en_US.utf8"}, LangZH},
		{"linux zh_CN.GBK", "linux", map[string]string{"LANG": "zh_CN.GBK"}, LangEN},
		{"LC_ALL 优先于 LANG", "linux", map[string]string{"LC_ALL": "C", "LANG": "zh_CN.UTF-8"}, LangEN},
		{"LC_CTYPE 优先于 LANG", "linux", map[string]string{"LC_CTYPE": "en_US.UTF-8", "LANG": "C"}, LangZH},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			getenv := func(k string) string { return c.env[k] }
			if got := templateLangFor(c.goos, getenv); got != c.want {
				t.Errorf("templateLangFor = %q，期望 %q", got, c.want)
			}
		})
	}
}

func TestNormalizeLang(t *testing.T) {
	for in, want := range map[string]string{"": "", "zh": LangZH, " EN ": LangEN} {
		if got, err := NormalizeLang(in); err != nil || got != want {
			t.Errorf("NormalizeLang(%q) = %q, %v，期望 %q", in, got, err, want)
		}
	}
	if _, err := NormalizeLang("jp"); err == nil {
		t.Error("不支持的语言应报错")
	}
}
