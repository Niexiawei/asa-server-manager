package mesh

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
	"asa-server/pkg/upnp"
)

// UPnP 端口映射（§12「P6 补充：UPnP 端口映射」）：在本机所在的路由器上打开 Peer 端口（TCP）与打洞端口（UDP）。
// 映射有效时外部地址作为 MAPPED 候选——TCP 随登记上报（对方第一次拨号就能直连），UDP 排在打洞候选的首位
// （本机是对称型 NAT 也能打通）。协议实现在 pkg/upnp（改编自 Syncthing）；续约、选端口、上级 NAT 的识别在这里。

const (
	upnpDiscoverTimeout = 3 * time.Second
	upnpCallTimeout     = 5 * time.Second
	upnpLease           = time.Hour
	// upnpRenew 是续约间隔（租期的一半）。只支持永久映射的路由器也按它复查一次（路由器重启会丢映射）。
	upnpRenew = 30 * time.Minute
	// upnpRetry 是没找到网关 / 出错之后多久再试。
	upnpRetry = 30 * time.Minute
	// upnpPortTries：内部端口同号被占之后，按确定性序列再试的外部端口数（照 Syncthing）。
	upnpPortTries     = 10
	upnpDeleteTimeout = 2 * time.Second
)

// 状态接口里的 upnp.state。
const (
	upnpStopped   = "stopped"
	upnpDisabled  = "disabled"
	upnpSearching = "searching"
	upnpNotFound  = "not_found"
	upnpDoubleNAT = "double_nat"
	upnpMapped    = "mapped"
	upnpError     = "error"
)

// UPnPStatus 是状态里的端口映射一节。
type UPnPStatus struct {
	State      string        `json:"state"`
	Gateway    string        `json:"gateway,omitempty"`
	ExternalIP string        `json:"external_ip,omitempty"`
	Mappings   []UPnPMapping `json:"mappings"`
	Error      string        `json:"error,omitempty"`
	CheckedAt  time.Time     `json:"checked_at,omitzero"`
}

// UPnPMapping 是一条已生效的映射。
type UPnPMapping struct {
	Protocol     string `json:"protocol"` // TCP / UDP
	External     string `json:"external"` // 外部地址 ip:port
	InternalPort int    `json:"internal_port"`
	Permanent    bool   `json:"permanent"` // 路由器只支持永久映射；mesh 停止时删除
}

type portReq struct {
	proto    upnp.Protocol
	internal int
}

type portMapping struct {
	proto    upnp.Protocol
	internal int
	external int
	lease    time.Duration // 0 = 永久
}

// portMapper 维护本机在路由器上的映射：发现网关 → 判断上级有没有 NAT → 逐个端口映射 → 定期续约 → Stop 时删除。
type portMapper struct {
	self     meshid.ID
	reqs     []portReq
	discover func(ctx context.Context) []*upnp.Gateway
	// srflxIPs 返回 STUN 看到的本机出口 IP（没有协调节点或还没测到时为空）。
	srflxIPs func() []netip.Addr
	// allowNonPublic 让私网 / 回环的 WAN 地址也算有效（只给测试用）。
	allowNonPublic bool
	// onChange 在宣称的映射地址变化时调用（重新上报候选）。
	onChange func()
	trigger  chan struct{}
	done     chan struct{}

	mu        sync.Mutex
	gw        *upnp.Gateway
	state     string
	extIP     netip.Addr
	mappings  []portMapping
	err       string
	checkedAt time.Time
}

func newPortMapper(self meshid.ID, reqs []portReq, discover func(context.Context) []*upnp.Gateway,
	srflxIPs func() []netip.Addr, onChange func()) *portMapper {
	if discover == nil {
		discover = discoverGateways
	}
	return &portMapper{self: self, reqs: reqs, discover: discover, srflxIPs: srflxIPs, onChange: onChange,
		trigger: make(chan struct{}, 1), done: make(chan struct{}), state: upnpSearching}
}

