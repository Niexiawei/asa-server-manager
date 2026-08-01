package console

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clean(t *testing.T, in string) string {
	t.Helper()

	var out strings.Builder
	if err := CleanConsoleOutput(strings.NewReader(in), &out); err != nil {
		t.Fatalf("CleanConsoleOutput() error = %v", err)
	}
	return out.String()
}

func TestCleanConsoleOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "OSC 窗口标题整段剥离",
			in:   "\x1b]0;E:\\asa_server_data\\steamcmd\\steamcmd.exe\x07done\n",
			want: "done\n",
		},
		{
			name: "OSC 以 ST(ESC\\) 结束",
			in:   "\x1b]0;title\x1b\\rest\n",
			want: "rest\n",
		},
		{
			name: "OSC 被截断在行尾",
			in:   "head\x1b]0;E:\\steamcmd.exe\n",
			want: "head\n",
		},
		{
			name: "DCS/APC 同样整段剥离",
			in:   "a\x1bP1$r0m\x1b\\b\x1b_note\x07c\n",
			want: "abc\n",
		},
		{
			name: "CSI 序列仍被剥离",
			in:   "\x1b[?25l\x1b[2J\x1b[m\x1b[Hhello\x1b[32m world\x1b[0m\n",
			want: "hello world\n",
		},
		{
			name: "保留制表符并裁掉尾部空格",
			in:   "col1\tcol2   \n",
			want: "col1\tcol2\n",
		},
		{
			name: "连续空行折叠成一个",
			in:   "a\r\n\r\n\r\n\r\n\r\nb\r\n",
			want: "a\n\nb\n",
		},
		{
			name: "开头的空行被丢弃",
			in:   "\r\n\r\n\r\nfirst\r\n",
			want: "first\n",
		},
		{
			name: "真实 steamcmd ConPTY 片段",
			in: "\x1b[?25l\x1b[2J\x1b[m\x1b[H" +
				strings.Repeat("\r\n", 30) +
				"\x1b[H\x1b]0;E:\\asa_server_data\\steamcmd\\steamcmd.exe\x07\x1b[?25h\n",
			// 整段都是清屏 + 空行 + 窗口标题，清洗后不应产生任何输出
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clean(t, tt.in); got != tt.want {
				t.Errorf("CleanConsoleOutput()\ngot  = %q\nwant = %q", got, tt.want)
			}
		})
	}
}

// 回归：ESC] 曾被当成两字节 C1 序列，导致窗口标题内容以 "0;<路径>" 的形式漏成正文
func TestCleanConsoleOutputNoOSCLeak(t *testing.T) {
	got := clean(t, "\x1b]0;E:\\asa_server_data\\steamcmd\\steamcmd.exe\x07\n")

	for _, leak := range []string{"0;", "steamcmd.exe", "asa_server_data"} {
		if strings.Contains(got, leak) {
			t.Errorf("输出泄漏了 OSC 内容 %q: got = %q", leak, got)
		}
	}
}

func cleanScreen(t *testing.T, in string) string {
	t.Helper()

	var out strings.Builder
	if err := CleanScreenOutput(strings.NewReader(in), &out); err != nil {
		t.Fatalf("CleanScreenOutput() error = %v", err)
	}
	return out.String()
}

func TestCleanScreenOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "光标定位当换行，不粘行",
			in:   "[API] Loading...\x1b[10;1H\x1b[?25h\x1b[m[API] Added DLL search directory\n",
			want: "[API] Loading...\n[API] Added DLL search directory\n",
		},
		{
			name: "CUP 的 f 形式同样当换行",
			in:   "first\x1b[22;1fsecond\n",
			want: "first\nsecond\n",
		},
		{
			name: "CNL 当换行",
			in:   "first\x1b[2Esecond\n",
			want: "first\nsecond\n",
		},
		{
			name: "无参数的 ESC[H 也当换行",
			in:   "first\x1b[Hsecond\n",
			want: "first\nsecond\n",
		},
		{
			name: "连续光标定位切出的空段折叠成一个空行",
			in:   "a\x1b[5;1H\x1b[6;1H\x1b[7;1Hb\n",
			want: "a\n\nb\n",
		},
		{
			name: "颜色与 OSC 仍被剥离",
			in:   "\x1b[38;5;2mgreen\x1b[m\x1b]0;title\x07\n",
			want: "green\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanScreen(t, tt.in); got != tt.want {
				t.Errorf("CleanScreenOutput()\ngot  = %q\nwant = %q", got, tt.want)
			}
		})
	}
}

// 用 AsaApiLoader 的真实 PTY 输出跑 golden 比对
func TestCleanScreenOutputGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "asaapi_pty.log"))
	if err != nil {
		t.Fatalf("读取 testdata 失败: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "asaapi_pty.golden"))
	if err != nil {
		t.Fatalf("读取 golden 失败: %v", err)
	}

	got := cleanScreen(t, string(raw))

	if got != string(want) {
		t.Errorf("与 golden 不一致\ngot  =\n%s\nwant =\n%s", got, want)
	}

	if strings.Contains(got, "\x1b") {
		t.Error("输出中仍有 ESC 残留")
	}

	// 这五处是 AsaApiLoader 用光标定位换行的地方，曾经各粘成一行
	for _, glued := range []string{
		"Loading...07/",
		"Initialized hooks07/",
		"-----[Success]",
		"Loading plugins..07/",
		"Loaded all pluginsSetting",
	} {
		if strings.Contains(got, glued) {
			t.Errorf("出现粘行 %q", glued)
		}
	}
}

// 锁住前提：同一份输入交给 CleanConsoleOutput 仍然会粘行，
// 所以 AsaApiLoader 必须走 CleanScreenOutput，两个函数不能合并
func TestCleanConsoleOutputStillGluesScreenOutput(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "asaapi_pty.log"))
	if err != nil {
		t.Fatalf("读取 testdata 失败: %v", err)
	}

	got := clean(t, string(raw))

	if !strings.Contains(got, "Loading...07/") {
		t.Error("CleanConsoleOutput 不再粘行了——若已修复，本测试和 CleanScreenOutput 的存在理由都需要重新评估")
	}
}
