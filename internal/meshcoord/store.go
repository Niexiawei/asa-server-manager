package meshcoord

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"asa-server/pkg/meshid"

	_ "modernc.org/sqlite" // 纯 Go 的 SQLite 驱动，无需 cgo
)

// DefaultNetworkID 是首启自动建的网络。D6-B：首期只有它，没有建网络的 CLI（§8.4.4）。
const DefaultNetworkID = "default"

const schemaVersion = 1

// ErrNotFound 是存储层的「没有这条记录」。
var ErrNotFound = errors.New("记录不存在")

// Network 是一个网络。
//
// ⚠️ 与计划文档 §8.4.5 的偏差：密钥存**原文**而不是哈希。只存哈希的话
// `asa-coordinator join-blob` 就没法随时重新打印接入串（首启又刻意不打印），
// 而数据库与协调节点自己的私钥在同一个 data_dir 里——能读到库的人本来就能冒充协调节点，
// 哈希在这里挡不住任何人。数据库文件因此是 0600。
type Network struct {
	ID        string
	Name      string
	Secret    string
	CreatedAt time.Time
	QuotaJSON string
}

// NodeRecord 是节点表的一行。
type NodeRecord struct {
	NetworkID string
	NodeID    meshid.ID
	Label     string
	Version   string
	FirstSeen time.Time // 零值 = 从未接入（例如被预先拉黑）
	LastSeen  time.Time
	Banned    bool
}

// Store 是协调节点的持久化：网络、节点、拉黑表。
type Store struct {
	db *sql.DB
}

