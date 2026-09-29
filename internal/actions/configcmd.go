package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/urfave/cli/v3"

	"asa-server/internal/appconfig"
	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/fsutil"
)

// ConfigCommand 是 `asa-server config`：把「生成 config.yaml」从启动副作用里拆出来
// 成为显式步骤，让用户在 setup 下载几百 MB / 几十 GB 之前就能改好下载代理、端口、
// prefix 模式等。标准部署流程：config init → 编辑 → config validate → setup。
// 见 docs/SETUP_FLOW_OPTIMIZATION_PLAN.md Part 2 §P2-3.3。
//
// main.go 对 config 子命令不做启动副作用（不生成模板、不建数据目录，见
// startupModeFor），所以这里看到的 appconfig.ConfigMissing() 是真实状态。
func ConfigCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "生成 / 查看 / 校验应用配置文件 config.yaml",
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "生成 config.yaml（不建数据目录、不下载任何东西），改好后再运行 setup",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "dir",
						Usage: "config.yaml 写到哪个目录（默认：ASA_CFG 指定的目录，否则程序所在目录）",
					},
					&cli.StringFlag{
						Name:  "basedir",
						Usage: "数据目录（ARK 本体 + 存档），写进 basedir 字段；留空 = 与配置文件同目录",
					},
					&cli.StringFlag{
						Name:  "lang",
						Usage: "配置文件注释语言 zh / en（默认：交互模式下询问，否则按 locale 推断）",
					},
					&cli.BoolFlag{
						Name:  "force",
						Usage: "目标已存在时覆盖（原文件先备份为 config.yaml.bak-<时间>）；非交互模式下遮蔽已有配置也需要它",
					},
					&cli.BoolFlag{
						Name:  "non-interactive",
						Usage: "不提问（标准输入不是终端时自动如此）",
					},
				},
				Action: actionConfigInit,
			},
			{
				Name:   "path",
				Usage:  "显示当前使用的 config.yaml、三级查找位置与数据目录来源",
				Action: actionConfigPath,
			},
			{
				Name:  "validate",
				Usage: "校验 config.yaml（不修改任何文件）",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "file",
						Usage: "要校验的文件（默认：当前使用的那份）",
					},
				},
				Action: actionConfigValidate,
			},
		},
	}
}

// ---------------------------------------------------------------- config init

type configInitOptions struct {
	Dir         string
	BaseDir     string
	Lang        string
	Force       bool
	Interactive bool
}

// validateBaseDir 可在单测里替换：真实实现要求 ≥30GB 剩余空间，测试机未必满足。
var validateBaseDir = appconfig.ValidateBaseDir

func actionConfigInit(ctx context.Context, cmd *cli.Command) error {
	opts := configInitOptions{
		Dir:         cmd.String("dir"),
		BaseDir:     cmd.String("basedir"),
		Lang:        cmd.String("lang"),
		Force:       cmd.Bool("force"),
		Interactive: !cmd.Bool("non-interactive") && stdinIsTerminal(),
	}
	if err := runConfigInit(opts, stdPrompter()); err != nil {
		return cli.Exit(err.Error(), 1)
	}
	return nil
}

