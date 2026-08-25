// Package auth 实现鉴权领域逻辑：用户、密码、会话令牌、两步验证、
// 登录限流与审计。
//
// 本包不依赖 gin —— HTTP 接入层在 webapi/authapi。
//
// 存储用 SQLite（{BaseDir}/database_file/auth.db），且**只用于鉴权**。
// 项目其余部分的持久化（Badger 状态库、schedules.json、实例 INI）一概不动。
// 判断某张表属不属于这里的标准很简单：如果它在 auth.enabled 为 false 时
// 仍然需要被写入，那它就不该放进 auth.db。
package auth

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	// 纯 Go 的 SQLite 驱动，无需 cgo。
	// 它已经因为 go-arkparser 解析 .ark 存档而存在于依赖图中，这里复用它不增加二进制体积。
	_ "modernc.org/sqlite"
)

// Open 打开（必要时创建）鉴权数据库。
//
// 不在这里执行迁移：迁移有自己的备份、干跑、降级保护流程，
// 由 Migrate 或 CLI 的 `asa-server db migrate` 显式驱动。
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("打开鉴权数据库失败: %w", err)
	}

	// 鉴权库的并发量极低（登录、令牌校验都走内存副本，很少真的落到 SQL）。
	// 限制成单连接可以彻底规避 SQLITE_BUSY 和写锁竞争，也省掉自己实现 busy 重试。
	// 不要为了"性能"把这里调大——真正的读路径根本不查库。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接鉴权数据库失败: %w", err)
	}
	// PRAGMA 在 DSN 里已声明，但 foreign_keys 值得单独确认一次：
	// 它在 SQLite 里默认是关的，一旦没生效，删用户会留下孤儿凭证和孤儿恢复码，
	// 而且这种错误不会报任何异常，只会在很久以后表现为数据不一致。
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		db.Close()
		return nil, fmt.Errorf("查询 foreign_keys 失败: %w", err)
	}
	if fk != 1 {
		db.Close()
		return nil, fmt.Errorf("外键约束未生效（PRAGMA foreign_keys = %d）", fk)
	}

	return db, nil
}

func dsn(path string) string {
	// modernc 的驱动按第一个 '?' 切分路径与参数，Windows 路径里的反斜杠
	// 统一换成正斜杠更保险。
	var b strings.Builder
	b.WriteString(filepath.ToSlash(path))
	b.WriteString("?_pragma=journal_mode(WAL)")   // 读写不互斥，异常退出后可自恢复
	b.WriteString("&_pragma=busy_timeout(5000)")  // 遇锁等 5 秒，而不是立刻 SQLITE_BUSY
	b.WriteString("&_pragma=foreign_keys(ON)")    // 让 ON DELETE CASCADE 真正生效
	b.WriteString("&_pragma=synchronous(NORMAL)") // WAL 下足够安全，避免每次提交都 fsync
	return b.String()
}

// Verify 做一致性检查，对应 CLI 的 `asa-server db verify`。
// 返回的字符串是给人看的检查报告，err 非 nil 表示检查本身没能跑完。
func Verify(db *sql.DB) (report string, ok bool, err error) {
	var b strings.Builder
	ok = true

	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return "", false, fmt.Errorf("integrity_check 执行失败: %w", err)
	}
	if integrity == "ok" {
		b.WriteString("integrity_check: ok\n")
	} else {
		ok = false
		fmt.Fprintf(&b, "integrity_check: %s\n", integrity)
	}

	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return "", false, fmt.Errorf("foreign_key_check 执行失败: %w", err)
	}
	defer rows.Close()

	violations := 0
	for rows.Next() {
		var table, parent sql.NullString
		var rowid, fkid sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return "", false, fmt.Errorf("读取 foreign_key_check 结果失败: %w", err)
		}
		violations++
		fmt.Fprintf(&b, "外键违规: 表 %s rowid %d -> %s\n",
			table.String, rowid.Int64, parent.String)
	}
	if err := rows.Err(); err != nil {
		return "", false, fmt.Errorf("读取 foreign_key_check 结果失败: %w", err)
	}
	if violations == 0 {
		b.WriteString("foreign_key_check: ok\n")
	} else {
		ok = false
	}

	return b.String(), ok, nil
}

// Backup 用 SQLite 原生的 VACUUM INTO 做一致性备份。
//
// 比直接复制 .db 文件安全：WAL 模式下文件里可能还有未合并的事务，
// 单独复制主库文件会得到一个缺数据的副本。
func Backup(db *sql.DB, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("备份目标已存在: %s", dst)
	}
	if dir := filepath.Dir(dst); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建备份目录失败: %w", err)
		}
	}
	// VACUUM INTO 不接受参数占位符，只能拼接。单引号转义即可——
	// 路径由调用方构造，不来自用户输入。
	quoted := strings.ReplaceAll(filepath.ToSlash(dst), "'", "''")
	if _, err := db.Exec("VACUUM INTO '" + quoted + "'"); err != nil {
		return fmt.Errorf("备份失败: %w", err)
	}
	return nil
}

// Vacuum 回收空间。审计日志滚动淘汰后文件不会自动变小，需要显式调用。
func Vacuum(db *sql.DB) error {
	if _, err := db.Exec("VACUUM"); err != nil {
		return fmt.Errorf("VACUUM 失败: %w", err)
	}
	return nil
}
