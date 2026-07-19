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
	ansiRegexp = regexp.MustCompile(
		`\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])`,
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
)

// CleanConsoleOutput reads from r line by line, strips ANSI escape sequences and
// control characters, trims trailing whitespace, and writes each cleaned line to w.
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

		if _, err := w.Write(
			append(line, '\n'),
		); err != nil {
			return err
		}
	}

	return scanner.Err()
}
