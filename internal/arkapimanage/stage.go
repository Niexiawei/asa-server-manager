package arkapimanage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	cfgpkg "asa-server/internal/config"
	"asa-server/pkg/archive"
	"asa-server/pkg/logger"
)

// 两段式安装的暂存区（docs/ARKAPI_PLUGIN_INSTALL_PLAN.md §6.0）：
//
//	上传 → {BaseDir}/arkapi/staging/<token>/ 解压并校验 → 返回报告 → 用户确认 → apply
//
// 登记只在内存里：暂存 30 分钟过期，用户不确认就关掉对话框时立即丢弃，进程启动时整个清空。

const (
	// MaxUploadBytes 是上传包的大小上限（方案 §5.1）
	MaxUploadBytes = 256 << 20

	stagingTTL = 30 * time.Minute
	kindPlugin = "plugin"
)

var errStageGone = errors.New("暂存的安装包不存在或已过期（30 分钟），请重新上传")

type stagedPackage struct {
	token     string
	kind      string
	dir       string
	expiresAt time.Time
	plugin    *PluginReport
	timer     *time.Timer
}

var (
	stagingMu sync.Mutex
	staged    = map[string]*stagedPackage{}
)

func stagingRoot() string {
	return filepath.Join(cfgpkg.BaseDir, "arkapi", "staging")
}

// ClearStaging 清空暂存区。进程启动时调用：登记只在内存里，上一个进程留下的目录已经没人认领了。
func ClearStaging() {
	if err := os.RemoveAll(stagingRoot()); err != nil {
		logger.Warnf("清空 ArkApi 暂存区失败: %v", err)
	}
}

func newStage(kind string) (*stagedPackage, error) {
	token, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	sp := &stagedPackage{token: token, kind: kind, dir: filepath.Join(stagingRoot(), token)}
	if err := os.MkdirAll(sp.dir, 0755); err != nil {
		return nil, err
	}
	return sp, nil
}

func (sp *stagedPackage) extractDir() string { return filepath.Join(sp.dir, "x") }

func (sp *stagedPackage) remove() {
	if err := os.RemoveAll(sp.dir); err != nil {
		logger.Warnf("清理暂存目录 %s 失败: %v", sp.dir, err)
	}
}

// receive 把上传体落盘并解压。extractErr 非空表示包本身不合格（不是 zip、条目不安全、超限），
// 应当作为校验错误报告给用户；err 非空才是服务端或传输错误。
func (sp *stagedPackage) receive(src io.Reader) (entries []archive.Entry, extractErr, err error) {
	zipPath := filepath.Join(sp.dir, "upload.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		return nil, nil, err
	}
	_, copyErr := io.Copy(f, src)
	closeErr := f.Close()
	if copyErr != nil {
		return nil, nil, copyErr
	}
	if closeErr != nil {
		return nil, nil, closeErr
	}
	entries, extractErr = archive.ExtractZip(zipPath, sp.extractDir(), archive.Limits{})
	_ = os.Remove(zipPath) // 暂存区只留解压结果
	return entries, extractErr, nil
}

// register 登记一个校验通过的暂存包，到期自动丢弃。
func register(sp *stagedPackage) {
	stagingMu.Lock()
	defer stagingMu.Unlock()
	sp.expiresAt = time.Now().Add(stagingTTL)
	sp.timer = time.AfterFunc(stagingTTL, func() { Discard(sp.token) })
	staged[sp.token] = sp
}

// take 取出一个暂存包用于 apply：从登记里摘掉，于是它不会被过期清理，也不能被重复 apply。
// 调用方用完负责 remove。
func take(token, kind string) (*stagedPackage, error) {
	stagingMu.Lock()
	defer stagingMu.Unlock()
	sp, ok := staged[token]
	if !ok || sp.kind != kind {
		return nil, errStageGone
	}
	delete(staged, token)
	sp.timer.Stop()
	return sp, nil
}

// Discard 丢弃一个暂存包（用户关掉了确认对话框，或已过期）。不存在时什么都不做。
func Discard(token string) {
	stagingMu.Lock()
	sp, ok := staged[token]
	if ok {
		delete(staged, token)
		sp.timer.Stop()
	}
	stagingMu.Unlock()
	if ok {
		sp.remove()
	}
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