// runConfigInit 是 config init 的全部逻辑，与 CLI 解耦以便单测。
//
// 语言最先定：选了英文（终端显示不了中文）之后的每一行输出都得是英文。
func runConfigInit(o configInitOptions, p *prompter) error {
	out := p.out

	lang, err := appconfig.NormalizeLang(o.Lang)
	if err != nil {
		return err
	}
	if lang == "" {
		if o.Interactive {
			if lang, err = p.askTemplateLang(); err != nil {
				return err
			}
		} else {
			lang = appconfig.DefaultTemplateLang()
		}
	}
	L := func(zh, en string) string {
		if lang == appconfig.LangEN {
			return en
		}
		return zh
	}

	dir := o.Dir
	if dir == "" {
		if dir, err = appconfig.DefaultInitDir(); err != nil {
			return err
		}
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return err
	}
	target := filepath.Join(dir, appconfig.ConfigFileName)

	// 1) 目标本身已存在：覆盖要显式同意。
	force := o.Force
	if fileExists(target) && !force {
		if !o.Interactive {
			return fmt.Errorf(L("配置文件已存在：%s\n如需重新生成请加 --force（原文件会先备份为 %s.bak-<时间>）",
				"Config file already exists: %s\nAdd --force to regenerate it (the old file is backed up as %s.bak-<time> first)"),
				target, appconfig.ConfigFileName)
		}
		ok, err := p.Confirm(fmt.Sprintf(L("配置文件已存在：%s，覆盖吗？（原文件会先备份）",
			"Config file already exists: %s. Overwrite it? (the old file is backed up first)"), target), false)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New(L("已取消", "Cancelled"))
		}
		force = true
	}

	// 2) 生成之后下次到底读哪份：新文件可能不被读取（被更高一级遮住 / 不在查找范围内），
	//    也可能反过来遮住一份正在用的配置。
	after, err := appconfig.ConfigPathAfterInit(target)
	if err != nil {
		return err
	}
	current := ""
	if !appconfig.ConfigMissing() {
		current = appconfig.ConfigPath()
	}
	switch {
	case !fsutil.SamePath(after, target):
		fmt.Fprintf(out, L("注意：生成后程序不会读取这份配置，而是读 %s。\n要让它生效，请设置环境变量 ASA_CFG=%s\n",
			"Note: the program will NOT read this file after it is generated; it reads %s instead.\nTo use it, set the environment variable ASA_CFG=%s\n"),
			after, dir)
		if o.Interactive {
			ok, err := p.Confirm(L("仍然生成？", "Generate it anyway?"), false)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New(L("已取消", "Cancelled"))
			}
		}
	case current != "" && !fsutil.SamePath(current, target):
		fmt.Fprintf(out, L("注意：新文件会遮蔽当前正在使用的 %s（程序目录优先于系统目录），之后程序改读新文件。\n",
			"Note: the new file will shadow the config currently in use, %s (the program directory takes precedence over the system directory).\n"),
			current)
		if o.Interactive {
			ok, err := p.Confirm(L("继续？", "Continue?"), false)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New(L("已取消", "Cancelled"))
			}
		} else if !o.Force {
			return errors.New(L("非交互模式下遮蔽已有配置需要加 --force", "Shadowing an existing config in non-interactive mode requires --force"))
		}
	}

	// 3) 数据目录。
	baseDir := o.BaseDir
	if baseDir == "" && o.Interactive {
		q := fmt.Sprintf(L("数据目录（ARK 服务端本体 + 存档，建议预留 30GB）\n直接回车 = 与配置文件同目录 %s\n> ",
			"Data directory (ARK server files + saves, reserve at least 30GB)\nPress Enter = same directory as the config file, %s\n> "), dir)
		if baseDir, err = p.Line(q); err != nil {
			return err
		}
	}
	for baseDir != "" {
		if abs, absErr := filepath.Abs(baseDir); absErr == nil {
			baseDir = abs
		}
		verr := validateBaseDir(baseDir)
		if verr == nil {
			break
		}
		if !o.Interactive {
			return verr
		}
		fmt.Fprintln(out, verr)
		if baseDir, err = p.Line(L("请换一个数据目录（直接回车 = 与配置文件同目录）：", "Choose another data directory (Enter = same as the config file): ")); err != nil {
			return err
		}
	}
	if baseDir == "" {
		// 数据就落在配置目录里（或 ASA_BASEDIR）：同样的问题（网络盘、空间不足）照样存在，
		// 但这是默认值不是用户的显式选择，只提示不拦。
		if os.Getenv("ASA_BASEDIR") == "" {
			if verr := validateBaseDir(dir); verr != nil {
				fmt.Fprintf(out, L("提示：数据将存放在配置文件所在目录，但它可能不合适：\n%v\n", "Hint: data will live in the config directory, which may be unsuitable:\n%v\n"), verr)
			}
		}
	}

	// 4) 写。
	path, err := appconfig.InitConfig(appconfig.InitOptions{Dir: dir, BaseDir: baseDir, Lang: lang, Force: force})
	if err != nil {
		return err
	}

	printConfigInitSummary(out, path, dir, baseDir, lang)
	return nil
}

func printConfigInitSummary(out io.Writer, path, dir, baseDir, lang string) {
	en := lang == appconfig.LangEN
	dataDir := baseDir
	if dataDir == "" {
		if env := os.Getenv("ASA_BASEDIR"); env != "" {
			dataDir = env
		} else {
			dataDir = dir
		}
	}
	if en {
		fmt.Fprintf(out, "\nConfig file generated: %s (English comments)\n", path)
		fmt.Fprintf(out, "Data directory: %s\n\n", dataDir)
		fmt.Fprintln(out, "Settings usually worth checking before installing:")
		fmt.Fprintln(out, "  download.github_proxy / download.http_proxy   proxies for downloading umu / GE-Proton / SteamCMD")
		fmt.Fprintln(out, "  server.port                                    management panel port (default 19193)")
		if runtime.GOOS == "linux" {
			fmt.Fprintln(out, "  linux.prefix_mode                              Wine prefix isolation for multiple instances (shared / per-instance / overlay)")
			fmt.Fprintln(out, "  linux.umu_runtime_user                         unprivileged system user that runs the game processes")
		}
		fmt.Fprintln(out, "When done, run: asa-server config validate && asa-server setup")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "If Chinese text looks garbled in your terminal, set your terminal / SSH client charset to UTF-8")
		fmt.Fprintln(out, "(the program's other console output is Chinese too). Then `asa-server config init --force --lang zh`")
		fmt.Fprintln(out, "regenerates the file with Chinese comments.")
		return
	}
	fmt.Fprintf(out, "\n已生成配置文件：%s（中文注释）\n", path)
	fmt.Fprintf(out, "数据目录：%s\n\n", dataDir)
	fmt.Fprintln(out, "开始安装前通常需要检查的配置：")
	fmt.Fprintln(out, "  download.github_proxy / download.http_proxy   下载 umu / GE-Proton / SteamCMD 的代理")
	fmt.Fprintln(out, "  server.port                                    管理面板端口（默认 19193）")
	if runtime.GOOS == "linux" {
		fmt.Fprintln(out, "  linux.prefix_mode                              多实例 Wine prefix 隔离方式（shared / per-instance / overlay）")
		fmt.Fprintln(out, "  linux.umu_runtime_user                         降权运行游戏进程的系统用户")
	}
	fmt.Fprintln(out, "改好后执行：asa-server config validate && asa-server setup")
}

