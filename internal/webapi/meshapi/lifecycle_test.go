package meshapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"asa-server/internal/appconfig"
	"asa-server/internal/auth"
	"asa-server/internal/mesh"
	"asa-server/internal/webapi/authapi"
	"asa-server/pkg/meshjoin"
)

// 页面上协调节点只是一项配置：保存 / 替换不启动 mesh，启停由 enable / disable / restart 单独控制。

func lifecycleServer(t *testing.T, m *mesh.Manager) *httptest.Server {
	t.Helper()
	t.Cleanup(mesh.SetGlobalManagerForTest(m))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(authapi.Middleware())
	NewHandler().RegisterRouter(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

type apiResp struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func postJSON(t *testing.T, srv *httptest.Server, path string, body any, mutate func(*http.Request)) (int, apiResp, string) {
	t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest("POST", srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if mutate != nil {
		mutate(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var out apiResp
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out, string(b)
}

type savedResp struct {
	Running       bool   `json:"running"`
	Enabled       bool   `json:"enabled"`
	Applied       bool   `json:"applied"`
	Coordinator   string `json:"coordinator"`
	CoordinatorID string `json:"coordinator_id"`
	NetworkID     string `json:"network_id"`
}

func TestJoinSavesWithoutStarting(t *testing.T) {
	setupConfig(t, "auth:\n  enabled: false\n")
	blob := startCoord(t)
	jb, err := meshjoin.ParseJoinBlob(blob)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	noListen := true
	if _, err := mesh.UpdateConfig(dir, mesh.ConfigPatch{NoListen: &noListen}); err != nil {
		t.Fatal(err)
	}
	m := mesh.New(mesh.Options{Dir: dir, BackoffMin: 50 * time.Millisecond})
	t.Cleanup(func() { m.Stop() })
	srv := lifecycleServer(t, m)

	// 停止状态下保存：不启动，applied=false；状态里能看到解析出的协调节点信息。
	code, out, raw := postJSON(t, srv, "/api/mesh/join", map[string]string{"blob": blob}, nil)
	var st savedResp
	_ = json.Unmarshal(out.Data, &st)
	if code != 200 || st.Running || st.Enabled || st.Applied {
		t.Fatalf("停止状态下保存协调节点不该启动 mesh：%d %s", code, raw)
	}
	if st.Coordinator != jb.Addr || st.CoordinatorID != jb.Coordinator.String() || st.NetworkID != jb.NetworkID {
		t.Fatalf("状态里的协调节点信息不对：%s", raw)
	}

	// restart 在未启动时拒绝。
	if code, _, raw := postJSON(t, srv, "/api/mesh/restart", nil, nil); code != 400 {
		t.Fatalf("未启动时 restart 应 400：%d %s", code, raw)
	}

	// 启动 → 运行中；此时再保存（替换）会热应用。
	if code, _, raw := postJSON(t, srv, "/api/mesh/enable", nil, nil); code != 200 || !m.Running() {
		t.Fatalf("启动失败：%d %s", code, raw)
	}
	code, out, raw = postJSON(t, srv, "/api/mesh/join", map[string]string{"blob": blob}, nil)
	_ = json.Unmarshal(out.Data, &st)
	if code != 200 || !st.Running || !st.Applied {
		t.Fatalf("运行中替换协调节点应热应用：%d %s", code, raw)
	}
	if code, _, raw := postJSON(t, srv, "/api/mesh/restart", nil, nil); code != 200 || !m.Running() {
		t.Fatalf("运行中 restart 失败：%d %s", code, raw)
	}

	// 停止后改配置只保存，不把 mesh 拉起来。
	if code, _, raw := postJSON(t, srv, "/api/mesh/disable", nil, nil); code != 200 || m.Running() {
		t.Fatalf("停止失败：%d %s", code, raw)
	}
	req, _ := http.NewRequest("PUT", srv.URL+"/api/mesh/config", strings.NewReader(`{"label":"机房-1"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var cfgOut apiResp
	_ = json.NewDecoder(resp.Body).Decode(&cfgOut)
	resp.Body.Close()
	_ = json.Unmarshal(cfgOut.Data, &st)
	if resp.StatusCode != 200 || st.Running || st.Applied {
		t.Fatalf("停止状态下改配置不该启动 mesh：%d %+v", resp.StatusCode, st)
	}
}

func TestJoinPreview(t *testing.T) {
	setupConfig(t, "auth:\n  enabled: false\n")
	blob := startCoord(t)
	jb, _ := meshjoin.ParseJoinBlob(blob)
	dir := t.TempDir()
	m := mesh.New(mesh.Options{Dir: dir})
	srv := lifecycleServer(t, m)

	code, out, raw := postJSON(t, srv, "/api/mesh/join/preview", map[string]string{"blob": blob}, nil)
	var p joinPreview
	_ = json.Unmarshal(out.Data, &p)
	if code != 200 || p.Addr != jb.Addr || p.CoordinatorID != jb.Coordinator.String() || p.NetworkID != jb.NetworkID || !p.HasSecret {
		t.Fatalf("预览结果不对：%d %s", code, raw)
	}
	if strings.Contains(raw, jb.NetworkSecret) {
		t.Fatalf("预览响应不该含网络密钥：%s", raw)
	}
	// 预览不写盘。
	if st := m.Status(); st.Coordinator != "" {
		t.Fatalf("预览不该保存配置：%+v", st)
	}
	// 截断的串：400，且报错是明确的校验失败。
	if code, _, raw := postJSON(t, srv, "/api/mesh/join/preview", map[string]string{"blob": blob[:len(blob)-6]}, nil); code != 400 {
		t.Fatalf("截断的 join blob 应 400：%d %s", code, raw)
	}
}

// 新增的三个写接口都只有管理员能调。
func TestLifecycleRequiresAdmin(t *testing.T) {
	setupConfig(t, "auth:\n  enabled: true\n")
	am := auth.GetManager()
	op, err := am.CreateUser(context.Background(), "oper", "correct-horse-battery", auth.RoleOperator, auth.ActorCLI)
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := am.IssueSession(op, auth.StageFull, []string{"pwd"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	srv := lifecycleServer(t, mesh.New(mesh.Options{Dir: t.TempDir()}))
	cookie := &http.Cookie{Name: appconfig.Get().Auth.Session.CookieName, Value: tok}
	for _, path := range []string{"/api/mesh/join", "/api/mesh/join/preview", "/api/mesh/restart"} {
		code, _, raw := postJSON(t, srv, path, map[string]string{"blob": "x"}, func(r *http.Request) { r.AddCookie(cookie) })
		if code != 403 {
			t.Errorf("operator 调 %s 应 403：%d %s", path, code, raw)
		}
	}
}

// 打洞与 UPnP 的配置项（§12 P6-7、「P6 补充」）：经 PUT /config 保存、在状态里回显；越界的端口 400。
func TestPunchConfig(t *testing.T) {
	setupConfig(t, "auth:\n  enabled: false\n")
	srv := lifecycleServer(t, mesh.New(mesh.Options{Dir: t.TempDir()}))
	put := func(body string) (int, apiResp) {
		req, _ := http.NewRequest("PUT", srv.URL+"/api/mesh/config", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out apiResp
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	code, out := put(`{"no_punch":true,"udp_port":20000,"no_upnp":true}`)
	var st struct {
		NoPunch bool `json:"no_punch"`
		UDPPort int  `json:"udp_port"`
		Punch   struct {
			Active  bool   `json:"active"`
			Mapping string `json:"mapping"`
		} `json:"punch"`
		NoUPnP bool `json:"no_upnp"`
		UPnP   struct {
			State string `json:"state"`
		} `json:"upnp"`
	}
	_ = json.Unmarshal(out.Data, &st)
	if code != 200 || !st.NoPunch || st.UDPPort != 20000 || st.Punch.Active || st.Punch.Mapping != "unknown" ||
		!st.NoUPnP || st.UPnP.State != "disabled" {
		t.Fatalf("打洞配置没有保存或回显不对：%d %s", code, out.Data)
	}
	if code, out := put(`{"udp_port":70000}`); code != 400 {
		t.Fatalf("越界的 UDP 端口应 400：%d %+v", code, out)
	}
}
