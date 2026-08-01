package actions

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	"asa-server/internal/appconfig"
	"asa-server/internal/auth"
	cfgpkg "asa-server/internal/config"

	"github.com/urfave/cli/v3"
)

// AuthDBCommand 提供 auth.db 的迁移与维护。
//
// 作用域仅限鉴权数据库。项目里其它持久化（Badger 状态库、schedules.json、
// 实例 INI）不归它管——`asa-server state clear` 才是状态库那边的入口。
func AuthDBCommand() *cli.Command {
	return &cli.Command{
		Name:  "db",
		Usage: "鉴权数据库（auth.db）的迁移与维护",
		Commands: []*cli.Command{
			{
				Name:   "status",
				Usage:  "显示当前 schema 版本与待执行的迁移",
				Action: actionDBStatus,
			},
			{
				Name:  "migrate",
				Usage: "应用所有待执行的迁移",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "dry-run", Usage: "只打印将要执行什么，不改动数据库"},
					&cli.BoolFlag{Name: "no-backup", Usage: "跳过迁移前的自动备份"},
					&cli.BoolFlag{Name: "force", Usage: "跳过「服务正在运行」检查"},
				},
				Action: actionDBMigrate,
			},
			{
				Name:   "verify",
				Usage:  "一致性检查（integrity_check + foreign_key_check）",
				Action: actionDBVerify,
			},
			{
				Name:  "backup",
				Usage: "生成一份一致性备份",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "out", Usage: "备份文件路径（默认放在数据库同目录）"},
				},
				Action: actionDBBackup,
			},
			{
				Name:   "vacuum",
				Usage:  "回收空间（审计日志滚动淘汰后文件不会自动变小）",
				Action: actionDBVacuum,
			},
		},
	}
}

func authDBPath() string {
	return appconfig.Get().DatabasePath(cfgpkg.BaseDir)
}

// openAuthDB 打开数据库但**不**迁移。
// CLI 的每个子命令都要先看清楚现状再决定做什么，自动迁移会把这一步跳过去。
func openAuthDB() (*sql.DB, error) {
	return auth.Open(authDBPath())
}

func actionDBStatus(ctx context.Context, cmd *cli.Command) error {
	path := authDBPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("数据库尚未创建: %s\n", path)
		fmt.Printf("当前版本: 0\n待执行迁移: 全部 %d 个\n", auth.LatestVersion())
		return nil
	}

	db, err := openAuthDB()
	if err != nil {
		return err
	}
	defer db.Close()

	cur, err := auth.CurrentVersion(db)
	if err != nil {
		return err
	}
	fmt.Printf("数据库: %s\n", path)
	fmt.Printf("当前版本: %d\n本程序支持到: %d\n", cur, auth.LatestVersion())

	pending, err := auth.Pending(db)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		fmt.Println("待执行迁移: 无，已是最新")
		return nil
	}
	fmt.Println("待执行迁移:")
	for _, m := range pending {
		fmt.Printf("  %-4d %s\n", m.Version, m.Name)
	}
	fmt.Printf("共 %d 个迁移待执行。执行 asa-server db migrate 应用它们。\n", len(pending))
	return nil
}