// ---------------------------------------------------------------- config path

func actionConfigPath(ctx context.Context, cmd *cli.Command) error {
	return runConfigPath(os.Stdout)
}

func runConfigPath(out io.Writer) error {
	path := appconfig.ConfigPath()
	missing := appconfig.ConfigMissing()

	if missing {
		fmt.Fprintf(out, "配置文件：%s（不存在，可运行 asa-server config init 生成）\n", path)
	} else {
		fmt.Fprintf(out, "配置文件：%s\n", path)
	}

	dirs, err := appconfig.ConfigSearchDirs()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "查找顺序（找到即停，只读一份）：")
	// label 自带补齐到 8 个显示列：fmt 的 %-8s 按字符数而不是显示宽度补空格，
	// 中文标签会错位。
	level := func(n int, label, dir string) {
		if dir == "" {
			fmt.Fprintf(out, "  %d. %s  （未设置）\n", n, label)
			return
		}
		mark := "无 config.yaml"
		if fileExists(filepath.Join(dir, appconfig.ConfigFileName)) {
			mark = "有 config.yaml"
		}
		inUse := ""
		if fsutil.SamePath(filepath.Join(dir, appconfig.ConfigFileName), path) {
			inUse = "  ← 当前使用"
		}
		fmt.Fprintf(out, "  %d. %s  %s  [%s]%s\n", n, label, dir, mark, inUse)
	}
	level(1, "ASA_CFG ", dirs.ASACfg)
	level(2, "程序目录", dirs.ExeDir)
	level(3, "系统目录", dirs.SystemDir)

	cfg := appconfig.Get()
	source := "配置文件所在目录"
	switch {
	case !missing && cfg.BaseDir != "":
		source = "config.yaml 的 basedir 字段"
	case os.Getenv("ASA_BASEDIR") != "":
		source = "环境变量 ASA_BASEDIR"
	}
	fmt.Fprintf(out, "数据目录：%s（来源：%s）\n", cfgpkg.BaseDir, source)

	if !missing {
		if _, err := appconfig.CheckFile(path); err != nil {
			fmt.Fprintf(out, "\n⚠ 这份配置无法通过校验，程序会回落到默认配置运行：\n  %v\n运行 asa-server config validate 查看详情。\n", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------- config validate

func actionConfigValidate(ctx context.Context, cmd *cli.Command) error {
	if err := runConfigValidate(os.Stdout, cmd.String("file")); err != nil {
		return cli.Exit(err.Error(), 1)
	}
	return nil
}

func runConfigValidate(out io.Writer, file string) error {
	path := file
	if path == "" {
		path = appconfig.ConfigPath()
	}
	if path == "" || !fileExists(path) {
		return fmt.Errorf("未找到配置文件 %s，请先运行 asa-server config init", path)
	}
	cfg, err := appconfig.CheckFile(path)
	if err != nil {
		return fmt.Errorf("配置无效：%s\n%v", path, err)
	}

	dataDir := cfg.BaseDir
	if dataDir == "" {
		if env := os.Getenv("ASA_BASEDIR"); env != "" {
			dataDir = env
		} else {
			dataDir = filepath.Dir(path)
		}
	}
	scheme := "http"
	if cfg.Server.TLS.Enabled {
		scheme = "https"
	}
	auth := "关闭"
	if cfg.Auth.Enabled {
		auth = "开启"
	}
	fmt.Fprintf(out, "配置有效：%s\n", path)
	fmt.Fprintf(out, "  数据目录：%s\n", dataDir)
	fmt.Fprintf(out, "  管理面板：%s://<本机地址>:%d，登录鉴权%s\n", scheme, cfg.Server.Port, auth)
	if cfg.Download.GithubProxy != "" || cfg.Download.HTTPProxy != "" {
		fmt.Fprintf(out, "  下载代理：github_proxy=%q http_proxy=%q\n", cfg.Download.GithubProxy, cfg.Download.HTTPProxy)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
