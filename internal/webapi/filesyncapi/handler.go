// Package filesyncapi 暴露文件同步（internal/filesyncmanage）的 HTTP 接口：
// 配置读写、接入字符串预览、状态与状态流、启停与身份重置、可选的集群列表。
// 写操作一律要求管理员——配置里有引导私钥。
// 见 docs/FILESYNC_REPLACE_SYNCTHING_PLAN.md §8 P3。
package filesyncapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	cfgpkg "asa-server/internal/config"
	"asa-server/internal/filesyncmanage"
	"asa-server/internal/webapi/apiresp"
	"asa-server/internal/webapi/authapi"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRouter(r *gin.Engine) {
	g := r.Group("/api/filesync")
	g.GET("/config", h.getConfig)
	g.PUT("/config", authapi.RequireAdmin(), h.updateConfig)
	g.POST("/join-blob/inspect", authapi.RequireAdmin(), h.inspectJoinBlob)
	g.GET("/status", h.status)
	g.GET("/status/stream", h.streamStatus)
	g.POST("/start", authapi.RequireAdmin(), h.start)
	g.POST("/stop", authapi.RequireAdmin(), h.stop)
	g.POST("/restart", authapi.RequireAdmin(), h.restart)
	g.POST("/identity/reset", authapi.RequireAdmin(), h.resetIdentity)
	g.GET("/clusters", h.clusters)
}

func manager(c *gin.Context) *filesyncmanage.Manager {
	m := filesyncmanage.GetGlobalManager()
	if m == nil {
		fail(c, http.StatusInternalServerError, errors.New("文件同步模块未初始化"))
	}
	return m
}

func fail(c *gin.Context, code int, err error) {
	c.JSON(code, apiresp.StatusResponse{Success: false, Message: err.Error(), Error: err.Error()})
}

func ok(c *gin.Context, message string, data any) {
	c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Message: message, Data: data})
}

// configView 是 GET /config 的返回：**不含任何 PEM 内容**。私钥自不必说；CA 与证书
// 虽然不是机密，回传它们也没有用处，前端要的只是"有没有、连哪里、何时到期"。
type configView struct {
	Configured        bool                              `json:"configured"`
	Enabled           bool                              `json:"enabled"`
	Address           string                            `json:"address"`
	Label             string                            `json:"label"`
	HasCA             bool                              `json:"has_ca"`
	HasBootstrap      bool                              `json:"has_bootstrap"`
	Credential        *filesyncmanage.CredentialPreview `json:"credential,omitempty"`
	UploadLimitKBps   int64                             `json:"upload_limit_kbps"`
	DownloadLimitKBps int64                             `json:"download_limit_kbps"`
	Clusters          []filesyncmanage.ClusterRoot      `json:"clusters"`
}

func (h *Handler) getConfig(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	cfg := m.Config()
	if cfg == nil {
		ok(c, "文件同步尚未配置", configView{Clusters: []filesyncmanage.ClusterRoot{}})
		return
	}
	view := configView{
		Configured: true, Enabled: cfg.Enabled, Address: cfg.Address, Label: cfg.Label,
		HasCA: cfg.CAPEM != "", HasBootstrap: cfg.BootstrapCertPEM != "" && cfg.BootstrapKeyPEM != "",
		UploadLimitKBps: cfg.UploadLimitKBps, DownloadLimitKBps: cfg.DownloadLimitKBps, Clusters: cfg.Clusters,
	}
	if view.Clusters == nil {
		view.Clusters = []filesyncmanage.ClusterRoot{}
	}
	if p, has := cfg.CredentialPreview(); has {
		view.Credential = &p
	}
	ok(c, "", view)
}

// updateRequest 是 PUT /config 的请求体。
//
// 凭据两种填法（§10-7），**二选一**：
//   - join_blob：解开后覆盖地址与三份 PEM；
//   - ca_pem / bootstrap_cert_pem / bootstrap_key_pem：逐项覆盖，留空表示不修改。
type updateRequest struct {
	Enabled           bool                         `json:"enabled"`
	Address           string                       `json:"address"`
	Label             string                       `json:"label"`
	UploadLimitKBps   int64                        `json:"upload_limit_kbps"`
	DownloadLimitKBps int64                        `json:"download_limit_kbps"`
	Clusters          []filesyncmanage.ClusterRoot `json:"clusters"`

	JoinBlob         string `json:"join_blob"`
	CAPEM            string `json:"ca_pem"`
	BootstrapCertPEM string `json:"bootstrap_cert_pem"`
	BootstrapKeyPEM  string `json:"bootstrap_key_pem"`
}