// OpenStore 打开（必要时创建）{dataDir}/coordinator.db 并建表。
func OpenStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "coordinator.db")
	// 先建出空文件把权限定成 0600，再交给 SQLite（它会沿用已有文件的权限）。
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		f.Close()
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(path)+
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	// 写很少（登记、拉黑），单连接即可彻底规避 SQLITE_BUSY。
	// asa-coordinator node ban 是另一个进程，跨进程靠 WAL + busy_timeout。
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v > schemaVersion {
		return fmt.Errorf("数据库版本 %d 高于本程序支持的 %d，请升级 asa-coordinator", v, schemaVersion)
	}
	if v == schemaVersion {
		return nil
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS networks (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	secret     TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	quota_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS nodes (
	network_id TEXT NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
	node_id    TEXT NOT NULL,
	label      TEXT NOT NULL DEFAULT '',
	version    TEXT NOT NULL DEFAULT '',
	first_seen INTEGER NOT NULL DEFAULT 0,
	last_seen  INTEGER NOT NULL DEFAULT 0,
	banned     INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (network_id, node_id)
);
PRAGMA user_version = 1;`)
	return err
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

// EnsureDefaultNetwork 确保 default 网络存在；首次创建时生成 32 字节随机密钥。
func (s *Store) EnsureDefaultNetwork() (Network, bool, error) {
	n, err := s.Network(DefaultNetworkID)
	if err == nil {
		return n, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Network{}, false, err
	}
	n, err = s.CreateNetwork(DefaultNetworkID, "默认网络")
	return n, err == nil, err
}

// CreateNetwork 建一个网络。首期只有 EnsureDefaultNetwork 与测试会调它。
func (s *Store) CreateNetwork(id, name string) (Network, error) {
	secret, err := newSecret()
	if err != nil {
		return Network{}, err
	}
	n := Network{ID: id, Name: name, Secret: secret, CreatedAt: time.Now().Truncate(time.Second), QuotaJSON: "{}"}
	_, err = s.db.Exec(`INSERT INTO networks (id, name, secret, created_at, quota_json) VALUES (?, ?, ?, ?, ?)`,
		n.ID, n.Name, n.Secret, n.CreatedAt.Unix(), n.QuotaJSON)
	if err != nil {
		return Network{}, err
	}
	return n, nil
}

// Network 读一个网络。
func (s *Store) Network(id string) (Network, error) {
	var n Network
	var created int64
	err := s.db.QueryRow(`SELECT id, name, secret, created_at, quota_json FROM networks WHERE id = ?`, id).
		Scan(&n.ID, &n.Name, &n.Secret, &created, &n.QuotaJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Network{}, ErrNotFound
	}
	n.CreatedAt = time.Unix(created, 0)
	return n, err
}

// Node 读一行节点记录。
func (s *Store) Node(networkID string, id meshid.ID) (NodeRecord, error) {
	row := s.db.QueryRow(`SELECT network_id, node_id, label, version, first_seen, last_seen, banned
		FROM nodes WHERE network_id = ? AND node_id = ?`, networkID, id.Compact())
	r, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeRecord{}, ErrNotFound
	}
	return r, err
}

// RecordJoin 记录一次成功登记：首次则插入，之后更新 label / version / last_seen。
func (s *Store) RecordJoin(networkID string, id meshid.ID, label, version string, now time.Time) error {
	_, err := s.db.Exec(`
INSERT INTO nodes (network_id, node_id, label, version, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (network_id, node_id) DO UPDATE SET
	label = excluded.label, version = excluded.version, last_seen = excluded.last_seen,
	first_seen = CASE WHEN nodes.first_seen = 0 THEN excluded.first_seen ELSE nodes.first_seen END`,
		networkID, id.Compact(), label, version, now.Unix(), now.Unix())
	return err
}

// Touch 更新在线节点的 last_seen。
func (s *Store) Touch(networkID string, id meshid.ID, now time.Time) error {
	_, err := s.db.Exec(`UPDATE nodes SET last_seen = ? WHERE network_id = ? AND node_id = ?`,
		now.Unix(), networkID, id.Compact())
	return err
}

// SetBanned 拉黑 / 解除拉黑。节点从未接入时也能预先拉黑（插入一行 first_seen = 0 的记录）。
func (s *Store) SetBanned(networkID string, id meshid.ID, banned bool) error {
	if _, err := s.Network(networkID); err != nil {
		return fmt.Errorf("网络 %q: %w", networkID, err)
	}
	_, err := s.db.Exec(`
INSERT INTO nodes (network_id, node_id, banned) VALUES (?, ?, ?)
ON CONFLICT (network_id, node_id) DO UPDATE SET banned = excluded.banned`,
		networkID, id.Compact(), boolInt(banned))
	return err
}

// BannedAmong 返回 ids 里被拉黑的那些（给在线会话的定期复查用）。
func (s *Store) BannedAmong(ctx context.Context, networkID string, ids []meshid.ID) (map[meshid.ID]bool, error) {
	out := map[meshid.ID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{networkID}
	for _, id := range ids {
		args = append(args, id.Compact())
	}
	q := `SELECT node_id FROM nodes WHERE network_id = ? AND banned = 1 AND node_id IN (?` +
		strings.Repeat(",?", len(ids)-1) + `)`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if id, err := meshid.ParseID(raw); err == nil {
			out[id] = true
		}
	}
	return out, rows.Err()
}

// ListNodes 列出节点；networkID 为空时列出全部网络。
func (s *Store) ListNodes(networkID string) ([]NodeRecord, error) {
	q := `SELECT network_id, node_id, label, version, first_seen, last_seen, banned FROM nodes`
	var args []any
	if networkID != "" {
		q += ` WHERE network_id = ?`
		args = append(args, networkID)
	}
	q += ` ORDER BY network_id, last_seen DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeRecord
	for rows.Next() {
		r, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanNode(row scanner) (NodeRecord, error) {
	var r NodeRecord
	var nodeID string
	var first, last int64
	var banned int
	if err := row.Scan(&r.NetworkID, &nodeID, &r.Label, &r.Version, &first, &last, &banned); err != nil {
		return NodeRecord{}, err
	}
	id, err := meshid.ParseID(nodeID)
	if err != nil {
		return NodeRecord{}, fmt.Errorf("数据库里的节点 ID %q 无效: %w", nodeID, err)
	}
	r.NodeID = id
	if first > 0 {
		r.FirstSeen = time.Unix(first, 0)
	}
	if last > 0 {
		r.LastSeen = time.Unix(last, 0)
	}
	r.Banned = banned != 0
	return r, nil
}

func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