// discoverGateways 是默认的发现：SSDP，只留本机地址落在上报中的网卡上的网关
// （排除 Docker / Hyper-V 等虚拟网卡后面的「网关」）。
func discoverGateways(ctx context.Context) []*upnp.Gateway {
	local := hostAddrs(systemInterfaces())
	var out []*upnp.Gateway
	for _, g := range upnp.Discover(ctx, upnpDiscoverTimeout) {
		if ip, ok := netip.AddrFromSlice(g.LocalIPv4.To4()); ok && slices.Contains(local, ip) {
			out = append(out, g)
		}
	}
	return out
}

// kick 请求立即重新检查一次（网卡变化、STUN 结果变化时）。
func (pm *portMapper) kick() {
	select {
	case pm.trigger <- struct{}{}:
	default:
	}
}

// run 循环到 ctx 结束；之后由 close 删除映射。
func (pm *portMapper) run(ctx context.Context) {
	defer close(pm.done)
	for {
		wait := pm.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-pm.trigger:
		case <-time.After(wait):
		}
	}
}

// close 等 run 退出后删除全部映射（ctx 已取消；这里另给 upnpDeleteTimeout）。
func (pm *portMapper) close() {
	<-pm.done
	pm.mu.Lock()
	gw, ms := pm.gw, pm.mappings
	pm.gw, pm.mappings, pm.state = nil, nil, upnpStopped
	pm.mu.Unlock()
	if gw == nil || len(ms) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), upnpDeleteTimeout)
	defer cancel()
	deleteMappings(ctx, gw, ms)
	logger.Infof("[mesh] 已删除路由器上的 %d 条 UPnP 映射", len(ms))
}

func deleteMappings(ctx context.Context, gw *upnp.Gateway, ms []portMapping) {
	for _, m := range ms {
		if err := gw.DeletePortMapping(ctx, m.proto, m.external); err != nil {
			logger.Debugf("[mesh] 删除 UPnP 映射 %s %d 失败：%v", m.proto, m.external, err)
		}
	}
}

// mapped 返回 proto 的有效外部地址（只在映射有效时）。
func (pm *portMapper) mapped(proto upnp.Protocol) []netip.AddrPort {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.state != upnpMapped {
		return nil
	}
	var out []netip.AddrPort
	for _, m := range pm.mappings {
		if m.proto == proto {
			out = append(out, netip.AddrPortFrom(pm.extIP, uint16(m.external)))
		}
	}
	return out
}

func (pm *portMapper) status() UPnPStatus {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	st := UPnPStatus{State: pm.state, Error: pm.err, CheckedAt: pm.checkedAt, Mappings: []UPnPMapping{}}
	if pm.gw != nil {
		st.Gateway = pm.gw.FriendlyName
	}
	if pm.extIP.IsValid() {
		st.ExternalIP = pm.extIP.String()
	}
	if pm.state == upnpMapped {
		for _, m := range pm.mappings {
			st.Mappings = append(st.Mappings, UPnPMapping{Protocol: string(m.proto),
				External: netip.AddrPortFrom(pm.extIP, uint16(m.external)).String(), InternalPort: m.internal, Permanent: m.lease == 0})
		}
	}
	return st
}

