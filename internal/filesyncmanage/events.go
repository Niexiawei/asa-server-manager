package filesyncmanage

import (
	"strings"
	"sync"
	"time"

	"github.com/Niexiawei/simple-file-sync/pkg/client"

	"asa-server/internal/realtime"
	"asa-server/internal/runner"
	"asa-server/pkg/logger"
)

// realtimeEventType 是推给前端的 WS 事件类型，前端按它筛出文件同步的通知。
const realtimeEventType = "filesync"

// maxRecentEvents 是状态面板上"最近告警"保留的条数。
const maxRecentEvents = 20

// Notice 是一条值得让用户看到的同步事件（状态面板的"最近告警"与 WS 推送共用）。
type Notice struct {
	Time         time.Time `json:"time"`
	Kind         string    `json:"kind"`
	Level        string    `json:"level"` // info | warning | error
	ClusterID    string    `json:"cluster_id,omitempty"`
	Message      string    `json:"message"`
	Path         string    `json:"path,omitempty"`
	ConflictPath string    `json:"conflict_path,omitempty"`
}

// recentNotices 是最近的告警，环形保留 maxRecentEvents 条。
// 单独一把锁：事件回调在同步库的协程里，不能去抢 Manager.mu（见 Manager.generation）。
type recentNotices struct {
	mu    sync.Mutex
	items []Notice
}

func (r *recentNotices) add(n Notice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, n)
	if len(r.items) > maxRecentEvents {
		r.items = r.items[len(r.items)-maxRecentEvents:]
	}
}

func (r *recentNotices) snapshot() []Notice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Notice(nil), r.items...)
}

var notices recentNotices

// clusterIDOf 从同步组名还原集群 ID；不是本程序建的组时返回空。
func clusterIDOf(groupID string) string {
	id, ok := strings.CutPrefix(groupID, "cluster-")
	if !ok {
		return ""
	}
	return id
}

// onEvent 处理同步库的事件。它在库的协程里被调用，不能阻塞，也不能抢 Manager.mu。
func (m *Manager) onEvent(generation int64, e client.Event) {
	if generation != m.generation.Load() {
		return // 已被替换掉的旧节点
	}
	clusterID := clusterIDOf(e.GroupID)
	notice := Notice{Time: time.Now(), ClusterID: clusterID, Path: e.Path, ConflictPath: e.ConflictPath}

	switch e.Kind {
	case client.EventFileApplied:
		// 同步库以本程序的身份落盘，Linux 上降权运行的游戏改不了、删不掉它。
		// 事件里只有任务 id 没有路径，集群目录又只有几个小文件，所以整棵交还；
		// 已经属于运行时用户的文件不会被改动。Windows 上是空操作。
		if clusterID != "" {
			if err := runner.ChownTreeForRuntime(clusterDir(m.baseDir, clusterID)); err != nil {
				logger.Warnf("%s集群 %s 的文件交还运行时用户失败: %v", logPrefix, clusterID, err)
			}
		}
		return
	case client.EventRemoteCommand:
		// 只记日志，作为"协调端要求本机做过什么"的审计痕迹；不打扰用户。
		if e.Err != nil {
			logger.Warnf("%s拒绝或未能执行协调端的远程指令 %s（%s）: %v", logPrefix, e.Command, e.CommandID, e.Err)
		} else {
			logger.Infof("%s执行了协调端的远程指令 %s（%s）", logPrefix, e.Command, e.CommandID)
		}
		return

	case client.EventSessionConnected:
		notice.Kind, notice.Level, notice.Message = "connected", "info", "已连接协调端"
	case client.EventSessionDisconnected:
		notice.Kind, notice.Level, notice.Message = "disconnected", "warning", "与协调端的连接已断开，正在重连"
	case client.EventEnrolled:
		notice.Kind, notice.Level, notice.Message = "enrolled", "info", "本机已接入集群，取得节点证书"
	case client.EventCertificateRenewed:
		notice.Kind, notice.Level, notice.Message = "certificate_renewed", "info", "节点证书已自动续期"
	case client.EventCertificateExpiringSoon:
		// 节点证书到期前 90 天会自动续期；走到这里通常意味着续期一直失败。
		notice.Kind, notice.Level, notice.Message = "certificate_expiring", "warning", "节点证书即将到期"
	case client.EventAuthenticationFailed:
		notice.Kind, notice.Level, notice.Message = "authentication_failed", "error",
			"协调端拒绝了本机的证书（可能已被吊销，或身份被重置），同步已停止"
	case client.EventNodeIDConflict:
		// 新信任模型下，同一 node_id 有两个活跃会话意味着节点私钥可能被复制到了另一台机器。
		notice.Kind, notice.Level, notice.Message = "node_id_conflict", "error",
			"另一台机器正以本机的身份在线（节点私钥可能被复制），同步已停止"
	case client.EventRootRejected:
		notice.Kind, notice.Level, notice.Message = "cluster_rejected", "error", "协调端拒绝了这个集群的同步"
	case client.EventRootFailed:
		notice.Kind, notice.Level, notice.Message = "cluster_failed", "error", "这个集群的同步已停止"
	case client.EventTaskFailed:
		notice.Kind, notice.Level, notice.Message = "task_failed", "warning", "一个同步任务失败"
	case client.EventConflictCopySaved:
		// 首期不挪走冲突副本，只记录并提示（§10-8）；游戏是否会把它当成角色待真机验证（§7-15）。
		notice.Kind, notice.Level, notice.Message = "conflict_copy_saved", "warning", "保存了一份冲突副本"
	case client.EventCoordinatorError:
		notice.Kind, notice.Level, notice.Message = "coordinator_error", "warning", "协调端返回了错误"
	default:
		return
	}
	if e.Err != nil {
		notice.Message += ": " + e.Err.Error()
	} else if e.Message != "" && e.Kind != client.EventConflictCopySaved {
		notice.Message += ": " + e.Message
	}
	if e.Kind == client.EventConflictCopySaved && e.Message != "" {
		notice.Message += "（原因: " + e.Message + "）"
	}

	line := logPrefix + notice.Message
	if clusterID != "" {
		line += " cluster=" + clusterID
	}
	if notice.Path != "" {
		line += " path=" + notice.Path
	}
	if notice.ConflictPath != "" {
		line += " conflict_copy=" + notice.ConflictPath
	}
	switch notice.Level {
	case "error":
		logger.Error(line)
	case "warning":
		logger.Warn(line)
	default:
		logger.Info(line)
	}

	notices.add(notice)
	realtime.BroadcastServerEventWithData(realtimeEventType, "", notice.Message, notice.Level, map[string]any{
		"kind": notice.Kind, "cluster_id": notice.ClusterID, "path": notice.Path, "conflict_path": notice.ConflictPath,
	})
}
