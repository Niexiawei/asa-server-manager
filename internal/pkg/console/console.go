// Package console provides helpers for cleaning terminal/console output
// (ANSI escape sequences and C0 control characters) with no domain dependencies.
package console

import (
	"bufio"
	"bytes"
	"io"
	"regexp"
)

var (
	// ANSI escape sequence
	//
	// 示例:
	// ESC[32m
	// ESC[0m
	// ESC[2K
	// ESC[?25h
	// ESC]0;窗口标题 BEL
	//
	// 三个分支顺序不可调整：Go 的正则是 leftmost-first，
	// 字符串型序列必须排在单字符 C1 分支之前，
	// 否则 ESC] 会被当成两字节序列吃掉，剩下的标题内容变成正文
	ansiRegexp = regexp.MustCompile(
		// 字符串型序列 OSC/DCS/APC/PM/SOS，直到 BEL 或 ST(ESC\) 结束
		// 终止符允许缺省，容忍被截断在行尾的序列
		`\x1B[\]P_^X][^\x07\x1B]*(?:\x07|\x1B\\)?` +
			// CSI
			`|\x1B\[[0-?]*[ -/]*[@-~]` +
			// 其余 C1 两字节序列
			`|\x1B[@-Z\\-_]`,
	)

	// 控制字符
	//
	// 删除:
	// 0x00-0x08
	// 0x0B
	// 0x0C
	// 0x0E-0x1F
	// 0x7F
	//
	// 保留:
	// \n
	// \r
	// \t
	ctrlRegexp = regexp.MustCompile(
		`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`,
	)

	// 光标定位序列
	//
	// CUP: ESC[row;colH 或 ESC[row;colf
	// CNL: ESC[nE
	//
	// 屏幕型程序（如 AsaApiLoader）不发 \n，而是用它跳到下一行，
	// 因此必须翻成换行而不是直接删掉，否则相邻两条记录会粘成一行
	cursorNewlineRegexp = regexp.MustCompile(
		`\x1B\[[0-9;]*[HfE]`,
	)
)

// CleanConsoleOutput reads from r line by line, strips ANSI escape sequences and
// control characters, trims trailing whitespace, and writes each cleaned line to w.
// 连续的空行会被折叠成一个，避免 ConPTY 清屏产生的成百上千个空行灌进日志。
func CleanConsoleOutput(
	r io.Reader,
	w io.Writer,
) error {

	scanner := bufio.NewScanner(r)

	// 增大单行限制
	scanner.Buffer(
		make([]byte, 4096),
		10*1024*1024,
	)

	// 首行为空同样跳过，避免开头就是空行
	prevBlank := true

	for scanner.Scan() {

		line := scanner.Bytes()

		// remove ANSI color/control sequences
		line = ansiRegexp.ReplaceAll(line, nil)

		// remove non-printable control chars
		line = ctrlRegexp.ReplaceAll(line, nil)

		// 保留换行，去掉尾部空格
		line = bytes.TrimRight(
			line,
			" \t",
		)

		// 折叠连续空行
		blank := len(line) == 0
		if blank && prevBlank {
			continue
		}
		prevBlank = blank

		if _, err := w.Write(
			append(line, '\n'),
		); err != nil {
			return err
		}
	}

	return scanner.Err()
}

// CleanScreenOutput 清洗「用光标定位排版」的控制台输出（如 AsaApiLoader）。
//
// 与 CleanConsoleOutput 的唯一区别：先把光标定位序列翻成换行，再按同样的规则清洗。
// 这类程序换行不发 \n，若把光标定位当普通 ANSI 删掉，相邻两条记录会粘成一行。
// steamcmd 不这么排版（靠 \r 重画进度行），所以它仍应使用 CleanConsoleOutput。
func CleanScreenOutput(
	r io.Reader,
	w io.Writer,
) error {

	scanner := bufio.NewScanner(r)

	// 增大单行限制
	scanner.Buffer(
		make([]byte, 4096),
		10*1024*1024,
	)

	// 首行为空同样跳过，避免开头就是空行
	prevBlank := true

	for scanner.Scan() {

		// 一条输入行可能被光标定位切成多行
		raw := cursorNewlineRegexp.ReplaceAll(
			scanner.Bytes(),
			[]byte{'\n'},
		)

		for line := range bytes.SplitSeq(raw, []byte{'\n'}) {

			// remove ANSI color/control sequences
			// ReplaceAll 必定返回新分配的切片，后面 append 不会写坏 raw 的底层数组
			line = ansiRegexp.ReplaceAll(line, nil)

			// remove non-printable control chars
			line = ctrlRegexp.ReplaceAll(line, nil)

			// 保留换行，去掉尾部空格
			line = bytes.TrimRight(
				line,
				" \t",
			)

			// 折叠连续空行
			blank := len(line) == 0
			if blank && prevBlank {
				continue
			}
			prevBlank = blank

			if _, err := w.Write(
				append(line, '\n'),
			); err != nil {
				return err
			}
		}
	}

	return scanner.Err()
}
