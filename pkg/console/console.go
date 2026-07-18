// Package console provides helpers for cleaning terminal/console output
// (ANSI escape sequences and C0 control characters) with no domain dependencies.
package console

import (
	"bufio"
	"bytes"
	"io"
	"regexp"
	"strings"
)

var (
	// ansiRegexp matches ANSI escape sequences (e.g. ESC [ ? ... h/l/m).
	ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	// ctrlRegexp matches C0 control characters except newline (\n).
	ctrlRegexp = regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`)
)

// CleanConsoleOutput reads from r line by line, strips ANSI escape sequences and
// control characters, trims trailing whitespace, and writes each cleaned line to w.
func CleanConsoleOutput(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()

		// 去掉 ANSI / 控制字符
		line = ansiRegexp.ReplaceAll(line, []byte{})
		line = ctrlRegexp.ReplaceAll(line, []byte{})

		// 输出，保证每行以换行符结尾
		line = bytes.TrimRight(line, " \t")
		if _, err := w.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// RemoveANSIEscapes removes ANSI escape sequences from a string and trims it.
func RemoveANSIEscapes(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		// Check if this is the start of an escape sequence
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip the escape sequence
			i += 2
			// Skip until we find a letter or other terminator
			for i < len(s) {
				c := s[i]
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '@' {
					i++
					break
				}
				i++
			}
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return strings.TrimSpace(result.String())
}
