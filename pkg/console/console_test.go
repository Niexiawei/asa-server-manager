package console

import (
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
