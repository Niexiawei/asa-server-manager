package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"asa-server/appconfig"
)

var (
	// ErrSessionRevoked 表示令牌本身有效，但对应的会话已被吊销
	// （改过密码、被管理员踢下线，或这台设备单独登出过）
	ErrSessionRevoked = errors.New("会话已失效，请重新登录")
	// ErrNotInitialized 表示鉴权尚未初始化
	ErrNotInitialized = errors.New("鉴权模块未初始化")
)

// Manager 持有数据库连接、签名密钥，以及用户表和吊销列表的内存副本。
//
// 内存副本存在的理由：中间件每个请求都要校验令牌，而校验需要读该用户的
// session_version 和 denylist。如果每个请求都打一次 SQLite，长驻 SSE 加上
// 高频 REST 会把它变成瓶颈。所以——**SQLite 是持久化真相，内存是读路径**，
// 所有写操作都在同一个方法里同时更新两者。
type Manager struct {
	db      *sql.DB
	secret  []byte
	baseDir string

	mu       sync.RWMutex
	users    map[string]*User     // key: 小写用户名
	denylist map[string]time.Time // key: jti
}

var global atomic.Pointer[Manager]

// Initialize 打开数据库、按需迁移、加载内存副本，并把结果设为全局实例。
//
// 只应在 auth.enabled 为 true 时调用：鉴权关闭时完全不碰 auth.db，
// 数据库有任何问题都不该影响一个没开鉴权的实例。
func Initialize(baseDir string) (*Manager, error) {
	cfg := appconfig.Get()
	dbPath := cfg.DatabasePath(baseDir)

	db, err := Open(dbPath)
	if err != nil {
		return nil, err
	}

	if cfg.Auth.Database.AutoMigrate {
		if _, err := Migrate(db); err != nil {
			db.Close()
			// 迁移失败必须让调用方拒绝启动，绝不能"静默降级为不鉴权"——
			// 那是把一个安全故障变成一个安全漏洞。
			return nil, fmt.Errorf("%w。可执行 asa-server db migrate --dry-run 诊断", err)
		}
	} else {
		pending, err := Pending(db)
		if err != nil {
			db.Close()
			return nil, err
		}
		if len(pending) > 0 {
			db.Close()
			return nil, fmt.Errorf("数据库有 %d 个待执行的迁移，但 auth.database.auto_migrate 为 false。"+
				"请执行 asa-server db migrate", len(pending))
		}
	}

	secret, err := LoadOrCreateSecret(baseDir)
	if err != nil {
		db.Close()
		return nil, err
	}

	m := &Manager{db: db, secret: secret, baseDir: baseDir}
	if err := m.Reload(context.Background()); err != nil {
		db.Close()
		return nil, err
	}

	global.Store(m)
	return m, nil
}

// GetManager 返回全局实例，未初始化时返回 nil
func GetManager() *Manager { return global.Load() }

// Close 关闭数据库并清空全局实例
func (m *Manager) Close() error {
	global.CompareAndSwap(m, nil)
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

// DB 返回底层连接，供需要直接写 SQL 的场景使用
func (m *Manager) DB() *sql.DB { return m.db }

// BaseDir 返回程序数据根目录
func (m *Manager) BaseDir() string { return m.baseDir }

// Reload 从数据库重新加载内存副本。
// CLI 在本机改完库之后会通过 /api/auth/reload 触发它，
// 这样丢手机、忘密码的用户不必等服务重启就能登录。
func (m *Manager) Reload(ctx context.Context) error {
	users, err := ListUsers(ctx, m.db)
	if err != nil {
		return err
	}
	deny, err := LoadDenylist(ctx, m.db, time.Now())
	if err != nil {
		return err
	}

	byName := make(map[string]*User, len(users))
	for _, u := range users {
		byName[strings.ToLower(u.Username)] = u
	}

	m.mu.Lock()
	m.users = byName
	m.denylist = deny
	m.mu.Unlock()
	return nil
}

// Lookup 从内存副本里取用户。零 I/O，可以在请求热路径上调用。
func (m *Manager) Lookup(username string) (*User, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[strings.ToLower(strings.TrimSpace(username))]
	if !ok {
		return nil, false
	}
	cp := *u // 返回副本，防止调用方改到缓存里的对象
	return &cp, true
}

// UserCount 返回用户数量。为 0 时系统处于零用户状态，需要走首次引导。
func (m *Manager) UserCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users)
}

// Users 返回内存副本里全部用户的快照
func (m *Manager) Users() []*User {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		cp := *u
		out = append(out, &cp)
	}
	return out
}

// IssueSession 签发一个会话令牌
func (m *Manager) IssueSession(u *User, stage string, amr []string, ttl time.Duration) (string, *Claims, error) {
	jti, err := NewJTI()
	if err != nil {
		return "", nil, err
	}
	now := time.Now()
	c := Claims{
		Username:  u.Username,
		Version:   u.SessionVersion,
		JTI:       jti,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
		Stage:     stage,
		AMR:       amr,
	}
	tok, err := SignToken(m.secret, c)
	if err != nil {
		return "", nil, err
	}
	return tok, &c, nil
}

