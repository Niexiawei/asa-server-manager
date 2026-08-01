package actions

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"asa-server/internal/appconfig"
	"asa-server/internal/auth"
	cfgpkg "asa-server/internal/config"

	"github.com/mattn/go-runewidth"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

// AuthUserCommand 提供用户管理与本机救援。
//
// 这些命令不是"锦上添花"：auth.db 不像 JSON 那样能手动编辑，
// 所以 CLI 是忘记密码、丢失两步验证设备时唯一的本地救援通道。
func AuthUserCommand() *cli.Command {
	return &cli.Command{
		Name:  "user",
		Usage: "管理面板账户（本机救援通道）",
		Commands: []*cli.Command{
			{
				Name:   "list",
				Usage:  "列出所有账户",
				Action: withManager(actionUserList),
			},
			{
				Name:      "add",
				Usage:     "新增账户（密码交互式输入）",
				ArgsUsage: "<用户名>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "role", Value: auth.RoleOperator, Usage: "admin 或 operator"},
					&cli.BoolFlag{Name: "random", Usage: "生成随机密码而不是交互式输入"},
				},
				Action: withManager(actionUserAdd),
			},
			{
				Name:      "passwd",
				Usage:     "重置账户密码",
				ArgsUsage: "<用户名>",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "random", Usage: "生成随机强密码并打印一次"},
					&cli.BoolFlag{Name: "stdin", Usage: "从标准输入读取密码（供脚本使用）"},
				},
				Action: withManager(actionUserPasswd),
			},
			{
				Name:      "role",
				Usage:     "修改账户角色",
				ArgsUsage: "<用户名> <admin|operator>",
				Action:    withManager(actionUserRole),
			},
			{
				Name:      "disable",
				Usage:     "禁用账户",
				ArgsUsage: "<用户名>",
				Action:    withManager(actionUserDisable),
			},
			{
				Name:      "enable",
				Usage:     "启用账户",
				ArgsUsage: "<用户名>",
				Action:    withManager(actionUserEnable),
			},
			{
				Name:      "delete",
				Usage:     "删除账户",
				ArgsUsage: "<用户名>",
				Action:    withManager(actionUserDelete),
			},
			{
				Name:      "unlock",
				Usage:     "解除因连续登录失败造成的锁定",
				ArgsUsage: "<用户名>",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "keep-ip-locks",
						Usage: "只解开用户维度，保留按来源 IP 的锁定",
					},
				},
				Action: withManager(actionUserUnlock),
			},
			{
				Name:      "totp-reset",
				Usage:     "解绑两步验证（用户丢手机时的救援路径）",
				ArgsUsage: "<用户名>",
				Action:    withManager(actionUserTOTPReset),
			},
			{
				Name:  "audit",
				Usage: "查看审计日志",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "user", Usage: "只看某个用户"},
					&cli.StringFlag{Name: "event", Usage: "只看某类事件，如 login_fail"},
					&cli.IntFlag{Name: "limit", Value: 50, Usage: "最多显示多少条"},
				},
				Action: withManager(actionUserAudit),
			},
		},
	}
}

// withManager 负责打开数据库、执行迁移、收尾关闭，并在写操作后通知
// 运行中的服务重新加载。每个子命令只关心自己那点逻辑。
func withManager(fn func(context.Context, *cli.Command, *auth.Manager) error) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		m, err := auth.Initialize(cfgpkg.BaseDir)
		if err != nil {
			return err
		}
		defer m.Close()
		return fn(ctx, cmd, m)
	}
}

func actionUserList(ctx context.Context, cmd *cli.Command, m *auth.Manager) error {
	users := m.Users()
	if len(users) == 0 {
		fmt.Println("尚未创建任何账户。")
		fmt.Println("启用鉴权后，请在本机浏览器打开面板走首次引导，或执行 asa-server user add <用户名> --role admin")
		return nil
	}

	rows := [][]string{{"用户名", "角色", "状态", "两步验证", "锁定", "最后登录"}}
	now := time.Now()
	for _, u := range users {
		status := "启用"
		if u.Disabled {
			status = "已禁用"
		}
		totpState := "未绑定"
		if u.TOTPEnabled {
			totpState = "已绑定"
		}
		lockState := "-"
		if st, err := auth.CheckLock(ctx, m.DB(), auth.ScopeUser, u.Username, now); err == nil && st.Locked {
			lockState = fmt.Sprintf("剩余 %d 分钟", int(st.Remaining.Minutes())+1)
		}
		lastLogin := "从未"
		if !u.LastLoginAt.IsZero() {
			lastLogin = u.LastLoginAt.Format("2006-01-02 15:04")
		}
		rows = append(rows, []string{u.Username, u.Role, status, totpState, lockState, lastLogin})
	}
	printTable(rows)
	return nil
}