// refresh 做一轮：发现 → 判断 → 映射 / 续约。返回下一轮的等待时间。
func (pm *portMapper) refresh(ctx context.Context) time.Duration {
	// 发现本身按网卡限时，但取设备描述是 HTTP：卡住的路由器不能把这一轮拖住。
	dctx, dcancel := context.WithTimeout(ctx, upnpDiscoverTimeout+upnpCallTimeout)
	gws := pm.discover(dctx)
	dcancel()
	if ctx.Err() != nil {
		return 0
	}
	pm.mu.Lock()
	oldGW, oldMaps, oldIP := pm.gw, pm.mappings, pm.extIP
	pm.mu.Unlock()

	gw := pickGateway(gws, oldGW)
	if oldGW != nil && (gw == nil || gw.ID() != oldGW.ID()) {
		// 换了网关（或网关不见了）：旧映射尽力删掉。
		dctx, cancel := context.WithTimeout(ctx, upnpDeleteTimeout)
		deleteMappings(dctx, oldGW, oldMaps)
		cancel()
		oldMaps = nil
	}
	if gw == nil {
		pm.set(nil, upnpNotFound, netip.Addr{}, nil, "", oldIP)
		return upnpRetry
	}

	cctx, cancel := context.WithTimeout(ctx, upnpCallTimeout)
	ip, err := gw.GetExternalIPv4Address(cctx)
	cancel()
	if err != nil {
		pm.set(gw, upnpError, netip.Addr{}, oldMaps, "读取路由器的 WAN 地址失败："+err.Error(), oldIP)
		return upnpRetry
	}
	extIP, _ := netip.AddrFromSlice(ip.To4())
	if reason := pm.unusable(extIP); reason != "" {
		// 上级还有 NAT：映射出来的端口在公网上不可达，删掉、不宣称。
		dctx, cancel := context.WithTimeout(ctx, upnpDeleteTimeout)
		deleteMappings(dctx, gw, oldMaps)
		cancel()
		pm.set(gw, upnpDoubleNAT, extIP, nil, reason, oldIP)
		return upnpRenew
	}

	var maps []portMapping
	var errs []error
	for _, r := range pm.reqs {
		var prev *portMapping
		for i := range oldMaps {
			if oldMaps[i].proto == r.proto && oldMaps[i].internal == r.internal {
				prev = &oldMaps[i]
			}
		}
		m, err := pm.mapOne(ctx, gw, r, prev)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %d：%w", r.proto, r.internal, err))
			continue
		}
		maps = append(maps, m)
	}
	if ctx.Err() != nil {
		return 0
	}
	state, msg := upnpMapped, ""
	if len(errs) > 0 {
		msg = errors.Join(errs...).Error()
		if len(maps) == 0 {
			state = upnpError
		}
	}
	pm.set(gw, state, extIP, maps, msg, oldIP)
	return upnpRenew
}

// set 记下一轮的结果；宣称的地址变了就通知，并在结论变化时记日志。
func (pm *portMapper) set(gw *upnp.Gateway, state string, extIP netip.Addr, maps []portMapping, msg string, oldIP netip.Addr) {
	pm.mu.Lock()
	prevState, prevMaps := pm.state, pm.mappings
	wasMapped := prevState == upnpMapped
	pm.gw, pm.state, pm.extIP, pm.mappings, pm.err, pm.checkedAt = gw, state, extIP, maps, msg, time.Now()
	pm.mu.Unlock()

	changed := wasMapped != (state == upnpMapped) || oldIP != extIP || !slices.Equal(prevMaps, maps)
	if prevState != state || changed {
		switch state {
		case upnpMapped:
			logger.Infof("[mesh] UPnP：路由器 %s 已映射 %s", gw.FriendlyName, describeMappings(extIP, maps))
		case upnpNotFound:
			logger.Infof("[mesh] UPnP：未发现支持 UPnP 的路由器")
		default:
			logger.Warnf("[mesh] UPnP：%s（%s）", state, msg)
		}
	}
	if changed && pm.onChange != nil {
		pm.onChange()
	}
}

func describeMappings(ip netip.Addr, maps []portMapping) string {
	parts := make([]string, 0, len(maps))
	for _, m := range maps {
		parts = append(parts, fmt.Sprintf("%s %s→%d", m.proto, netip.AddrPortFrom(ip, uint16(m.external)), m.internal))
	}
	return strings.Join(parts, "，")
}