// buildConfig 把请求合并到当前配置上（current 为 nil 表示首次配置）。
func buildConfig(current *filesyncmanage.Config, req updateRequest) (*filesyncmanage.Config, error) {
	if req.JoinBlob != "" && (req.CAPEM != "" || req.BootstrapCertPEM != "" || req.BootstrapKeyPEM != "") {
		return nil, errors.New("接入字符串与证书文件只能选一种")
	}
	next := &filesyncmanage.Config{}
	if current != nil {
		*next = *current
	}
	next.Enabled, next.Address, next.Label = req.Enabled, req.Address, req.Label
	next.UploadLimitKBps, next.DownloadLimitKBps = req.UploadLimitKBps, req.DownloadLimitKBps
	next.Clusters = req.Clusters
	if req.JoinBlob != "" {
		if err := next.ApplyJoinBlob(req.JoinBlob); err != nil {
			return nil, err
		}
		return next, nil
	}
	if req.CAPEM != "" {
		next.CAPEM = req.CAPEM
	}
	if req.BootstrapCertPEM != "" {
		next.BootstrapCertPEM = req.BootstrapCertPEM
	}
	if req.BootstrapKeyPEM != "" {
		next.BootstrapKeyPEM = req.BootstrapKeyPEM
	}
	return next, nil
}

func (h *Handler) updateConfig(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	var req updateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, fmt.Errorf("请求格式错误: %w", err))
		return
	}
	next, err := buildConfig(m.Config(), req)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := m.SetConfig(next); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ok(c, "文件同步配置已保存", nil)
}

func (h *Handler) inspectJoinBlob(c *gin.Context) {
	var req struct {
		JoinBlob string `json:"join_blob"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.JoinBlob == "" {
		fail(c, http.StatusBadRequest, errors.New("缺少接入字符串"))
		return
	}
	preview, err := filesyncmanage.InspectJoinBlob(req.JoinBlob)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ok(c, "", preview)
}

func (h *Handler) status(c *gin.Context) {
	if m := manager(c); m != nil {
		ok(c, "", m.Status())
	}
}

// streamStatus 用 SSE 推送 Status，与 /status 同一个构造。
// 在途传输的进度每秒都在变，所以有内容变化就推；没变化时隔一段发心跳注释帧，
// 防止反代按空闲超时掐断连接（同 frp 的状态流）。
func (h *Handler) streamStatus(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	const (
		pollInterval      = 2 * time.Second
		heartbeatInterval = 25 * time.Second
	)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var last []byte
	lastSent := time.Now()
	if payload, err := json.Marshal(m.Status()); err == nil {
		last = payload
		fmt.Fprintf(c.Writer, "data: %s\n\n", payload)
		c.Writer.Flush()
	}
	c.Stream(func(w io.Writer) bool {
		select {
		case <-ticker.C:
			payload, err := json.Marshal(m.Status())
			if err != nil {
				return true
			}
			if !bytes.Equal(payload, last) {
				last, lastSent = payload, time.Now()
				fmt.Fprintf(w, "data: %s\n\n", payload)
				return true
			}
			if time.Since(lastSent) >= heartbeatInterval {
				lastSent = time.Now()
				fmt.Fprint(w, ": ping\n\n")
			}
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}

func (h *Handler) start(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	if err := m.Start(); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ok(c, "文件同步已启动", nil)
}

func (h *Handler) stop(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	if err := m.Stop(); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ok(c, "文件同步已停止", nil)
}

func (h *Handler) restart(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	if err := m.Restart(); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ok(c, "文件同步已重启", nil)
}

func (h *Handler) resetIdentity(c *gin.Context) {
	m := manager(c)
	if m == nil {
		return
	}
	if err := m.ResetIdentity(); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ok(c, "已删除本机节点证书，将用引导凭据重新接入", nil)
}

// clusterOption 是集群下拉框的一项：一个 ClusterID，以及哪些实例在用它。
type clusterOption struct {
	ClusterID string   `json:"cluster_id"`
	Instances []string `json:"instances"`
}

// clusters 从各实例配置里收集已有的 ClusterID，供前端下拉选择——
// 用户不必手敲、也就不会敲错一个与实例对不上的 ID。
func (h *Handler) clusters(c *gin.Context) {
	names, err := cfgpkg.GetAvailableInstances()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	byID := map[string][]string{}
	for _, name := range names {
		cfg, err := cfgpkg.LoadInstanceConfig(name)
		if err != nil || cfg.ClusterID == "" {
			continue
		}
		byID[cfg.ClusterID] = append(byID[cfg.ClusterID], name)
	}
	options := make([]clusterOption, 0, len(byID))
	for id, instances := range byID {
		sort.Strings(instances)
		options = append(options, clusterOption{ClusterID: id, Instances: instances})
	}
	sort.Slice(options, func(i, j int) bool { return options[i].ClusterID < options[j].ClusterID })
	ok(c, "", options)
}
