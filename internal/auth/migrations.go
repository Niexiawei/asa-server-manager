package auth

import (
	"crypto/rand"
	"database/sql"
	"fmt"
)

// migrations 必须按 Version 升序排列，且只允许在末尾追加。
//
// 已发布的迁移函数一律不再改动——跑过它的数据库不会再跑一次，
// 改了只会让新装和老装的库结构悄悄分叉，这类问题极难排查。
var migrations = []Migration{
	{Version: 1, Name: "initial_schema", Up: m001InitialSchema},
	{Version: 2, Name: "webauthn_credentials", Up: m002WebAuthn},
	{Version: 3, Name: "drop_webauthn", Up: m003DropWebAuthn},
}

func m001InitialSchema(tx *sql.Tx) error {
	stmts := []string{
		// username_lower 的 UNIQUE 约束把"用户名大小写不敏感去重"交给数据库，
		// 而不是靠每个调用方自己记得先转小写再查一遍。
		`CREATE TABLE users (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			username        TEXT    NOT NULL,
			username_lower  TEXT    NOT NULL UNIQUE,
			password_hash   TEXT    NOT NULL,
			role            TEXT    NOT NULL DEFAULT 'operator',
			session_version INTEGER NOT NULL DEFAULT 1,
			totp_enabled    INTEGER NOT NULL DEFAULT 0,
			totp_secret     TEXT    NOT NULL DEFAULT '',
			totp_last_step  INTEGER NOT NULL DEFAULT 0,
			disabled        INTEGER NOT NULL DEFAULT 0,
			created_at      INTEGER NOT NULL,
			last_login_at   INTEGER NOT NULL DEFAULT 0
		)`,

		// 用掉的恢复码置 used_at 而不是删行，便于事后审计"这个码是什么时候被用掉的"
		`CREATE TABLE recovery_codes (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			code_hash TEXT    NOT NULL,
			used_at   INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX idx_recovery_user ON recovery_codes(user_id)`,

		// 单设备登出：吊销单个 jti 直到它自然过期。
		// 全设备登出走 users.session_version++，不经过这张表。
		`CREATE TABLE token_denylist (
			jti        TEXT    PRIMARY KEY,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE INDEX idx_denylist_exp ON token_denylist(expires_at)`,

		// 登录失败计数必须落库。放内存的话，攻击者只要等一次服务重启
		// （更新、崩溃恢复、手动重启在 Windows 服务上都是常态）计数就清零了，
		// 锁定机制形同虚设。
		`CREATE TABLE login_failures (
			scope        TEXT    NOT NULL,
			key          TEXT    NOT NULL,
			fail_count   INTEGER NOT NULL DEFAULT 0,
			first_fail   INTEGER NOT NULL,
			locked_until INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (scope, key)
		)`,

		`CREATE TABLE audit_log (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			ts         INTEGER NOT NULL,
			event      TEXT    NOT NULL,
			username   TEXT    NOT NULL DEFAULT '',
			actor      TEXT    NOT NULL DEFAULT '',
			client_ip  TEXT    NOT NULL DEFAULT '',
			user_agent TEXT    NOT NULL DEFAULT '',
			detail     TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX idx_audit_ts ON audit_log(ts)`,
		`CREATE INDEX idx_audit_user ON audit_log(username, ts)`,
	}

	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func m002WebAuthn(tx *sql.Tx) error {
	stmts := []string{
		// WebAuthn 的 user handle。规范要求它是**随机且不含 PII** 的值：
		// 它会被存进认证器，discoverable 登录时还会回传，
		// 用用户名或自增 id 等于把账户标识泄露给认证器和它背后的同步云。
		`ALTER TABLE users ADD COLUMN webauthn_handle BLOB NOT NULL DEFAULT x''`,

		// rp_id 这一列是关键：凭证绑定在具体域名上，在 localhost 注册的
		// Passkey 在 ark.example.com 上用不了，同一个人可能需要各注册一次。
		`CREATE TABLE webauthn_credentials (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			rp_id            TEXT    NOT NULL,
			credential_id    BLOB    NOT NULL,
			public_key       BLOB    NOT NULL,
			attestation_type TEXT    NOT NULL DEFAULT '',
			aaguid           BLOB,
			sign_count       INTEGER NOT NULL DEFAULT 0,
			transports       TEXT    NOT NULL DEFAULT '[]',
			flags_uv         INTEGER NOT NULL DEFAULT 0,
			flags_backup_eligible INTEGER NOT NULL DEFAULT 0,
			flags_backup_state    INTEGER NOT NULL DEFAULT 0,
			attachment       TEXT    NOT NULL DEFAULT '',
			name             TEXT    NOT NULL DEFAULT '',
			clone_warned     INTEGER NOT NULL DEFAULT 0,
			created_at       INTEGER NOT NULL,
			last_used_at     INTEGER NOT NULL DEFAULT 0
		)`,
		// 同一个认证器不能被注册到两个账户上
		`CREATE UNIQUE INDEX idx_wa_credid ON webauthn_credentials(rp_id, credential_id)`,
		`CREATE INDEX idx_wa_user ON webauthn_credentials(user_id)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}

	// 给存量用户补上随机 handle。留空的话，discoverable 登录会因为
	// 多个用户的 handle 都是空字节串而互相串号。
	rows, err := tx.Query(`SELECT id FROM users WHERE length(webauthn_handle) = 0`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range ids {
		h, err := newWebAuthnHandle()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE users SET webauthn_handle = ? WHERE id = ?`, h, id); err != nil {
			return err
		}
	}
	// 唯一索引要在补完值之后建，否则空 handle 会互相冲突
	if _, err := tx.Exec(`CREATE UNIQUE INDEX idx_users_handle ON users(webauthn_handle)`); err != nil {
		return err
	}
	return nil
}

// newWebAuthnHandle 生成一个 32 字节随机 user handle。
//
// WebAuthn 功能已移除，这个函数只剩 m002 一个调用方——m002 是已发布的迁移，
// 从 v1 升上来的库仍要原样跑一遍，所以它依赖的东西必须留着。
// 原先它在 auth/webauthn.go 里叫 NewWebAuthnHandle，随该文件一起删除，
// 这里保留一份未导出的实现，避免 m002 的行为随功能移除而改变。
func newWebAuthnHandle() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("生成 WebAuthn handle 失败: %w", err)
	}
	return b, nil
}

// m003DropWebAuthn 移除 WebAuthn 功能留下的表与索引。
//
// ⚠️ idx_users_handle 必须一起删，这不是清洁度问题而是功能问题。m002 建的是：
//
//	webauthn_handle BLOB NOT NULL DEFAULT x''
//	CREATE UNIQUE INDEX idx_users_handle ON users(webauthn_handle)
//
// 移除 WebAuthn 后 CreateUser 不再生成 handle，新用户的该列全部落在默认的空字节串上，
// 留着这个唯一索引会让**第二个用户创建时撞 UNIQUE 约束**。
//
// webauthn_handle 列本身保留：SQLite 删列要重建整张表，风险远大于收益，
// 留一个没人读的 BLOB 列无害（读取侧已从 userColumns 里去掉）。
// idx_wa_credid / idx_wa_user 建在 webauthn_credentials 上，随表一起消失，无需单独 DROP。
func m003DropWebAuthn(tx *sql.Tx) error {
	stmts := []string{
		`DROP INDEX IF EXISTS idx_users_handle`,
		`DROP TABLE IF EXISTS webauthn_credentials`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}
