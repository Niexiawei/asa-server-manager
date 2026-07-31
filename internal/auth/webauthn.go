package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"asa-server/appconfig"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

var (
	ErrWebAuthnUnavailable = errors.New("当前访问地址不支持 Passkey")
	ErrCredentialExists    = errors.New("该认证器已被注册")
	ErrCredentialNotFound  = errors.New("凭证不存在")
)

// NewWebAuthnHandle 生成一个 WebAuthn user handle。
//
// 规范要求它是随机且不含 PII 的：这个值会被存进认证器，
// discoverable 登录时还会回传，用用户名或自增 id 等于把账户标识
// 泄露给认证器和它背后的同步云。
func NewWebAuthnHandle() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("生成 WebAuthn handle 失败: %w", err)
	}
	return b, nil
}

// Credential 是一条已注册的 WebAuthn 凭证
type Credential struct {
	ID              int64     `json:"id"`
	UserID          int64     `json:"-"`
	RPID            string    `json:"rp_id"`
	CredentialID    []byte    `json:"-"`
	PublicKey       []byte    `json:"-"`
	AttestationType string    `json:"-"`
	AAGUID          []byte    `json:"-"`
	SignCount       uint32    `json:"-"`
	Transports      []string  `json:"transports,omitempty"`
	UserVerified    bool      `json:"user_verified"`
	BackupEligible  bool      `json:"backup_eligible"`
	BackupState     bool      `json:"backup_state"`
	Attachment      string    `json:"attachment,omitempty"`
	Name            string    `json:"name"`
	CloneWarned     bool      `json:"clone_warned"`
	CreatedAt       time.Time `json:"created_at"`
	LastUsedAt      time.Time `json:"last_used_at,omitempty"`
}

// ---- WebAuthn 实例缓存 ----

var (
	waCacheMu sync.RWMutex
	waCache   = map[string]*webauthn.WebAuthn{}
)

// InstanceFor 返回某个 RP ID 对应的 WebAuthn 实例。
//
// webauthn.Config 的 RPID 是单值，而本项目要同时支持 localhost 与反代域名，
// 所以按 RP ID 建实例并缓存。
func InstanceFor(rpID string) (*webauthn.WebAuthn, error) {
	waCacheMu.RLock()
	w, ok := waCache[rpID]
	waCacheMu.RUnlock()
	if ok {
		return w, nil
	}

	cfg := appconfig.Get()
	wcfg := cfg.Auth.WebAuthn

	w, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: wcfg.RPDisplayName,
		RPOrigins: OriginsFor(rpID, wcfg.Domains, cfg.Server.Port,
			cfg.Server.TLS.Enabled, wcfg.ExtraOrigins),
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      residentKeyRequirement(wcfg),
			UserVerification: userVerification(wcfg),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("初始化 WebAuthn 失败: %w", err)
	}

	waCacheMu.Lock()
	waCache[rpID] = w
	waCacheMu.Unlock()
	return w, nil
}

// ResetWebAuthnCache 在配置热重载后调用。
// 不清的话，改了 domains 或 rp_display_name 之后仍会用着旧实例。
func ResetWebAuthnCache() {
	waCacheMu.Lock()
	waCache = map[string]*webauthn.WebAuthn{}
	waCacheMu.Unlock()
}

func residentKeyRequirement(cfg appconfig.WebAuthnConfig) protocol.ResidentKeyRequirement {
	if cfg.DiscoverableLogin {
		// 「使用 Passkey 登录」（不输用户名）需要认证器里有驻留密钥，
		// 否则浏览器弹窗里会是空的
		return protocol.ResidentKeyRequirementRequired
	}
	return protocol.ResidentKeyRequirementPreferred
}

func userVerification(cfg appconfig.WebAuthnConfig) protocol.UserVerificationRequirement {
	switch cfg.UserVerification {
	case "discouraged":
		return protocol.VerificationDiscouraged
	case "preferred":
		return protocol.VerificationPreferred
	default:
		return protocol.VerificationRequired
	}
}

// ---- webauthn.User 实现 ----

type webAuthnUser struct {
	user  *User
	creds []webauthn.Credential
}

func (w *webAuthnUser) WebAuthnID() []byte          { return w.user.WebAuthnHandle }
func (w *webAuthnUser) WebAuthnName() string        { return w.user.Username }
func (w *webAuthnUser) WebAuthnDisplayName() string { return w.user.Username }
func (w *webAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return w.creds
}

