package actions

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"asa-server/internal/appconfig"
)

// prompter 是命令行交互的最小封装：整条命令共用一个 bufio.Reader——每次提问都
// 新建 Reader 的话，上一个 Reader 预读进缓冲区的输入就丢了（管道喂多行答案时
// 第二个问题会读到 EOF）。in/out 可注入，单测用 strings.Reader + bytes.Buffer。
type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

func newPrompter(in io.Reader, out io.Writer) *prompter {
	return &prompter{in: bufio.NewReader(in), out: out}
}

func stdPrompter() *prompter { return newPrompter(os.Stdin, os.Stdout) }

// stdinIsTerminal 报告标准输入是不是终端。不是终端（管道、CI、systemd）时即使
// 没加 --non-interactive 也不该提问——没人会回答，只会卡住或读到 EOF。
func stdinIsTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// Line 打印 question 并读一行，去掉首尾空白。
func (p *prompter) Line(question string) (string, error) {
	fmt.Fprint(p.out, question)
	line, err := p.in.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", fmt.Errorf("读取输入失败: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// Confirm 问一个是非题，直接回车取 def。
func (p *prompter) Confirm(question string, def bool) (bool, error) {
	hint := " [y/N] "
	if def {
		hint = " [Y/n] "
	}
	for {
		ans, err := p.Line(question + hint)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(ans) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
}

// askTemplateLang 让用户亲眼判断终端能不能显示中文，据此选模板注释语言。
//
// 这是唯一可靠的判据：服务器 locale 是 UTF-8、SSH 客户端却按 GBK 解码时（实际
// 遇到过），服务器这一侧什么都探测不到。提示与选项中英双语、**英文在前**——会选
// 「乱码」的人恰恰看不懂中文部分。见 docs/SETUP_FLOW_OPTIMIZATION_PLAN.md
// Part 2 §P2-3.1(c)。直接回车取 DefaultTemplateLang()（locale 推断）。
func (p *prompter) askTemplateLang() (string, error) {
	def := appconfig.DefaultTemplateLang()
	defChoice := "1"
	if def == appconfig.LangEN {
		defChoice = "2"
	}
	fmt.Fprintln(p.out, "Config file comments language / 配置文件注释语言")
	fmt.Fprintln(p.out, "  Can you read this line correctly?  ->  中文显示测试：数据目录、下载代理")
	fmt.Fprintln(p.out, "  [1] Yes, Chinese comments / 能正常显示，用中文注释")
	fmt.Fprintln(p.out, "  [2] No (garbled), English comments / 显示乱码，用英文注释")
	for {
		ans, err := p.Line(fmt.Sprintf("Choose [1/2] (default %s): ", defChoice))
		if err != nil {
			return "", err
		}
		if ans == "" {
			ans = defChoice
		}
		switch ans {
		case "1":
			return appconfig.LangZH, nil
		case "2":
			return appconfig.LangEN, nil
		}
		fmt.Fprintln(p.out, "Please enter 1 or 2.")
	}
}
