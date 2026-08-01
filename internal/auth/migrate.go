package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Migration 是一次 schema 变更。
//
// 规矩：只往 migrations 末尾追加，**永远不修改已发布的迁移函数**。
// 已经跑过它的数据库不会再跑第二次，改了只会让新旧库的结构悄悄分叉。
type Migration struct {
	Version int
	Name    string
	Up      func(*sql.Tx) error
}

// ErrSchemaAhead 表示数据库的版本比当前二进制知道的还新，通常是程序被降级了。
var ErrSchemaAhead = errors.New("数据库 schema 版本高于本程序支持的版本")

// LatestVersion 返回本程序已知的最高 schema 版本
func LatestVersion() int {
	if len(migrations) == 0 {
		return 0
	}
	return migrations[len(migrations)-1].Version
}

// CurrentVersion 读取数据库当前的 schema 版本。全新的库返回 0。
func CurrentVersion(db *sql.DB) (int, error) {
	if err := ensureVersionTable(db); err != nil {
		return 0, err
	}
	var v int
	err := db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取 schema 版本失败: %w", err)
	}
	return v, nil
}

// Pending 返回尚未应用的迁移。
//
// 数据库版本高于程序已知版本时返回 ErrSchemaAhead：此时**绝不能**继续，
// 用新表结构配旧代码会静默写坏数据，而且往往过很久才暴露出来。
func Pending(db *sql.DB) ([]Migration, error) {
	cur, err := CurrentVersion(db)
	if err != nil {
		return nil, err
	}
	if cur > LatestVersion() {
		return nil, fmt.Errorf("%w：数据库为 %d，本程序支持到 %d。"+
			"这通常意味着 asa-server.exe 被降级了，请换回较新的版本，或从备份恢复 auth.db",
			ErrSchemaAhead, cur, LatestVersion())
	}

	var out []Migration
	for _, m := range migrations {
		if m.Version > cur {
			out = append(out, m)
		}
	}
	return out, nil
}

// Migrate 应用所有待执行的迁移，返回实际执行了哪些。
//
// 每个迁移单独一个事务。SQLite 的 DDL 是事务性的，所以某个迁移失败时
// 只回滚它自己，数据库停留在上一个**完整**版本，不会出现半迁移状态。
func Migrate(db *sql.DB) ([]Migration, error) {
	pending, err := Pending(db)
	if err != nil {
		return nil, err
	}

	applied := make([]Migration, 0, len(pending))
	for _, m := range pending {
		if err := applyOne(db, m); err != nil {
			return applied, fmt.Errorf("迁移 %d (%s) 失败: %w", m.Version, m.Name, err)
		}
		applied = append(applied, m)
	}
	return applied, nil
}

func applyOne(db *sql.DB, m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // 已提交时是 no-op

	if err := m.Up(tx); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM schema_version"); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO schema_version(version) VALUES(?)", m.Version); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureVersionTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`)
	if err != nil {
		return fmt.Errorf("创建 schema_version 表失败: %w", err)
	}
	return nil
}

// BackupName 生成迁移前备份的文件名，形如 auth.db.bak-v3-20260728T143000
func BackupName(dbPath string, version int, at time.Time) string {
	return fmt.Sprintf("%s.bak-v%d-%s", dbPath, version, at.Format("20060102T150405"))
}
