//go:build windows

package procx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 查测试进程自己：名字取测试二进制的文件名，命令行片段用 go test 必传的 -test.
// 参数。以前这里查的是作者本机某个实例（Port=9310）、只打印不断言，查到查不到
// 都通过（docs/TEST_ENV_COUPLING_PLAN.md T5）。
func TestQueryProcess_FindsSelf(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	res, err := QueryProcess(filepath.Base(exe), "-test.")
	if err != nil {
		t.Fatalf("QueryProcess: %v", err)
	}
	pid := uint32(os.Getpid())
	for _, p := range res {
		if p.ProcessId == pid {
			if p.CommandLine == "" {
				t.Errorf("查到了自己（PID %d），但 CommandLine 为空", pid)
			}
			return
		}
	}
	t.Errorf("没有查到测试进程自己（%s，PID %d），结果：%+v", filepath.Base(exe), pid, res)
}

func TestQueryProcess_NoMatchIsEmptyNotError(t *testing.T) {
	res, err := QueryProcess(fmt.Sprintf("asa-no-such-process-%d.exe", os.Getpid()), "")
	if err != nil {
		t.Fatalf("查不到不应报错: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("应查不到任何进程，实际 %+v", res)
	}
}

// escapeWQL 决定实例名 / 命令行里带 ' % _ [ 时会不会查错进程：这几个字符在 WQL 的
// LIKE 里分别是字符串定界符与通配符。
func TestEscapeWQL(t *testing.T) {
	cases := map[string]string{
		"plain":     "plain",
		"it's":      `it\'s`,
		"100%":      "100[%]",
		"a_b":       "a[_]b",
		"[x]":       "[[]x]",
		`C:\a\b`:    `C:\\a\\b`,
		`\'`:        `\\\'`, // 反斜杠先转，单引号的转义符不能被再转一次
		"%_['":      `[%][_][[]\'`,
		"AltSave=x": "AltSave=x",
	}
	for in, want := range cases {
		if got := escapeWQL(in); got != want {
			t.Errorf("escapeWQL(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// 转义后的字面量真的能被 WMI 接受：带 ' \ % _ [ 的查询不能报语法错误。
// 这条用例写下时当场抓到 escapeWQL 把 ' 翻倍、\ 原样保留，WMI 一律报「无效查询」。
func TestQueryProcess_EscapedQueryIsAccepted(t *testing.T) {
	for _, s := range []string{"it's", `C:\a\b`, "100%", "a_b", "[x]", `it's C:\100%_[x]`} {
		if _, err := QueryProcess("asa-no-such-proc.exe", s); err != nil {
			t.Errorf("命令行片段 %q 的查询应被 WMI 接受: %v", s, err)
		}
		if _, err := QueryProcess(s, ""); err != nil {
			t.Errorf("名字 %q 的查询应被 WMI 接受: %v", s, err)
		}
	}
}

// _ 是 LIKE 的「任意一个字符」：不转义的话 procx_test.exe 会匹配到 procx.test.exe。
// 转义之后必须查不到自己。
func TestQueryProcess_UnderscoreIsLiteral(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	base := filepath.Base(exe)
	before, after, ok := strings.Cut(base, ".")
	if !ok {
		t.Skipf("测试二进制名 %q 里没有点，构造不出对照", base)
	}
	probe := before + "_" + after
	res, err := QueryProcess(probe, "-test.")
	if err != nil {
		t.Fatalf("QueryProcess: %v", err)
	}
	for _, p := range res {
		if p.ProcessId == uint32(os.Getpid()) {
			t.Fatalf("%q 不应匹配到 %q：_ 被当成了通配符", probe, base)
		}
	}
}

// 反斜杠转义后仍按字面匹配：用测试二进制所在目录（满是反斜杠的 Windows 路径）
// 作命令行片段，必须能查到自己。
func TestQueryProcess_MatchesPathWithBackslashes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	res, err := QueryProcess(filepath.Base(exe), filepath.Dir(exe))
	if err != nil {
		t.Fatalf("QueryProcess: %v", err)
	}
	pid := uint32(os.Getpid())
	for _, p := range res {
		if p.ProcessId == pid {
			return
		}
	}
	t.Errorf("用目录 %q 作命令行片段没有查到自己（PID %d），结果：%+v", filepath.Dir(exe), pid, res)
}
