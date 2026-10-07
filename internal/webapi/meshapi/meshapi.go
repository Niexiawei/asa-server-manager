// Package meshapi 暴露管理器互控的 HTTP 接口（docs/REMOTE_MANAGER_MESH_PLAN.md §12 P3-7、P3-8）：
//
//   - /api/mesh/*：本机状态、配置、配对、邀请码、待批准申请。只读的三个（status / peers / hello）
//     要求「能使用远程控制」（control_role，默认 admin），其余写操作一律要求管理员；
//   - /api/peers/:id/fwd/*path：A 侧的转发入口，经 Peer.HTTP 隧道把请求交给对端的 Gin 路由。
//
// 两组都在 /api 下：A 的鉴权中间件只拦 /api 前缀（§7.2），写成 /peers/... 会让转发入口绕过鉴权。
// 经隧道进来的请求访问这两组一律 403（远程禁区，由 authapi 的中间件拦截）。
package meshapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"asa-server/internal/appconfig"
	"asa-server/internal/mesh"
	"asa-server/internal/webapi/apiresp"
	"asa-server/internal/webapi/authapi"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
	"asa-server/pkg/meshjoin"
)

const rpcTimeout = 15 * time.Second

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRouter(r *gin.Engine) {
	g := r.Group("/api/mesh")
	g.GET("/status", requireControl(), h.status)
	g.GET("/peers", requireControl(), h.listPeers)
	g.POST("/peers/:id/hello", requireControl(), h.hello)
	g.POST("/hello/:id", requireControl(), h.hello) // P1 的排障入口，保留为别名

	admin := g.Group("", authapi.RequireAdmin())
	admin.PUT("/config", h.updateConfig)
	admin.POST("/join", h.join)
	admin.POST("/join/preview", h.previewJoin)
	admin.POST("/restart", h.restart)
	admin.POST("/leave", h.leave)
	admin.POST("/enable", h.enable)
	admin.POST("/disable", h.disable)
	admin.PUT("/peers/:id", h.updatePeer)
	admin.DELETE("/peers/:id", h.forgetPeer)
	admin.POST("/pair", h.pair)
	admin.GET("/invites", h.listInvites)
	admin.POST("/invites", h.createInvite)
	admin.DELETE("/invites/:id", h.deleteInvite)
	admin.GET("/requests", h.listRequests)
	admin.POST("/requests/:id", h.approveRequest)
	admin.DELETE("/requests/:id", h.rejectRequest)

	r.Any("/api/peers/:id/fwd/*path", requireControl(), h.forward)
}

// requireControl：本机上谁能使用远程控制（D5）。A 关着鉴权时放行（页面上有警告：
// 任何能打开本页面的人都能控制已配对的机器）；内网免鉴权视同管理员（与 RequireAdmin 一致）。
func requireControl() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !appconfig.Get().Auth.Enabled || authapi.Bypassed(c) {
			c.Next()
			return
		}
		u := authapi.CurrentUser(c)
		need := mesh.RoleAdmin
		if m := mesh.GetGlobalManager(); m != nil {
			need = m.ControlRole()
		}
		if u == nil || (need == mesh.RoleAdmin && !u.IsAdmin()) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "没有使用远程控制的权限", "code": "forbidden"})
			return
		}
		c.Next()
	}
}

func manager(c *gin.Context) *mesh.Manager {
	m := mesh.GetGlobalManager()
	if m == nil {
		fail(c, http.StatusInternalServerError, errors.New("管理器互控模块未初始化"))
	}
	return m
}

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Data: data})
}

func fail(c *gin.Context, code int, err error) {
	c.JSON(code, apiresp.StatusResponse{Success: false, Message: err.Error(), Error: err.Error()})
}

// rpcFail 把与对端交互的错误映射成状态码。
func rpcFail(c *gin.Context, err error) {
	code := http.StatusBadGateway
	switch {
	case errors.Is(err, mesh.ErrNotRunning):
		code = http.StatusServiceUnavailable
	case errors.Is(err, meshjoin.ErrPrefix), errors.Is(err, meshjoin.ErrVersion),
		errors.Is(err, meshjoin.ErrChecksum), errors.Is(err, meshjoin.ErrMalformed):
		code = http.StatusBadRequest
	default:
		switch status.Code(err) {
		case codes.PermissionDenied:
			code = http.StatusForbidden
			err = errors.New(status.Convert(err).Message())
		case codes.ResourceExhausted:
			code = http.StatusTooManyRequests
			err = errors.New(status.Convert(err).Message())
		}
	}
	fail(c, code, err)
}