// unusable 判断 WAN 地址能不能让外面连进来；不能时返回原因。
func (pm *portMapper) unusable(ip netip.Addr) string {
	if !ip.IsValid() {
		return "路由器没有报出 WAN 地址"
	}
	if ip.IsUnspecified() {
		// miniupnpd（OpenWrt 等）在自己的 WAN 是私网地址时就报 0.0.0.0（2026-10-07 本机实测即如此）。
		return "路由器报出的 WAN 地址是 0.0.0.0：它自己没有公网地址（上级还有 NAT，或宽带没有公网 IPv4），映射对外无效"
	}
	if !pm.allowNonPublic && (isLANAddr(ip) || !ip.IsGlobalUnicast()) {
		return fmt.Sprintf("路由器的 WAN 地址 %s 不是公网地址：上级还有 NAT（运营商级 NAT 或光猫 + 路由器双重 NAT），映射对外无效", ip)
	}
	if pm.srflxIPs != nil {
		var v4 []netip.Addr
		for _, a := range pm.srflxIPs() {
			if a.Unmap().Is4() {
				v4 = append(v4, a.Unmap())
			}
		}
		if len(v4) > 0 && !slices.Contains(v4, ip) {
			return fmt.Sprintf("路由器的 WAN 地址 %s 与 STUN 看到的出口 %s 不同：出口不是这台路由器，映射对外无效", ip, v4[0])
		}
	}
	return ""
}

// UPnPVerdict 判断路由器报出的 WAN 地址能否让外面连进来（CLI 诊断用，不比对 STUN）。
func UPnPVerdict(ip net.IP) (ok bool, reason string) {
	a, _ := netip.AddrFromSlice(ip.To4())
	reason = (&portMapper{}).unusable(a)
	return reason == "", reason
}

// pickGateway 优先沿用当前网关，其次 IGDv2。
func pickGateway(gws []*upnp.Gateway, cur *upnp.Gateway) *upnp.Gateway {
	if len(gws) == 0 {
		return nil
	}
	if cur != nil {
		for _, g := range gws {
			if g.ID() == cur.ID() {
				return g
			}
		}
	}
	for _, g := range gws {
		if g.IsV2() {
			return g
		}
	}
	return gws[0]
}

// mapOne 映射一个端口。外部端口依次试：上次的 → 与内部同号 → 确定性序列（种子 = 本机 ID + 协议 + 内部端口 + 网关，
// 重启后还是同一批，崩溃残留的映射会被这一次续约而不是越积越多）。只有 718（端口被别的机器占了）才换端口。
func (pm *portMapper) mapOne(ctx context.Context, gw *upnp.Gateway, r portReq, prev *portMapping) (portMapping, error) {
	desc := "asa-server mesh " + pm.self.Short()
	var lastErr error
	for _, ext := range pm.candidatePorts(gw, r, prev) {
		cctx, cancel := context.WithTimeout(ctx, upnpCallTimeout)
		lease, err := gw.AddPortMapping(cctx, r.proto, r.internal, ext, desc, upnpLease)
		cancel()
		if err == nil {
			return portMapping{proto: r.proto, internal: r.internal, external: ext, lease: lease}, nil
		}
		lastErr = err
		var se *upnp.SOAPError
		if !errors.As(err, &se) || se.Code != upnp.ErrCodeConflict || ctx.Err() != nil {
			return portMapping{}, err
		}
	}
	return portMapping{}, lastErr
}

func (pm *portMapper) candidatePorts(gw *upnp.Gateway, r portReq, prev *portMapping) []int {
	var out []int
	add := func(p int) {
		if p > 0 && p <= 65535 && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if prev != nil {
		add(prev.external)
	}
	add(r.internal)
	h := fnv.New64a()
	fmt.Fprintf(h, "%s/%s/%d/%s", pm.self.Compact(), r.proto, r.internal, gw.ID())
	seed := h.Sum64()
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	for range upnpPortTries {
		add(1024 + rng.IntN(65535-1024))
	}
	return out
}
