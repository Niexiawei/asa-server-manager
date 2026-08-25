package auth

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 用真实文件而不是 :memory:。WAL、busy_timeout、多进程访问这些行为
// 只有文件模式才有，内存库测不到。
func testDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := testDB(t)
	if _, err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func TestOpenEnablesForeignKeys(t *testing.T) {
	db, _ := testDB(t)
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Error("foreign_keys 必须开启，否则 ON DELETE CASCADE 是摆设")
	}
}

func TestOpenUsesWAL(t *testing.T) {
	db, _ := testDB(t)
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("journal_mode 应为 wal，实际 %q", mode)
	}
}

func TestOpenCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "auth.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open 应自动创建父目录: %v", err)
	}
	defer db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("数据库文件未创建: %v", err)
	}
}

func TestMigrateFromScratch(t *testing.T) {
	db, _ := testDB(t)

	v, err := CurrentVersion(db)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if v != 0 {
		t.Errorf("全新数据库版本应为 0，实际 %d", v)
	}

	applied, err := Migrate(db)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(applied) != len(migrations) {
		t.Errorf("应执行 %d 个迁移，实际 %d", len(migrations), len(applied))
	}

	v, _ = CurrentVersion(db)
	if v != LatestVersion() {
		t.Errorf("迁移后版本应为 %d，实际 %d", LatestVersion(), v)
	}

	for _, table := range []string{"users", "recovery_codes", "token_denylist", "login_failures", "audit_log"} {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("表 %s 未创建: %v", table, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := migratedDB(t)

	applied, err := Migrate(db)
	if err != nil {
		t.Fatalf("重复 Migrate 不应报错: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("重复 Migrate 不应执行任何迁移，实际执行了 %d 个", len(applied))
	}
}

// 数据库版本高于程序已知版本时必须拒绝运行：用新表结构配旧代码
// 会静默写坏数据，比直接报错难查得多。
func TestMigrateRefusesDowngrade(t *testing.T) {
	db := migratedDB(t)

	if _, err := db.Exec("UPDATE schema_version SET version = ?", LatestVersion()+5); err != nil {
		t.Fatalf("构造降级场景失败: %v", err)
	}

	_, err := Pending(db)
	if err == nil {
		t.Fatal("版本超前时 Pending 应报错")
	}
	if !errors.Is(err, ErrSchemaAhead) {
		t.Errorf("错误应可用 errors.Is 匹配 ErrSchemaAhead，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "降级") {
		t.Errorf("错误信息应提示用户程序被降级了，实际 %q", err)
	}

	if _, err := Migrate(db); err == nil {
		t.Error("版本超前时 Migrate 应报错")
	}
	// 关键：报错的同时不得有任何写入
	var v int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&v); err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}
	if v != LatestVersion()+5 {
		t.Errorf("拒绝迁移时不应改动版本号，实际变成了 %d", v)
	}
}

// 迁移失败必须整体回滚，数据库停在上一个完整版本，不能半迁移。
func TestMigrateRollsBackOnFailure(t *testing.T) {
	db, _ := testDB(t)

	orig := migrations
	t.Cleanup(func() { migrations = orig })
	migrations = append(append([]Migration{}, orig...), Migration{
		Version: 9998,
		Name:    "creates_table_then_fails",
		Up: func(tx *sql.Tx) error {
			if _, err := tx.Exec(`CREATE TABLE should_not_survive (x INTEGER)`); err != nil {
				return err
			}
			return errors.New("故意失败")
		},
	})

	if _, err := Migrate(db); err == nil {
		t.Fatal("迁移应报错")
	}

	v, _ := CurrentVersion(db)
	if v != orig[len(orig)-1].Version {
		t.Errorf("失败后版本应停在 %d，实际 %d", orig[len(orig)-1].Version, v)
	}

	var name string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='should_not_survive'").Scan(&name)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Error("失败迁移里建的表必须被回滚掉，SQLite 的 DDL 是事务性的")
	}
}

func TestForeignKeyCascade(t *testing.T) {
	db := migratedDB(t)

	res, err := db.Exec(
		`INSERT INTO users(username, username_lower, password_hash, created_at) VALUES('bob','bob','x',1)`)
	if err != nil {
		t.Fatalf("插入用户失败: %v", err)
	}
	uid, _ := res.LastInsertId()

	if _, err := db.Exec(
		`INSERT INTO recovery_codes(user_id, code_hash) VALUES(?, 'h')`, uid); err != nil {
		t.Fatalf("插入恢复码失败: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id = ?`, uid); err != nil {
		t.Fatalf("删除用户失败: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ?`, uid).Scan(&n); err != nil {
		t.Fatalf("统计恢复码失败: %v", err)
	}
	if n != 0 {
		t.Errorf("删用户后恢复码应被级联删除，仍剩 %d 条（PRAGMA foreign_keys 没生效？）", n)
	}
}

func TestUsernameLowerIsUnique(t *testing.T) {
	db := migratedDB(t)

	if _, err := db.Exec(
		`INSERT INTO users(username, username_lower, password_hash, created_at) VALUES('Admin','admin','x',1)`); err != nil {
		t.Fatalf("插入首个用户失败: %v", err)
	}
	_, err := db.Exec(
		`INSERT INTO users(username, username_lower, password_hash, created_at) VALUES('ADMIN','admin','y',2)`)
	if err == nil {
		t.Error("同名（忽略大小写）用户应被 UNIQUE 约束拒绝")
	}
}

func TestVerifyOnHealthyDB(t *testing.T) {
	db := migratedDB(t)

	report, ok, err := Verify(db)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Errorf("健康的数据库应通过检查，报告:\n%s", report)
	}
	if !strings.Contains(report, "integrity_check: ok") {
		t.Errorf("报告应包含 integrity_check 结果，实际:\n%s", report)
	}
}

func TestBackupProducesUsableCopy(t *testing.T) {
	db, path := testDB(t)
	if _, err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO users(username, username_lower, password_hash, created_at) VALUES('bob','bob','x',1)`); err != nil {
		t.Fatalf("插入用户失败: %v", err)
	}

	dst := BackupName(path, LatestVersion(), time.Now())
	if err := Backup(db, dst); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// 备份必须是一个能直接打开、内容完整的库
	bak, err := Open(dst)
	if err != nil {
		t.Fatalf("打开备份失败: %v", err)
	}
	defer bak.Close()

	var name string
	if err := bak.QueryRow(`SELECT username FROM users WHERE username_lower='bob'`).Scan(&name); err != nil {
		t.Fatalf("备份里读不到数据: %v", err)
	}
	if name != "bob" {
		t.Errorf("备份数据不对: %q", name)
	}
	if _, ok, _ := Verify(bak); !ok {
		t.Error("备份应通过一致性检查")
	}

	// 不覆盖已存在的备份
	if err := Backup(db, dst); err == nil {
		t.Error("目标已存在时 Backup 应报错")
	}
}

func TestBackupName(t *testing.T) {
	at := time.Date(2026, 7, 28, 14, 30, 0, 0, time.UTC)
	got := BackupName(`C:/asa/auth.db`, 3, at)
	want := `C:/asa/auth.db.bak-v3-20260728T143000`
	if got != want {
		t.Errorf("BackupName = %q，期望 %q", got, want)
	}
}

// m003 删掉了 users.webauthn_handle 上的唯一索引。这不是清洁度问题：
// 该列的默认值是空字节串，移除 WebAuthn 后 CreateUser 不再写入 handle，
// 于是所有新用户的该列都相同。唯一索引若还在，**第二个用户就建不出来**。
// 这条用例把这个约束钉死，避免以后有人"顺手"把 m003 改回去。
func TestM003DropsWebAuthnArtifacts(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_users_handle'`,
	).Scan(&n); err != nil {
		t.Fatalf("查询索引失败: %v", err)
	}
	if n != 0 {
		t.Error("idx_users_handle 应已被 m003 删除，否则第二个用户会撞 UNIQUE 约束")
	}

	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='webauthn_credentials'`,
	).Scan(&n); err != nil {
		t.Fatalf("查询表失败: %v", err)
	}
	if n != 0 {
		t.Error("webauthn_credentials 表应已被 m003 删除")
	}

	// 真正要防的回归：连续建两个用户不能报 UNIQUE 冲突
	if _, err := CreateUser(ctx, db, "alice", "hash-a", RoleAdmin); err != nil {
		t.Fatalf("创建第一个用户失败: %v", err)
	}
	if _, err := CreateUser(ctx, db, "bob", "hash-b", RoleOperator); err != nil {
		t.Fatalf("创建第二个用户失败（idx_users_handle 很可能还在）: %v", err)
	}
}

// 真实升级路径：一个已经跑到 v2、里面有用户和 Passkey 凭证的老库，
// 升到 v3 之后必须能正常用。上一条用例覆盖的是全新安装（v1->v3 一次跑完），
// 两者走的代码路径不同——老库里 idx_users_handle 上已经有非空的真实数据。
func TestM003UpgradesExistingV2Database(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()

	// 手工把库构造成 v2 状态：只跑 m001 + m002
	if err := ensureVersionTable(db); err != nil {
		t.Fatalf("ensureVersionTable: %v", err)
	}
	for _, m := range []Migration{migrations[0], migrations[1]} {
		if err := applyOne(db, m); err != nil {
			t.Fatalf("构造 v2 库失败（%s）: %v", m.Name, err)
		}
	}
	if v, err := CurrentVersion(db); err != nil || v != 2 {
		t.Fatalf("构造后版本应为 2，实际 %d (err=%v)", v, err)
	}

	// 老库里有两个各自持有 handle 的用户，其中一个还绑了 Passkey
	for i, name := range []string{"olduser1", "olduser2"} {
		h, err := newWebAuthnHandle()
		if err != nil {
			t.Fatalf("生成 handle: %v", err)
		}
		if _, err := db.Exec(
			`INSERT INTO users(username, username_lower, password_hash, role, webauthn_handle, created_at)
			 VALUES(?, ?, ?, ?, ?, ?)`,
			name, name, "hash", RoleOperator, h, time.Now().Unix()); err != nil {
			t.Fatalf("插入老用户 %d: %v", i, err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO webauthn_credentials(user_id, rp_id, credential_id, public_key, created_at)
		 VALUES(1, 'localhost', x'aabb', x'ccdd', ?)`, time.Now().Unix()); err != nil {
		t.Fatalf("插入老凭证: %v", err)
	}

	applied, err := Migrate(db)
	if err != nil {
		t.Fatalf("从 v2 升级失败: %v", err)
	}
	if len(applied) != 1 || applied[0].Version != 3 {
		t.Fatalf("应只应用 m003，实际 %+v", applied)
	}

	// 老用户还在，且能用密码登录所需的数据完好
	var cnt int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&cnt); err != nil || cnt != 2 {
		t.Errorf("升级不得丢用户，实际 %d 个 (err=%v)", cnt, err)
	}
	if u, err := GetUser(ctx, db, "olduser1"); err != nil || u.PasswordHash != "hash" {
		t.Errorf("老用户应可正常读取，err=%v", err)
	}

	// 索引已消失，因此还能继续建新用户
	if _, err := CreateUser(ctx, db, "newuser", "hash-n", RoleOperator); err != nil {
		t.Fatalf("升级后创建新用户失败: %v", err)
	}
}