func (w *webAuthnUser) Descriptors() []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, 0, len(w.creds))
	for _, c := range w.creds {
		out = append(out, c.Descriptor())
	}
	return out
}

// NewWebAuthnUser 组装出 go-webauthn 需要的 User，只带当前 RP ID 下的凭证。
// 按 RP ID 过滤是必须的：其它域名下的凭证在这里用不了，
// 塞进 allowCredentials 只会让浏览器提示一堆不可用的选项。
func (m *Manager) NewWebAuthnUser(ctx context.Context, u *User, rpID string) (*webAuthnUser, error) {
	creds, err := ListCredentials(ctx, m.db, u.ID, rpID)
	if err != nil {
		return nil, err
	}
	wc := make([]webauthn.Credential, 0, len(creds))
	for _, c := range creds {
		wc = append(wc, c.toWebAuthn())
	}
	return &webAuthnUser{user: u, creds: wc}, nil
}

func (c *Credential) toWebAuthn() webauthn.Credential {
	transports := make([]protocol.AuthenticatorTransport, 0, len(c.Transports))
	for _, t := range c.Transports {
		transports = append(transports, protocol.AuthenticatorTransport(t))
	}
	return webauthn.Credential{
		ID:              c.CredentialID,
		PublicKey:       c.PublicKey,
		AttestationType: c.AttestationType,
		Transport:       transports,
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			UserVerified:   c.UserVerified,
			BackupEligible: c.BackupEligible,
			BackupState:    c.BackupState,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:       c.AAGUID,
			SignCount:    c.SignCount,
			CloneWarning: c.CloneWarned,
			Attachment:   protocol.AuthenticatorAttachment(c.Attachment),
		},
	}
}

// ---- 凭证存储 ----

const credColumns = `id, user_id, rp_id, credential_id, public_key, attestation_type, aaguid,
	sign_count, transports, flags_uv, flags_backup_eligible, flags_backup_state,
	attachment, name, clone_warned, created_at, last_used_at`

// ListCredentials 列出某用户在某 RP ID 下的凭证。rpID 为空表示不限。
func ListCredentials(ctx context.Context, q queryer, userID int64, rpID string) ([]*Credential, error) {
	query := `SELECT ` + credColumns + ` FROM webauthn_credentials WHERE user_id = ?`
	args := []any{userID}
	if rpID != "" {
		query += ` AND rp_id = ?`
		args = append(args, rpID)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询凭证失败: %w", err)
	}
	defer rows.Close()

	var out []*Credential
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanCredential(rows interface {
	Scan(...any) error
}) (*Credential, error) {
	var c Credential
	var transports string
	var createdAt, lastUsed int64
	err := rows.Scan(&c.ID, &c.UserID, &c.RPID, &c.CredentialID, &c.PublicKey,
		&c.AttestationType, &c.AAGUID, &c.SignCount, &transports,
		&c.UserVerified, &c.BackupEligible, &c.BackupState,
		&c.Attachment, &c.Name, &c.CloneWarned, &createdAt, &lastUsed)
	if err != nil {
		return nil, fmt.Errorf("读取凭证失败: %w", err)
	}
	_ = json.Unmarshal([]byte(transports), &c.Transports)
	c.CreatedAt = time.Unix(createdAt, 0)
	if lastUsed > 0 {
		c.LastUsedAt = time.Unix(lastUsed, 0)
	}
	return &c, nil
}

// SaveCredential 保存一条新注册的凭证
func (m *Manager) SaveCredential(ctx context.Context, userID int64, rpID, name string, c *webauthn.Credential) error {
	transports := make([]string, 0, len(c.Transport))
	for _, t := range c.Transport {
		transports = append(transports, string(t))
	}
	tj, _ := json.Marshal(transports)

	_, err := m.db.ExecContext(ctx,
		`INSERT INTO webauthn_credentials(
			user_id, rp_id, credential_id, public_key, attestation_type, aaguid,
			sign_count, transports, flags_uv, flags_backup_eligible, flags_backup_state,
			attachment, name, created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		userID, rpID, c.ID, c.PublicKey, c.AttestationType, c.Authenticator.AAGUID,
		c.Authenticator.SignCount, string(tj), c.Flags.UserVerified,
		c.Flags.BackupEligible, c.Flags.BackupState,
		string(c.Authenticator.Attachment), name, time.Now().Unix())
	if err != nil {
		// UNIQUE(rp_id, credential_id) 挡住"同一个认证器注册到两个账户"
		if isUniqueViolation(err) {
			return ErrCredentialExists
		}
		return fmt.Errorf("保存凭证失败: %w", err)
	}
	return nil
}

// UpdateCredentialUsage 在每次成功登录后写回签名计数与最后使用时间。
//
// 这是 webauthn_credentials 表**每次登录都要写**的原因，也是当初判断
// "凭证是关系型数据、不该塞进 JSON 文件"的具体依据。
func (m *Manager) UpdateCredentialUsage(ctx context.Context, rpID string, credID []byte, signCount uint32, cloneWarned bool) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE webauthn_credentials
		 SET sign_count = ?, last_used_at = ?, clone_warned = clone_warned OR ?
		 WHERE rp_id = ? AND credential_id = ?`,
		signCount, time.Now().Unix(), cloneWarned, rpID, credID)
	return err
}

// RenameCredential 改名
func (m *Manager) RenameCredential(ctx context.Context, userID, credID int64, name string) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE webauthn_credentials SET name = ? WHERE id = ? AND user_id = ?`,
		name, credID, userID)
	if err != nil {
		return fmt.Errorf("重命名凭证失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrCredentialNotFound
	}
	return nil
}

// DeleteCredential 删除凭证。
//
// 不需要"不能删最后一个"之类的检查：密码始终可用，删光了也只是退回密码登录。
// 这是把 WebAuthn 限定为补充带来的直接简化。
func (m *Manager) DeleteCredential(ctx context.Context, userID, credID int64) error {
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?`, credID, userID)
	if err != nil {
		return fmt.Errorf("删除凭证失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrCredentialNotFound
	}
	return nil
}

