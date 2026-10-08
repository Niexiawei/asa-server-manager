package mesh

import (
	"sync"
	"sync/atomic"
	"time"

	"asa-server/pkg/meshid"
)

// relayMeter 按对端累计本机经中转收发的字节（§12 P7-4）。计在 streamconn 那一层，也就是 TLS 之下的密文字节，
// 与协调节点看到的数字一致。只在内存：mesh 每次启动重新计（coordClient 随 Start 新建），长期账单看协调节点的
// asa-coordinator relay stats。
type relayMeter struct {
	since time.Time

	mu    sync.Mutex
	peers map[meshid.ID]*relayCounter
}

type relayCounter struct {
	sent, received atomic.Int64
}

// RelayBytes 是经中转收发的字节数。
type RelayBytes struct {
	Sent     int64 `json:"sent"`
	Received int64 `json:"received"`
}

// RelayTraffic 是本次启动以来经中转收发的合计。
type RelayTraffic struct {
	RelayBytes
	Since time.Time `json:"since,omitzero"` // 起算时间（mesh 启动时刻）；没有协调节点时为空
}

func newRelayMeter() *relayMeter {
	return &relayMeter{since: time.Now(), peers: map[meshid.ID]*relayCounter{}}
}

func (m *relayMeter) counter(peer meshid.ID) *relayCounter {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.peers[peer]
	if c == nil {
		c = &relayCounter{}
		m.peers[peer] = c
	}
	return c
}

func (m *relayMeter) peer(id meshid.ID) RelayBytes {
	m.mu.Lock()
	c := m.peers[id]
	m.mu.Unlock()
	if c == nil {
		return RelayBytes{}
	}
	return RelayBytes{Sent: c.sent.Load(), Received: c.received.Load()}
}

func (m *relayMeter) total() RelayTraffic {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := RelayTraffic{Since: m.since}
	for _, c := range m.peers {
		t.Sent += c.sent.Load()
		t.Received += c.received.Load()
	}
	return t
}