// VerifySession 校验一个完整会话令牌，返回对应用户。
//
// 全程只读内存副本 + 一次 HMAC 计算，不查数据库。
func (m *Manager) VerifySession(token string) (*User, *Claims, error) {
	c, err := ParseTokenWithStage(m.secret, token, StageFull)
	if err != nil {
		return nil, nil, err
	}
	u, err := m.checkClaims(c)
	if err != nil {
		return nil, nil, err
	}
	return u, c, nil
}

// VerifyPreAuth 校验两步验证中间态令牌。
//
// 必须和 VerifySession 分成两个函数：只要有一处把 pre-auth 令牌当成完整凭证接受，
// 第二步验证就形同虚设。
func (m *Manager) VerifyPreAuth(token string) (*User, *Claims, error) {
	c, err := ParseTokenWithStage(m.secret, token, StagePre)
	if err != nil {
		return nil, nil, err
	}
	u, err := m.checkClaims(c)
	if err != nil {
		return nil, nil, err
	}
	return u, c, nil
}

func (m *Manager) checkClaims(c *Claims) (*User, error) {
	u, ok := m.Lookup(c.Username)
	if !ok {
		return nil, ErrUserNotFound
	}
	if u.Disabled {
		return nil, ErrUserDisabled
	}
	// 改密码、管理员踢人、"登出全部设备"都会让 session_version 前进一位，
	// 于是此前签发的所有令牌立刻作废。
	if c.Version != u.SessionVersion {
		return nil, ErrSessionRevoked
	}

	m.mu.RLock()
	exp, denied := m.denylist[c.JTI]
	m.mu.RUnlock()
	if denied && time.Now().Before(exp) {
		return nil, ErrSessionRevoked
	}
	return u, nil
}

// ShouldRenew 判断是否该滑动续期。
//
// 续期不只是方便：固定 TTL 意味着所有客户端的会话在同一时刻过期，
// 于是所有浏览器同时被踢、同时开始重连，形成惊群。让活跃用户的到期时间
// 自然错开，是从源头削掉那个尖峰。
func ShouldRenew(c *Claims, ttl time.Duration, now time.Time) bool {
	if ttl <= 0 {
		return false
	}
	return c.RemainingLifetime(now) < ttl/2
}

// RevokeToken 只登出当前这台设备（把 jti 加进吊销列表）
func (m *Manager) RevokeToken(ctx context.Context, c *Claims) error {
	if c == nil || c.JTI == "" {
		return nil
	}
	exp := time.Unix(c.ExpiresAt, 0)
	if err := DenyToken(ctx, m.db, c.JTI, exp); err != nil {
		return err
	}
	m.mu.Lock()
	if m.denylist == nil {
		m.denylist = map[string]time.Time{}
	}
	m.denylist[c.JTI] = exp
	m.mu.Unlock()
	return nil
}

// RevokeAllSessions 登出该用户的全部设备
func (m *Manager) RevokeAllSessions(ctx context.Context, username string) error {
	if err := BumpSessionVersion(ctx, m.db, username); err != nil {
		return err
	}
	return m.Reload(ctx)
}

// Housekeeping 做周期性清理：删掉已过期的吊销记录，把审计日志裁剪到上限。
func (m *Manager) Housekeeping(ctx context.Context) error {
	now := time.Now()
	if _, err := PurgeExpiredTokens(ctx, m.db, now); err != nil {
		return err
	}
	if _, err := TrimAudit(ctx, m.db, appconfig.Get().Auth.Audit.MaxRows); err != nil {
		return err
	}

	m.mu.Lock()
	maps.DeleteFunc(m.denylist, func(_ string, exp time.Time) bool {
		return now.After(exp)
	})
	m.mu.Unlock()
	return nil
}

// Audit 写一条审计记录。失败只记录不打断业务——
// 审计写不进去不该让用户登不上。
//
// 来源 IP 与 UA 没显式给出时从 context 里补：这样领域方法不必为了记日志
// 而在签名里到处传 HTTP 细节，同时又不会漏掉"谁从哪里做的"。
func (m *Manager) Audit(ctx context.Context, e AuditEntry) {
	if e.ClientIP == "" || e.UserAgent == "" {
		src := AuditSourceFrom(ctx)
		if e.ClientIP == "" {
			e.ClientIP = src.ClientIP
		}
		if e.UserAgent == "" {
			e.UserAgent = src.UserAgent
		}
	}
	_ = WriteAudit(ctx, m.db, e)
}

// LimitParams 从当前配置构造限流参数
func LimitParamsFromConfig() LimitParams {
	rl := appconfig.Get().Auth.RateLimit
	return LimitParams{
		MaxFailures: rl.MaxFailures,
		Window:      rl.Window,
		Lockout:     rl.Lockout,
	}
}