func actionDBMigrate(ctx context.Context, cmd *cli.Command) error {
	path := authDBPath()
	dryRun := cmd.Bool("dry-run")

	// 服务运行时迁移，它的内存副本和 schema 会失配。宁可让用户多停一次服务。
	// 干跑不写任何东西，所以不受这条限制——用户正是想在服务还开着的时候
	// 先看看升级会做什么。
	if !dryRun && !cmd.Bool("force") {
		if port, running := apiServerRunning(); running {
			return fmt.Errorf("检测到 API 服务正在 %d 端口运行。\n"+
				"请先执行 asa-server service stop（或关闭 GUI）再迁移。\n"+
				"确认无误可加 --force 跳过此检查", port)
		}
	}

	db, err := openAuthDB()
	if err != nil {
		return err
	}
	defer db.Close()

	cur, err := auth.CurrentVersion(db)
	if err != nil {
		return err
	}
	pending, err := auth.Pending(db)
	if err != nil {
		return err
	}

	fmt.Printf("当前版本: %d\n", cur)
	if len(pending) == 0 {
		fmt.Println("已是最新，无需迁移。")
		return nil
	}
	fmt.Println("待执行迁移:")
	for _, m := range pending {
		fmt.Printf("  %-4d %s\n", m.Version, m.Name)
	}

	if dryRun {
		fmt.Printf("共 %d 个迁移待执行。未做任何改动（--dry-run）。\n", len(pending))
		return nil
	}

	if !cmd.Bool("no-backup") && cur > 0 {
		dst := auth.BackupName(path, cur, time.Now())
		if err := auth.Backup(db, dst); err != nil {
			return fmt.Errorf("迁移前备份失败（可用 --no-backup 跳过备份）: %w", err)
		}
		fmt.Printf("已备份至: %s\n", dst)
		if err := pruneBackups(path, 5); err != nil {
			fmt.Printf("清理旧备份时出错（不影响迁移）: %v\n", err)
		}
	}

	applied, err := auth.Migrate(db)
	for _, m := range applied {
		fmt.Printf("  ✓ %d %s\n", m.Version, m.Name)
	}
	if err != nil {
		return err
	}
	fmt.Printf("迁移完成，当前版本 %d。\n", auth.LatestVersion())
	notifyReload()
	return nil
}

func actionDBVerify(ctx context.Context, cmd *cli.Command) error {
	db, err := openAuthDB()
	if err != nil {
		return err
	}
	defer db.Close()

	report, ok, err := auth.Verify(db)
	if err != nil {
		return err
	}
	fmt.Print(report)
	if !ok {
		return fmt.Errorf("数据库存在一致性问题。可从备份恢复，或删除 auth.db 回到首次引导流程")
	}
	fmt.Println("检查通过。")
	return nil
}

func actionDBBackup(ctx context.Context, cmd *cli.Command) error {
	db, err := openAuthDB()
	if err != nil {
		return err
	}
	defer db.Close()

	dst := cmd.String("out")
	if dst == "" {
		cur, err := auth.CurrentVersion(db)
		if err != nil {
			return err
		}
		dst = auth.BackupName(authDBPath(), cur, time.Now())
	}
	if err := auth.Backup(db, dst); err != nil {
		return err
	}
	fmt.Printf("已备份至: %s\n", dst)
	return nil
}

func actionDBVacuum(ctx context.Context, cmd *cli.Command) error {
	db, err := openAuthDB()
	if err != nil {
		return err
	}
	defer db.Close()

	before := fileSize(authDBPath())
	if err := auth.Vacuum(db); err != nil {
		return err
	}
	after := fileSize(authDBPath())
	fmt.Printf("VACUUM 完成: %d KB -> %d KB\n", before/1024, after/1024)
	return nil
}

// pruneBackups 只保留最近 keep 份迁移备份
func pruneBackups(dbPath string, keep int) error {
	pattern := dbPath + ".bak-v*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	if len(matches) <= keep {
		return nil
	}
	// 文件名里的时间戳是 20060102T150405，字典序即时间序
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-keep] {
		if err := os.Remove(old); err != nil {
			return err
		}
		fmt.Printf("已删除旧备份: %s\n", filepath.Base(old))
	}
	return nil
}

// apiServerRunning 通过尝试连接本机端口判断 API 服务是否在跑。
// 比查进程名可靠：GUI、服务、命令行三种启动方式下进程名都一样。
func apiServerRunning() (int, bool) {
	port := appconfig.Get().Server.Port
	conn, err := net.DialTimeout("tcp",
		net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), 300*time.Millisecond)
	if err != nil {
		return port, false
	}
	conn.Close()
	return port, true
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}