func parseID(c *gin.Context) (meshid.ID, bool) {
	id, err := meshid.ParseID(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return meshid.ID{}, false
	}
	return id, true
}

// apply 在配置变化后热应用：重新启动 mesh（未配置 / 已停用时停在停止状态，不算错误）。
func apply(m *mesh.Manager) error {
	if err := m.Reload(); err != nil && !errors.Is(err, mesh.ErrNotConfigured) {
		return err
	}
	return nil
}

// applyIfRunning 只在 mesh 正在运行时热应用：改配置不是启动 mesh 的理由（启停由页面顶部单独控制）。
// 返回是否应用了。
func applyIfRunning(m *mesh.Manager) (bool, error) {
	if !m.Running() {
		return false, nil
	}
	return true, apply(m)
}

// savedStatus 是「保存配置」类接口的响应：状态 + 这次保存是否已热应用（false = 下次启动时生效）。
type savedStatus struct {
	mesh.Status
	Applied bool `json:"applied"`
}

func (h *Handler) status(c *gin.Context) {
	if m := manager(c); m != nil {
		ok(c, m.Status())
	}
}

func (h *Handler) updateConfig(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	var p mesh.ConfigPatch
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if _, err := mesh.UpdateConfig(m.Dir(), p); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	applied, err := applyIfRunning(m)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, savedStatus{Status: m.Status(), Applied: applied})
}