// DeleteAllCredentials 清空某用户的全部凭证（管理员救援用）
func (m *Manager) DeleteAllCredentials(ctx context.Context, username, actor string) error {
	u, ok := m.Lookup(username)
	if !ok {
		return ErrUserNotFound
	}
	if _, err := m.db.ExecContext(ctx,
		`DELETE FROM webauthn_credentials WHERE user_id = ?`, u.ID); err != nil {
		return fmt.Errorf("清空凭证失败: %w", err)
	}
	m.Audit(ctx, AuditEntry{Event: EventCredDelete, Username: username, Actor: actor, Detail: "清空全部 Passkey"})
	return nil
}

// FindUserByHandle 按 WebAuthn user handle 查用户，供 discoverable 登录使用
func (m *Manager) FindUserByHandle(ctx context.Context, handle []byte) (*User, error) {
	row := m.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE webauthn_handle = ?`, handle)
	return scanUser(row)
}

// SignCountFor 返回库里记录的签名计数，用于与本次断言的计数比较
func (m *Manager) SignCountFor(rpID string, credID []byte) uint32 {
	var n uint32
	_ = m.db.QueryRow(
		`SELECT sign_count FROM webauthn_credentials WHERE rp_id = ? AND credential_id = ?`,
		rpID, credID).Scan(&n)
	return n
}

// DeleteCredentialByCredID 按认证器上报的凭证 ID 删除，
// 用于 clone_detection = disable_credential 时停用可疑凭证
func (m *Manager) DeleteCredentialByCredID(ctx context.Context, rpID string, credID []byte) error {
	_, err := m.db.ExecContext(ctx,
		`DELETE FROM webauthn_credentials WHERE rp_id = ? AND credential_id = ?`, rpID, credID)
	return err
}

// FinishWebAuthnLogin 收尾：记录最后登录时间、清掉登录失败计数、刷新内存副本
func (m *Manager) FinishWebAuthnLogin(ctx context.Context, u *User) {
	_ = TouchLastLogin(ctx, m.db, u.Username)
	_ = ClearFailures(ctx, m.db, ScopeUser, u.Username)
	_ = m.Reload(ctx)
}

// ShouldTreatAsClone 按配置决定如何处理疑似克隆的认证器。
//
// 注意：很多认证器（尤其 iCloud / Google 同步的 passkey）**按设计**恒返回 0，
// 把 0/0 当成异常会导致这些用户根本登不上去。
func ShouldTreatAsClone(storedCount, newCount uint32, libraryWarning bool) bool {
	if storedCount == 0 && newCount == 0 {
		return false // 该认证器不维护计数器，这个特性对它不适用
	}
	return libraryWarning
}
