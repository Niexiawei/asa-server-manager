package pluginapi

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfgpkg "asa-server/internal/config"

	"github.com/gin-gonic/gin"
)

// 这里验证的是 HTTP 这一层：multipart 流式接收、422 带报告、413、targets 校验。
// 安装/卸载本身的语义在 internal/arkapimanage 的用例里。

func setupArkApiEnv(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	origBase, origSF, origInst := cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir
	t.Cleanup(func() { cfgpkg.BaseDir, cfgpkg.ServerFilesDir, cfgpkg.InstancesDir = origBase, origSF, origInst })
	cfgpkg.BaseDir = root
	cfgpkg.ServerFilesDir = filepath.Join(root, "server-files")
	cfgpkg.InstancesDir = filepath.Join(root, "instances")
	loader := filepath.Join(cfgpkg.ServerFilesDir, "ShooterGame", "Binaries", "Win64", "AsaApiLoader.exe")
	if err := os.MkdirAll(filepath.Dir(loader), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loader, minimalPE(), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfgpkg.InstancesDir, 0755); err != nil {
		t.Fatal(err)
	}
}

// minimalPE 是 debug/pe 能解析的最小 x64 PE 头。
func minimalPE() []byte {
	b := make([]byte, 0x100)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[0x3c:], 0x80)
	copy(b[0x80:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x84:], 0x8664)
	return b
}

func pluginZipBytes(t *testing.T, name string, withPDB bool) []byte {
	t.Helper()
	files := map[string][]byte{
		name + "/PluginInfo.json":  []byte(`{"FullName":"` + name + `","Version":1.3}`),
		name + "/" + name + ".dll": minimalPE(),
	}
	if withPDB {
		files[name+"/"+name+".pdb"] = []byte("pdb")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for p, data := range files {
		w, err := zw.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newArkApiRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewHandler().registerArkApiRoutes(r)
	return r
}

type envelope struct {
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func do(t *testing.T, r *gin.Engine, req *http.Request) (int, envelope) {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是 JSON 信封（%d）: %s", w.Code, w.Body.String())
	}
	return w.Code, env
}

func uploadRequest(t *testing.T, query, field string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("note", "fields before the file must be skipped")
	fw, err := mw.CreateFormFile(field, "Tidy.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/arkapi/packages"+query, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestUploadPluginPackageOverHTTP(t *testing.T) {
	setupArkApiEnv(t)
	r := newArkApiRouter()

	code, env := do(t, r, uploadRequest(t, "?kind=plugin", "file", pluginZipBytes(t, "Tidy", true)))
	if code != http.StatusOK || !env.Success {
		t.Fatalf("合格的包应返回 200: %d %s", code, env.Error)
	}
	var stage struct {
		Token   string   `json:"token"`
		Name    string   `json:"name"`
		Version string   `json:"version"`
		Errors  []string `json:"errors"`
		Targets []any    `json:"targets"`
	}
	if err := json.Unmarshal(env.Data, &stage); err != nil {
		t.Fatal(err)
	}
	if stage.Token == "" || stage.Name != "Tidy" || stage.Version != "1.3" || stage.Targets == nil {
		t.Fatalf("报告 = %+v", stage)
	}

	// 放弃之后 token 失效
	code, _ = do(t, r, httptest.NewRequest(http.MethodDelete, "/api/arkapi/packages/"+stage.Token, nil))
	if code != http.StatusOK {
		t.Fatalf("discard = %d", code)
	}
	apply := httptest.NewRequest(http.MethodPost, "/api/arkapi/packages/"+stage.Token+"/apply", strings.NewReader(`{"targets":["a"]}`))
	apply.Header.Set("Content-Type", "application/json")
	if code, env = do(t, r, apply); code != http.StatusBadRequest || !strings.Contains(env.Error, "过期") {
		t.Errorf("放弃后 apply 应失败: %d %s", code, env.Error)
	}
}

// 校验失败是 422，data 与成功时同形、token 为空——前端靠它逐条展示错误。
func TestUploadInvalidPackageReturns422WithReport(t *testing.T) {
	setupArkApiEnv(t)
	r := newArkApiRouter()

	code, env := do(t, r, uploadRequest(t, "", "file", pluginZipBytes(t, "Tidy", false)))
	if code != http.StatusUnprocessableEntity || env.Success {
		t.Fatalf("缺 pdb 的包应返回 422，实际 %d", code)
	}
	var stage struct {
		Token  string   `json:"token"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(env.Data, &stage); err != nil {
		t.Fatal(err)
	}
	if stage.Token != "" || len(stage.Errors) == 0 || !strings.Contains(stage.Errors[0], "Tidy.pdb") {
		t.Errorf("报告 = %+v", stage)
	}
	if entries, _ := os.ReadDir(filepath.Join(cfgpkg.BaseDir, "arkapi", "staging")); len(entries) != 0 {
		t.Errorf("校验失败的包不应留在暂存区，还剩 %d 项", len(entries))
	}
}

func TestUploadRequestErrors(t *testing.T) {
	setupArkApiEnv(t)
	r := newArkApiRouter()

	if code, env := do(t, r, uploadRequest(t, "?kind=core", "file", []byte("x"))); code != http.StatusBadRequest {
		t.Errorf("kind=core 尚未开放，应返回 400: %d %s", code, env.Error)
	}
	if code, env := do(t, r, uploadRequest(t, "", "attachment", []byte("x"))); code != http.StatusBadRequest || !strings.Contains(env.Error, "file") {
		t.Errorf("没有 file 字段应返回 400: %d %s", code, env.Error)
	}
	notMultipart := httptest.NewRequest(http.MethodPost, "/api/arkapi/packages", strings.NewReader("{}"))
	notMultipart.Header.Set("Content-Type", "application/json")
	if code, _ := do(t, r, notMultipart); code != http.StatusBadRequest {
		t.Errorf("非 multipart 请求应返回 400: %d", code)
	}
}

func TestRespondUploadErrorMapsTooLarge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondUploadError(c, &http.MaxBytesError{Limit: 1})
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "256 MiB") {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
}

// 接口不存在「默认全部」：targets 为空或含非法实例名一律 400。
func TestTargetsMustBeExplicitAndValid(t *testing.T) {
	setupArkApiEnv(t)
	r := newArkApiRouter()
	for _, c := range []struct{ path, body string }{
		{"/api/arkapi/plugins/P/uninstall", `{"targets":[]}`},
		{"/api/arkapi/plugins/P/uninstall", `{}`},
		{"/api/arkapi/plugins/P/uninstall", `{"targets":["../etc"]}`},
		{"/api/arkapi/packages/whatever/apply", `{"targets":[]}`},
	} {
		req := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		if code, env := do(t, r, req); code != http.StatusBadRequest {
			t.Errorf("%s %s → %d %s，want 400", c.path, c.body, code, env.Error)
		}
	}

	code, env := do(t, r, httptest.NewRequest(http.MethodGet, "/api/arkapi/plugins/P/instances", nil))
	if code != http.StatusOK || !strings.Contains(string(env.Data), `"instances":[]`) {
		t.Errorf("没有实例时应返回空列表: %d %s", code, env.Data)
	}
}