func actionUserAdd(ctx context.Context, cmd *cli.Command, m *auth.Manager) error {
	username := cmd.Args().First()
	if username == "" {
		return errors.New("请指定用户名：asa-server user add <用户名>")
	}
	role := cmd.String("role")

	var password string
	var err error
	if cmd.Bool("random") {
		password, err = auth.GeneratePassword(20)
	} else {
		password, err = promptNewPassword()
	}
	if err != nil {
		return err
	}

	if _, err := m.CreateUser(ctx, username, password, role, auth.ActorCLI); err != nil {
		return err
	}
	fmt.Printf("已创建账户 %s（角色 %s）。\n", username, role)
	if cmd.Bool("random") {
		printGeneratedPassword(password)
	}
	notifyReload()
	return nil
}

func actionUserPasswd(ctx context.Context, cmd *cli.Command, m *auth.Manager) error {
	username := cmd.Args().First()
	if username == "" {
		return errors.New("请指定用户名：asa-server user passwd <用户名>")
	}
	if _, ok := m.Lookup(username); !ok {
		return fmt.Errorf("%w: %s", auth.ErrUserNotFound, username)
	}

	var password string
	var err error
	switch {
	case cmd.Bool("random"):
		password, err = auth.GeneratePassword(20)
	case cmd.Bool("stdin"):
		password, err = readPasswordFromStdin()
	default:
		password, err = promptNewPassword()
	}
	if err != nil {
		return err
	}

	// ChangePassword 内部在同一个事务里完成：写哈希、session_version++
	// （踢掉所有已登录设备）、清除登录失败锁定
	if err := m.ChangePassword(ctx, username, password, auth.ActorCLI, auth.EventPasswordReset); err != nil {
		return err
	}

	fmt.Printf("已重置用户 %s 的密码。\n所有已登录设备已被登出。\n", username)
	if cmd.Bool("random") {
		printGeneratedPassword(password)
	}
	notifyReload()
	return nil
}

func actionUserRole(ctx context.Context, cmd *cli.Command, m *auth.Manager) error {
	username, role := cmd.Args().Get(0), cmd.Args().Get(1)
	if username == "" || role == "" {
		return errors.New("用法：asa-server user role <用户名> <admin|operator>")
	}
	if err := m.SetRole(ctx, username, role, auth.ActorCLI); err != nil {
		return err
	}
	fmt.Printf("已将 %s 的角色改为 %s。\n", username, role)
	notifyReload()
	return nil
}

func actionUserDisable(ctx context.Context, cmd *cli.Command, m *auth.Manager) error {
	return setUserDisabled(ctx, cmd, m, true)
}

func actionUserEnable(ctx context.Context, cmd *cli.Command, m *auth.Manager) error {
	return setUserDisabled(ctx, cmd, m, false)
}

func setUserDisabled(ctx context.Context, cmd *cli.Command, m *auth.Manager, disabled bool) error {
	username := cmd.Args().First()
	if username == "" {
		return errors.New("请指定用户名")
	}
	if err := m.SetDisabled(ctx, username, disabled, auth.ActorCLI); err != nil {
		return err
	}
	if disabled {
		fmt.Printf("已禁用 %s，其所有会话已失效。\n", username)
	} else {
		fmt.Printf("已启用 %s。\n", username)
	}
	notifyReload()
	return nil
}

func actionUserDelete(ctx context.Context, cmd *cli.Command, m *auth.Manager) error {
	username := cmd.Args().First()
	if username == "" {
		return errors.New("请指定用户名：asa-server user delete <用户名>")
	}
	if err := m.DeleteUser(ctx, username, auth.ActorCLI); err != nil {
		return err
	}
	fmt.Printf("已删除账户 %s。\n", username)
	notifyReload()
	return nil
}

func actionUserUnlock(ctx context.Context, cmd *cli.Command, m *auth.Manager) error {
	username := cmd.Args().First()
	if username == "" {
		return errors.New("请指定用户名：asa-server user unlock <用户名>")
	}
	if err := m.Unlock(ctx, username, auth.ActorCLI); err != nil {
		return err
	}
	fmt.Printf("已解除 %s 的登录锁定。\n", username)

	// 限流是用户名和来源 IP 两个维度独立计数的。单管理员的服务器上，
	// 所有失败尝试都来自同一台机器，两个维度必然同时被锁——
	// 只解开用户维度的话，用户会发现"解锁了还是登不上"。
	if !cmd.Bool("keep-ip-locks") {
		n, err := auth.ClearScopeFailures(ctx, m.DB(), auth.ScopeIP)
		if err != nil {
			return err
		}
		if n > 0 {
			fmt.Printf("同时清除了 %d 条按来源 IP 的锁定记录（用 --keep-ip-locks 可保留）。\n", n)
		}
	}
	notifyReload()
	return nil
}