func (h *Handler) join(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	var req struct {
		Blob string `json:"blob"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	// 只保存协调节点，不改启用开关（CLI 的 mesh join 才会顺带启用）。
	if _, err := mesh.SetCoordinator(m.Dir(), req.Blob); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	applied, err := applyIfRunning(m)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, savedStatus{Status: m.Status(), Applied: applied})
}

// joinPreview 是 POST /api/mesh/join/preview 的响应：解析出的协调节点信息。**不含网络密钥**。
type joinPreview struct {
	Addr          string `json:"addr"`
	CoordinatorID string `json:"coordinator_id"`
	NetworkID     string `json:"network_id"`
	HasSecret     bool   `json:"has_secret"`
}

// previewJoin 只解析 join blob，不写盘、不连接。
func (h *Handler) previewJoin(c *gin.Context) {
	var req struct {
		Blob string `json:"blob"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	j, err := meshjoin.ParseJoinBlob(req.Blob)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ok(c, joinPreview{Addr: j.Addr, CoordinatorID: j.Coordinator.String(), NetworkID: j.NetworkID, HasSecret: j.NetworkSecret != ""})
}

// restart 重启正在使用的 mesh（重新读取配置）。未启用时拒绝：启动走 /enable。
func (h *Handler) restart(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	if err := m.Reload(); err != nil {
		if errors.Is(err, mesh.ErrNotConfigured) {
			fail(c, http.StatusBadRequest, errors.New("管理器互控未启动"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, m.Status())
}

func (h *Handler) leave(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	if err := mesh.Leave(m.Dir()); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	_ = m.Stop()
	ok(c, m.Status())
}

func (h *Handler) enable(c *gin.Context)  { h.setEnabled(c, true) }
func (h *Handler) disable(c *gin.Context) { h.setEnabled(c, false) }

func (h *Handler) setEnabled(c *gin.Context, on bool) {
	m := manager(c)
	if m == nil {
		return
	}
	if _, err := mesh.SetEnabled(m.Dir(), on); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var err error
	if on {
		err = apply(m)
	} else {
		err = m.Stop()
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, m.Status())
}

func (h *Handler) listPeers(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	peers, err := m.Peers()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	ok(c, peers)
}

func (h *Handler) hello(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	id, valid := parseID(c)
	if !valid {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), rpcTimeout)
	defer cancel()
	res, err := m.Hello(ctx, id)
	if err != nil {
		rpcFail(c, err)
		return
	}
	ok(c, res)
}

func (h *Handler) updatePeer(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	id, valid := parseID(c)
	if !valid {
		return
	}
	var p mesh.PeerPatch
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := m.Store().SetPeer(id, p); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	rec, _ := m.Store().Peer(id)
	ok(c, rec)
}

func (h *Handler) forgetPeer(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	id, valid := parseID(c)
	if !valid {
		return
	}
	if err := m.Store().Forget(id); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, mesh.ErrPeerNotFound) {
			code = http.StatusNotFound
		}
		fail(c, code, err)
		return
	}
	ok(c, nil)
}

func (h *Handler) pair(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	var req struct {
		Invite string   `json:"invite"`
		NodeID string   `json:"node_id"`
		Addrs  []string `json:"addrs"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), rpcTimeout)
	defer cancel()
	var res *mesh.PairResult
	var err error
	switch {
	case strings.TrimSpace(req.Invite) != "":
		res, err = m.PairWithInvite(ctx, req.Invite)
	case req.NodeID != "":
		id, perr := meshid.ParseID(req.NodeID)
		if perr != nil {
			fail(c, http.StatusBadRequest, perr)
			return
		}
		res, err = m.RequestPair(ctx, id, req.Addrs)
	default:
		fail(c, http.StatusBadRequest, errors.New("需要邀请码，或对方的节点 ID"))
		return
	}
	if err != nil {
		rpcFail(c, err)
		return
	}
	ok(c, res)
}

// inviteView 是邀请码列表的一行：不含密钥哈希。
type inviteView struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func toInviteView(v mesh.InviteRecord) inviteView {
	return inviteView{ID: v.ID, Role: v.Role, Note: v.Note, CreatedAt: v.CreatedAt, ExpiresAt: v.ExpiresAt}
}

func (h *Handler) listInvites(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	d, err := m.Store().Snapshot()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]inviteView, 0, len(d.Invites))
	now := time.Now()
	for _, v := range d.Invites {
		if now.Before(v.ExpiresAt) {
			out = append(out, toInviteView(v))
		}
	}
	ok(c, out)
}

func (h *Handler) createInvite(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	var req struct {
		Role           string   `json:"role"`
		TTLSeconds     int      `json:"ttl_seconds"`
		Addrs          []string `json:"addrs"`
		WithLocalAddrs bool     `json:"with_local_addrs"`
		Note           string   `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	encoded, rec, err := m.CreateInvite(mesh.InviteOptions{
		Role: req.Role, TTL: time.Duration(req.TTLSeconds) * time.Second, Addrs: req.Addrs, Note: req.Note,
	}, req.WithLocalAddrs)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	logger.Infof("[mesh] %s 生成了邀请码 %s（角色 %s，%s 到期）", authapi.ActorName(c), rec.ID, rec.Role, rec.ExpiresAt.Format(time.DateTime))
	// 整串只在这里出现一次：存储里只有哈希，以后再也拿不到。
	ok(c, gin.H{"invite": encoded, "record": toInviteView(rec)})
}

func (h *Handler) deleteInvite(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	if err := m.Store().DeleteInvite(c.Param("id")); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, mesh.ErrInviteNotFound) {
			code = http.StatusNotFound
		}
		fail(c, code, err)
		return
	}
	ok(c, nil)
}

func (h *Handler) listRequests(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	d, err := m.Store().Snapshot()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if d.Requests == nil {
		d.Requests = []mesh.RequestRecord{}
	}
	ok(c, d.Requests)
}

func (h *Handler) approveRequest(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	id, valid := parseID(c)
	if !valid {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := m.Store().ApproveRequest(id, req.Role); err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, mesh.ErrRequestNotFound) {
			code = http.StatusNotFound
		}
		fail(c, code, err)
		return
	}
	logger.Infof("[mesh] %s 批准了 %s 的配对申请，授予 %s", authapi.ActorName(c), id.Short(), req.Role)
	ok(c, nil)
}

func (h *Handler) rejectRequest(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	id, valid := parseID(c)
	if !valid {
		return
	}
	if err := m.Store().RejectRequest(id); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, mesh.ErrRequestNotFound) {
			code = http.StatusNotFound
		}
		fail(c, code, err)
		return
	}
	ok(c, nil)
}
