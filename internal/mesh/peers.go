package mesh

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"asa-server/pkg/atomicfile"
	"asa-server/pkg/filelock"
	"asa-server/pkg/meshid"
	"asa-server/pkg/meshjoin"
)

// PeersFileName 是 {BaseDir}/mesh/ 下的配对与授权表（§6.2、§12 P3-1）。
const PeersFileName = "peers.json"

const (
	peersFileVersion = 1
	// MaxPendingRequests 是待批准申请的上限：同一网络里的陌生节点不能借此刷满列表。
	MaxPendingRequests = 32
	requestTTL         = 7 * 24 * time.Hour
	// DefaultInviteTTL / MaxInviteTTL 是邀请码的有效期。
	DefaultInviteTTL = 10 * time.Minute
	MaxInviteTTL     = 24 * time.Hour
	// reloadThrottle：授权判断时最多每隔这么久 stat 一次文件。
	reloadThrottle = time.Second
)

var (
	ErrPeerNotFound    = errors.New("没有这个对端")
	ErrRequestNotFound = errors.New("没有这条待批准申请")
	ErrInviteNotFound  = errors.New("没有这个邀请码")
	// ErrInviteInvalid 不区分「不存在 / 密钥错 / 已过期」——对调用者统一回答。
	ErrInviteInvalid  = errors.New("邀请码无效或已过期")
	ErrTooManyPending = errors.New("待批准申请已满，请先处理已有的申请")
)

// PeerRecord 是一个对端。两个方向的信息放在同一条记录里：
//   - 入站（它能不能控制本机）：GrantedRole，**授权只看它**；
//   - 出站（本机能不能控制它）：RemoteRole 等是对方 Hello 回答的缓存，只用于显示。
type PeerRecord struct {
	NodeID      meshid.ID `json:"node_id"`
	Label       string    `json:"label,omitempty"`
	GrantedRole string    `json:"granted_role,omitempty"`
	GrantedAt   time.Time `json:"granted_at,omitzero"`

	RemoteRole    string `json:"remote_role,omitempty"`
	RemoteLabel   string `json:"remote_label,omitempty"`
	RemoteVersion string `json:"remote_version,omitempty"`
	// Addrs 是手填的直连地址（P2）。
	Addrs []string `json:"addrs,omitempty"`

	AddedAt time.Time `json:"added_at,omitzero"`
}

// DisplayLabel 返回页面与审计里用的名字：本机起的备注名 > 对方自报的备注名 > 短 ID。
func (p PeerRecord) DisplayLabel() string {
	switch {
	case p.Label != "":
		return p.Label
	case p.RemoteLabel != "":
		return p.RemoteLabel
	default:
		return p.NodeID.Short()
	}
}