func actionUserTOTPReset(ctx context.Context, cmd *cli.Command, m *auth.Manager) error {
	username := cmd.Args().First()
	if username == "" {
		return errors.New("请指定用户名：asa-server user totp-reset <用户名>")
	}
	if err := m.ResetTOTP(ctx, username, auth.ActorCLI); err != nil {
		return err
	}
	fmt.Printf("已解绑 %s 的两步验证，其恢复码也已全部清除。\n", username)
	fmt.Println("该用户下次登录只需密码，登录后可重新绑定。")
	notifyReload()
	return nil
}

func actionUserAudit(ctx context.Context, cmd *cli.Command, m *auth.Manager) error {
	entries, err := auth.QueryAudit(ctx, m.DB(), auth.AuditFilter{
		Username: cmd.String("user"),
		Event:    cmd.String("event"),
		Limit:    cmd.Int("limit"),
	})
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("没有匹配的审计记录。")
		return nil
	}

	rows := [][]string{{"时间", "事件", "用户", "操作者", "来源 IP", "详情"}}
	for _, e := range entries {
		rows = append(rows, []string{
			e.Timestamp.Format("2006-01-02 15:04:05"),
			e.Event, dash(e.Username), dash(e.Actor), dash(e.ClientIP), e.Detail,
		})
	}
	printTable(rows)
	return nil
}

// printTable 按**显示宽度**对齐，而不是按字符数。
//
// text/tabwriter 按 rune 计数，而"用户名"这类中日韩字符每个占两列，
// 于是含中文的列会少补一半空格，整张表看着是歪的。
func printTable(rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, cell := range r {
			if i < len(widths) {
				widths[i] = max(widths[i], runewidth.StringWidth(cell))
			}
		}
	}

	var b strings.Builder
	for _, r := range rows {
		for i, cell := range r {
			b.WriteString(cell)
			if i < len(r)-1 { // 最后一列不补尾随空格
				b.WriteString(strings.Repeat(" ", widths[i]-runewidth.StringWidth(cell)+2))
			}
		}
		b.WriteByte('\n')
	}
	fmt.Print(b.String())
}

// ---- 密码输入 ----

// promptNewPassword 交互式读取密码，不回显。
//
// 刻意**不**提供 --password <明文> 参数：明文会进 PowerShell 历史
// （ConsoleHost_history.txt）和进程命令行，本机任何进程都能看到。
// 脚本化需求由 --stdin 覆盖。
func promptNewPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("当前不是交互式终端，请改用 --stdin 或 --random")
	}

	fmt.Print("新密码: ")
	first, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("读取密码失败: %w", err)
	}

	fmt.Print("确认新密码: ")
	second, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("读取密码失败: %w", err)
	}

	if string(first) != string(second) {
		return "", errors.New("两次输入的密码不一致")
	}
	pw := string(first)
	minLen := appconfig.Get().Auth.Password.MinLength
	if err := auth.ValidatePasswordStrength(pw, minLen); err != nil {
		return "", err
	}
	return pw, nil
}

func readPasswordFromStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("读取标准输入失败: %w", err)
	}
	pw := strings.TrimRight(string(data), "\r\n")
	minLen := appconfig.Get().Auth.Password.MinLength
	if err := auth.ValidatePasswordStrength(pw, minLen); err != nil {
		return "", err
	}
	return pw, nil
}

func printGeneratedPassword(pw string) {
	fmt.Println()
	fmt.Println("    " + pw)
	fmt.Println()
	fmt.Println("请立即保存 —— 此密码不会再次显示。")
}

// ---- 通知运行中的服务重新加载 ----

// notifyReload 让正在运行的服务重新加载内存副本。
//
// CLI 和 API 服务会同时打开同一个 auth.db（WAL 模式下这是安全的），
// 但服务的内存副本不会自动感知外部改动。没有这一步的话，
// 丢手机的用户得等服务重启才能登录。
func notifyReload() {
	port := appconfig.Get().Server.Port
	scheme := "https"
	if !appconfig.Get().Server.TLS.Enabled {
		scheme = "http"
	}
	url := fmt.Sprintf("%s://127.0.0.1:%d/api/auth/reload", scheme, port)

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			// 目标是 127.0.0.1 上的本进程同族服务，用的是本地 CA 自签证书。
			// 这里的身份保证来自"连接目的地是回环地址"，不是证书链——
			// 而服务端也正是靠这一点来限制该接口只接受本机调用。
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}

	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		fmt.Println("改动已写入。服务当前未运行，将在下次启动时生效。")
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusOK {
		fmt.Println("✓ 已通知运行中的服务重新加载（无需重启）")
		return
	}
	fmt.Printf("改动已写入，但通知服务重载失败（HTTP %d），请重启服务使其生效。\n", resp.StatusCode)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
