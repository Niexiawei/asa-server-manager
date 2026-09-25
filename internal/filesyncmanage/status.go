package filesyncmanage

import (
	"errors"
	"sort"
	"time"

	"github.com/Niexiawei/simple-file-sync/pkg/client"
)

// 整体状态。
const (
	StateNotConfigured = "not_configured" // 还没保存过配置
	StateDisabled      = "disabled"       // 配置里关闭了同步
	StateStopped       = "stopped"        // 开启但没在跑（手动停止）
	StateConnecting    = "connecting"     // 节点在跑，尚未连上（或断线重连中）
	StateConnected     = "connected"
	StateFailed        = "failed" // 因终态错误停下，见 Message
)

// Status 是 /api/filesync/status 与其 SSE 流的共同 payload。
type Status struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`

	NodeID       string `json:"node_id,omitempty"`
	Address      string `json:"address,omitempty"`
	HasBootstrap bool   `json:"has_bootstrap"`
	// Enrolled 表示本机已经取得过节点证书（之后引导凭据可以删掉）。
	Enrolled bool `json:"enrolled"`
	// CertNotAfter 是当前使用的证书的到期时间：接入后是节点证书，接入前是引导证书。
	CertNotAfter *time.Time `json:"cert_not_after,omitempty"`

	// 连接诊断（见同步库 NodeState）：RemoteAddr 是最近一次握手实际连到的地址，
	// 公网 IP 被 DDNS 轮换时拿它对比域名当前解析到哪里。
	RemoteAddr     string `json:"remote_addr,omitempty"`
	Reconnects     int    `json:"reconnects"`
	AddressChanges int    `json:"address_changes"`
	LastConnectErr string `json:"last_connect_error,omitempty"`

	RxBytesPerSecond float64 `json:"rx_bytes_per_second"`
	TxBytesPerSecond float64 `json:"tx_bytes_per_second"`

	Clusters []ClusterStatus `json:"clusters"`
	Recent   []Notice        `json:"recent"`
}

// ClusterStatus 是一个集群同步根的状态。
type ClusterStatus struct {
	ClusterID     string     `json:"cluster_id"`
	Path          string     `json:"path"`
	PendingLocal  int        `json:"pending_local"`  // 本地已变化、尚未上报的文件
	InFlightTasks int        `json:"inflight_tasks"` // 正在执行的传输/删除
	FailedTasks   int        `json:"failed_tasks"`
	LastSyncedAt  *time.Time `json:"last_synced_at,omitempty"`
	LastScanAt    *time.Time `json:"last_scan_at,omitempty"`
	Error         string     `json:"error,omitempty"`
	Transfers     []Transfer `json:"transfers"`
}

// Transfer 是一条在途传输。
type Transfer struct {
	Direction      string  `json:"direction"` // upload | download
	Path           string  `json:"path"`
	Size           int64   `json:"size"`
	Offset         int64   `json:"offset"`
	BytesPerSecond float64 `json:"bytes_per_second"`
	ETASeconds     float64 `json:"eta_seconds"`
}

// Status 汇总当前状态。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	st := Status{NodeID: m.nodeID(), Enrolled: m.hasNodeCertificate(), Recent: notices.snapshot(), Clusters: []ClusterStatus{}}
	switch {
	case m.cfg == nil:
		st.State = StateNotConfigured
		return st
	case m.node != nil:
		st.State = StateConnecting
	case m.failure != nil && !errors.Is(m.failure, ErrNotConfigured):
		st.State = StateFailed
	case !m.cfg.Enabled:
		st.State = StateDisabled
	default:
		st.State = StateStopped
	}
	if m.failure != nil {
		st.Message = m.failure.Error()
	}
	st.Address = m.cfg.Address
	st.HasBootstrap = m.cfg.hasBootstrap()
	if !st.Enrolled && st.HasBootstrap {
		if summary, err := m.cfg.bootstrapSummary(); err == nil {
			st.CertNotAfter = &summary
		}
	}

	var nodeState client.NodeState
	if m.node != nil {
		nodeState = m.node.State()
		if nodeState.Connected {
			st.State = StateConnected
		}
		if expiry := m.node.CertificateExpiry(); !expiry.IsZero() {
			st.CertNotAfter = &expiry
		}
		st.RemoteAddr = nodeState.RemoteAddr
		st.Reconnects = nodeState.Reconnects
		st.AddressChanges = nodeState.AddressChanges
		if nodeState.LastConnectErr != nil && !nodeState.Connected {
			st.LastConnectErr = nodeState.LastConnectErr.Error()
		}
		stats := m.node.Stats()
		st.RxBytesPerSecond, st.TxBytesPerSecond = stats.RxBytesPerSecond, stats.TxBytesPerSecond
	}

	for _, clusterID := range m.cfg.clusterIDs() {
		cs := ClusterStatus{ClusterID: clusterID, Path: clusterDir(m.baseDir, clusterID), Transfers: []Transfer{}}
		if reason, ok := m.rootErr[clusterID]; ok {
			cs.Error = reason
		}
		if root, ok := m.roots[clusterID]; ok {
			if rs, ok := nodeState.Roots[groupID(clusterID)]; ok {
				cs.PendingLocal, cs.InFlightTasks, cs.FailedTasks = rs.PendingLocal, rs.InFlightTasks, rs.FailedTasks
				cs.LastSyncedAt, cs.LastScanAt = timePtr(rs.LastSyncedAt), timePtr(rs.LastScanAt)
				if rs.Failed && rs.FailedErr != nil {
					cs.Error = rs.FailedErr.Error()
				}
			}
			stats := root.Stats()
			cs.Transfers = append(cs.Transfers, transfers("upload", stats.Uploads)...)
			cs.Transfers = append(cs.Transfers, transfers("download", stats.Downloads)...)
		}
		st.Clusters = append(st.Clusters, cs)
	}
	return st
}

func transfers(direction string, list []client.Transfer) []Transfer {
	out := make([]Transfer, 0, len(list))
	for _, t := range list {
		out = append(out, Transfer{
			Direction: direction, Path: t.Path, Size: t.Size, Offset: t.Offset,
			BytesPerSecond: t.BytesPerSecond, ETASeconds: t.ETA.Seconds(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