// InviteRecord 是一个未用的邀请码。只存密钥的哈希。
type InviteRecord struct {
	ID           string    `json:"id"`
	SecretSHA256 string    `json:"secret_sha256"`
	Role         string    `json:"role"`
	Note         string    `json:"note,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// RequestRecord 是一条待批准的配对申请。
type RequestRecord struct {
	NodeID      meshid.ID `json:"node_id"`
	Label       string    `json:"label,omitempty"`
	Version     string    `json:"version,omitempty"`
	Addr        string    `json:"addr,omitempty"`
	RequestedAt time.Time `json:"requested_at"`
}

// PeersData 是 peers.json 的内容。
type PeersData struct {
	Version  int             `json:"version"`
	Peers    []PeerRecord    `json:"peers"`
	Invites  []InviteRecord  `json:"invites"`
	Requests []RequestRecord `json:"requests"`
}

func (d *PeersData) peer(id meshid.ID) *PeerRecord {
	for i := range d.Peers {
		if d.Peers[i].NodeID == id {
			return &d.Peers[i]
		}
	}
	return nil
}

// ensurePeer 返回 id 的记录，没有就新建。
func (d *PeersData) ensurePeer(id meshid.ID, now time.Time) *PeerRecord {
	if p := d.peer(id); p != nil {
		return p
	}
	d.Peers = append(d.Peers, PeerRecord{NodeID: id, AddedAt: now})
	return &d.Peers[len(d.Peers)-1]
}

func (d *PeersData) prune(now time.Time) {
	d.Invites = slices.DeleteFunc(d.Invites, func(v InviteRecord) bool { return !now.Before(v.ExpiresAt) })
	d.Requests = slices.DeleteFunc(d.Requests, func(r RequestRecord) bool { return now.Sub(r.RequestedAt) > requestTTL })
}

func (d *PeersData) clone() PeersData {
	out := PeersData{Version: d.Version}
	out.Peers = make([]PeerRecord, len(d.Peers))
	for i, p := range d.Peers {
		p.Addrs = slices.Clone(p.Addrs)
		out.Peers[i] = p
	}
	out.Invites = slices.Clone(d.Invites)
	out.Requests = slices.Clone(d.Requests)
	return out
}

func (d *PeersData) grants() map[meshid.ID]string {
	m := make(map[meshid.ID]string, len(d.Peers))
	for _, p := range d.Peers {
		if p.GrantedRole != "" {
			m[p.NodeID] = p.GrantedRole
		}
	}
	return m
}

type fileStamp struct {
	mod  time.Time
	size int64
	ok   bool
}

// PeerStore 是 peers.json 的读写入口。
//
// CLI（mesh invite / revoke / approve）与正在运行的服务是两个进程，所以：读改写一律在
// 文件锁下进行；服务侧按 (mtime, size) 发现文件被别人改过就重新加载，并把授权的变化
// 通知给 OnGrantsChanged（用来断开被撤销的对端，§12 P3-5）。
type PeerStore struct {
	dir string

	// writeMu 串行化本进程内的写：文件锁属于「打开的文件」，同一进程里两次加锁也会互相挡住，
	// 但 Lock 的轮询等待没必要。
	writeMu sync.Mutex

	mu        sync.Mutex
	data      PeersData
	stamp     fileStamp
	lastCheck time.Time
	loaded    bool
	onChange  func(changed []meshid.ID)
}

// OpenPeerStore 返回 dir 下的授权表。不碰磁盘，第一次用到时才读。
func OpenPeerStore(dir string) *PeerStore {
	return &PeerStore{dir: dir}
}

// OnGrantsChanged 设置授权变化的回调：参数是授权被撤销或**降级**（admin → operator）的节点。
// 升级与新授权不通知——它们不需要断开任何东西（断开反而会丢掉正在返回的 Pair 响应）。
// 回调在锁外调用。
func (s *PeerStore) OnGrantsChanged(fn func(changed []meshid.ID)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

func (s *PeerStore) path() string     { return filepath.Join(s.dir, PeersFileName) }
func (s *PeerStore) lockPath() string { return filepath.Join(s.dir, PeersFileName+".lock") }

func statFile(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fileStamp{}, nil
	}
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{mod: info.ModTime(), size: info.Size(), ok: true}, nil
}

func readPeersFile(path string) (PeersData, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return PeersData{Version: peersFileVersion}, nil
	}
	if err != nil {
		return PeersData{}, err
	}
	var d PeersData
	if err := json.Unmarshal(raw, &d); err != nil {
		return PeersData{}, fmt.Errorf("解析 %s: %w", PeersFileName, err)
	}
	if d.Version == 0 {
		d.Version = peersFileVersion
	}
	return d, nil
}

// Refresh 在文件被（别的进程）改过时重新加载。force 为 false 时按 reloadThrottle 节流。
func (s *PeerStore) Refresh(force bool) error {
	s.mu.Lock()
	if !force && s.loaded && time.Since(s.lastCheck) < reloadThrottle {
		s.mu.Unlock()
		return nil
	}
	s.lastCheck = time.Now()
	s.mu.Unlock()

	st, err := statFile(s.path())
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.loaded && st == s.stamp {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	d, err := readPeersFile(s.path())
	if err != nil {
		return err
	}
	s.install(d, st)
	return nil
}

// install 换上新内容，并把授权的变化通知出去。
func (s *PeerStore) install(d PeersData, st fileStamp) {
	s.mu.Lock()
	old := s.data.grants()
	wasLoaded := s.loaded
	s.data, s.stamp, s.loaded = d, st, true
	now := d.grants()
	fn := s.onChange
	s.mu.Unlock()

	if !wasLoaded || fn == nil {
		return
	}
	var changed []meshid.ID
	for id, role := range old {
		if roleRank(now[id]) < roleRank(role) {
			changed = append(changed, id)
		}
	}
	if len(changed) > 0 {
		fn(changed)
	}
}

// Snapshot 返回当前内容的副本。
func (s *PeerStore) Snapshot() (PeersData, error) {
	if err := s.Refresh(false); err != nil {
		return PeersData{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.clone(), nil
}

// Grant 返回本机授予 id 的角色；未授权为 ""。读文件失败时按未授权处理（失败即拒绝）。
func (s *PeerStore) Grant(id meshid.ID) string {
	if err := s.Refresh(false); err != nil {
		s.mu.Lock()
		loaded := s.loaded
		s.mu.Unlock()
		if !loaded {
			return ""
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.data.peer(id); p != nil {
		return p.GrantedRole
	}
	return ""
}

// Paired 实现 Authorizer：已授权（任何角色）即为已配对。
func (s *PeerStore) Paired(id meshid.ID) bool { return s.Grant(id) != "" }

// Peer 返回 id 的记录。
func (s *PeerStore) Peer(id meshid.ID) (PeerRecord, bool) {
	d, err := s.Snapshot()
	if err != nil {
		return PeerRecord{}, false
	}
	if p := d.peer(id); p != nil {
		return *p, true
	}
	return PeerRecord{}, false
}

// Update 在文件锁下读出最新内容、交给 fn 修改、清理过期项后写回。fn 返回错误时不写。
func (s *PeerStore) Update(fn func(d *PeersData, now time.Time) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	release, err := filelock.Lock(context.Background(), s.lockPath(), filelock.Exclusive, 20*time.Millisecond, nil)
	if err != nil {
		return err
	}
	defer release()

	d, err := readPeersFile(s.path())
	if err != nil {
		return err
	}
	now := time.Now()
	if err := fn(&d, now); err != nil {
		return err
	}
	d.prune(now)
	d.Version = peersFileVersion
	raw, err := json.MarshalIndent(&d, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.path(), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	st, err := statFile(s.path())
	if err != nil {
		return err
	}
	s.install(d, st)
	return nil
}

// roleRank 给角色排序：admin > operator > 未授权。
func roleRank(role string) int {
	switch role {
	case RoleAdmin:
		return 2
	case RoleOperator:
		return 1
	}
	return 0
}

// ValidateRole 校验授予的角色。
func ValidateRole(role string) error {
	if role != RoleAdmin && role != RoleOperator {
		return fmt.Errorf("角色只能是 %s 或 %s", RoleAdmin, RoleOperator)
	}
	return nil
}

func secretHash(secret []byte) string {
	sum := sha256.Sum256(secret)
	return hex.EncodeToString(sum[:])
}

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// InviteOptions 是生成邀请码的参数。
type InviteOptions struct {
	Role  string
	TTL   time.Duration
	Addrs []string
	Note  string
}

// CreateInvite 生成一个邀请码：记录（只存哈希）写进 peers.json，整串只返回这一次。
func (s *PeerStore) CreateInvite(self meshid.ID, opts InviteOptions) (string, InviteRecord, error) {
	if err := ValidateRole(opts.Role); err != nil {
		return "", InviteRecord{}, err
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultInviteTTL
	}
	if ttl > MaxInviteTTL {
		return "", InviteRecord{}, fmt.Errorf("邀请码有效期最长 %s", MaxInviteTTL)
	}
	for _, a := range opts.Addrs {
		if _, _, err := net.SplitHostPort(a); err != nil {
			return "", InviteRecord{}, fmt.Errorf("直连地址 %q 不是 host:port", a)
		}
	}
	secret := make([]byte, meshjoin.InviteSecretLen)
	if _, err := rand.Read(secret); err != nil {
		return "", InviteRecord{}, err
	}
	now := time.Now()
	rec := InviteRecord{
		ID: randomID(6), SecretSHA256: secretHash(secret), Role: opts.Role, Note: strings.TrimSpace(opts.Note),
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	encoded, err := meshjoin.Invite{
		Node: self, ID: rec.ID, Secret: secret, Role: opts.Role, Addrs: opts.Addrs, Expires: rec.ExpiresAt,
	}.Encode()
	if err != nil {
		return "", InviteRecord{}, err
	}
	err = s.Update(func(d *PeersData, _ time.Time) error {
		d.Invites = append(d.Invites, rec)
		return nil
	})
	if err != nil {
		return "", InviteRecord{}, err
	}
	return encoded, rec, nil
}

// DeleteInvite 作废一个邀请码。
func (s *PeerStore) DeleteInvite(id string) error {
	return s.Update(func(d *PeersData, _ time.Time) error {
		n := len(d.Invites)
		d.Invites = slices.DeleteFunc(d.Invites, func(v InviteRecord) bool { return v.ID == id })
		if len(d.Invites) == n {
			return ErrInviteNotFound
		}
		return nil
	})
}

// RedeemInvite 用邀请码给 peer 授权并消费掉邀请（一次性）。返回授予的角色。
// 任何一项不对都返回 ErrInviteInvalid，不说是哪一项。
func (s *PeerStore) RedeemInvite(peer meshid.ID, inviteID string, secret []byte, label string) (string, error) {
	var role string
	err := s.Update(func(d *PeersData, now time.Time) error {
		i := slices.IndexFunc(d.Invites, func(v InviteRecord) bool { return v.ID == inviteID })
		if i < 0 {
			return ErrInviteInvalid
		}
		inv := d.Invites[i]
		if subtle.ConstantTimeCompare([]byte(secretHash(secret)), []byte(inv.SecretSHA256)) != 1 || !now.Before(inv.ExpiresAt) {
			return ErrInviteInvalid
		}
		d.Invites = slices.Delete(d.Invites, i, i+1)
		p := d.ensurePeer(peer, now)
		p.GrantedRole, p.GrantedAt = inv.Role, now
		if p.Label == "" {
			p.Label = firstNonEmpty(inv.Note, label)
		}
		d.Requests = slices.DeleteFunc(d.Requests, func(r RequestRecord) bool { return r.NodeID == peer })
		role = inv.Role
		return nil
	})
	return role, err
}

// AddRequest 记一条待批准申请（同一节点只保留最新一条）。
func (s *PeerStore) AddRequest(r RequestRecord) error {
	return s.Update(func(d *PeersData, now time.Time) error {
		d.Requests = slices.DeleteFunc(d.Requests, func(x RequestRecord) bool { return x.NodeID == r.NodeID })
		if len(d.Requests) >= MaxPendingRequests {
			return ErrTooManyPending
		}
		r.RequestedAt = now
		d.Requests = append(d.Requests, r)
		return nil
	})
}

// ApproveRequest 批准一条申请。
func (s *PeerStore) ApproveRequest(id meshid.ID, role string) error {
	if err := ValidateRole(role); err != nil {
		return err
	}
	return s.Update(func(d *PeersData, now time.Time) error {
		i := slices.IndexFunc(d.Requests, func(r RequestRecord) bool { return r.NodeID == id })
		if i < 0 {
			return ErrRequestNotFound
		}
		req := d.Requests[i]
		d.Requests = slices.Delete(d.Requests, i, i+1)
		p := d.ensurePeer(id, now)
		p.GrantedRole, p.GrantedAt = role, now
		if p.RemoteLabel == "" {
			p.RemoteLabel = req.Label
		}
		if p.RemoteVersion == "" {
			p.RemoteVersion = req.Version
		}
		return nil
	})
}

// RejectRequest 拒绝（删除）一条申请。
func (s *PeerStore) RejectRequest(id meshid.ID) error {
	return s.Update(func(d *PeersData, _ time.Time) error {
		n := len(d.Requests)
		d.Requests = slices.DeleteFunc(d.Requests, func(r RequestRecord) bool { return r.NodeID == id })
		if len(d.Requests) == n {
			return ErrRequestNotFound
		}
		return nil
	})
}

// Revoke 撤销对 id 的入站授权（记录保留：本机可能还要控制它）。
func (s *PeerStore) Revoke(id meshid.ID) error {
	return s.Update(func(d *PeersData, _ time.Time) error {
		p := d.peer(id)
		if p == nil || p.GrantedRole == "" {
			return ErrPeerNotFound
		}
		p.GrantedRole, p.GrantedAt = "", time.Time{}
		return nil
	})
}

// Forget 删除 id 的整条记录（撤销入站授权并忘掉它）。
func (s *PeerStore) Forget(id meshid.ID) error {
	return s.Update(func(d *PeersData, _ time.Time) error {
		n := len(d.Peers)
		d.Peers = slices.DeleteFunc(d.Peers, func(p PeerRecord) bool { return p.NodeID == id })
		if len(d.Peers) == n {
			return ErrPeerNotFound
		}
		return nil
	})
}

// PeerPatch 是 PUT /api/mesh/peers/:id 的请求体：只改出现的字段。GrantedRole 为 "" = 撤销。
type PeerPatch struct {
	Label       *string   `json:"label"`
	GrantedRole *string   `json:"granted_role"`
	Addrs       *[]string `json:"addrs"`
}

// SetPeer 按 patch 修改（必要时新建）id 的记录。
func (s *PeerStore) SetPeer(id meshid.ID, p PeerPatch) error {
	if p.GrantedRole != nil && *p.GrantedRole != "" {
		if err := ValidateRole(*p.GrantedRole); err != nil {
			return err
		}
	}
	if p.Addrs != nil {
		for _, a := range *p.Addrs {
			if _, _, err := net.SplitHostPort(a); err != nil {
				return fmt.Errorf("直连地址 %q 不是 host:port", a)
			}
		}
	}
	return s.Update(func(d *PeersData, now time.Time) error {
		rec := d.ensurePeer(id, now)
		if p.Label != nil {
			rec.Label = strings.TrimSpace(*p.Label)
		}
		if p.GrantedRole != nil && *p.GrantedRole != rec.GrantedRole {
			rec.GrantedRole = *p.GrantedRole
			rec.GrantedAt = now
			if rec.GrantedRole == "" {
				rec.GrantedAt = time.Time{}
			}
		}
		if p.Addrs != nil {
			rec.Addrs = slices.Clone(*p.Addrs)
		}
		return nil
	})
}

// RecordRemote 记下对方的 Hello / Pair 回答（出站方向的缓存）。addrs 非空时并入手填地址。
// 内容没有变化时不写文件。
func (s *PeerStore) RecordRemote(id meshid.ID, role, label, version string, addrs []string) error {
	if p, ok := s.Peer(id); ok && p.RemoteRole == role && p.RemoteLabel == label && p.RemoteVersion == version &&
		!hasNew(p.Addrs, addrs) {
		return nil
	}
	return s.Update(func(d *PeersData, now time.Time) error {
		p := d.ensurePeer(id, now)
		p.RemoteRole, p.RemoteLabel, p.RemoteVersion = role, label, version
		for _, a := range addrs {
			if !slices.Contains(p.Addrs, a) {
				p.Addrs = append(p.Addrs, a)
			}
		}
		return nil
	})
}

func hasNew(have, add []string) bool {
	for _, a := range add {
		if !slices.Contains(have, a) {
			return true
		}
	}
	return false
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
