// Package certmgr 负责本地 HTTPS 证书：签发一个本地 CA，用它签发服务器叶子证书，
// 并（可选）把 CA 写入 Windows 受信任根存储，使浏览器访问 https://localhost:19193
// 不再弹出警告。启用 HTTPS 是浏览器协商 HTTP/2 的硬前提——没有任何主流浏览器支持
// 明文 h2c，详见 docs/HTTP2_CONNECTION_OPTIMIZATION.md。
//
// 安全红线：CA 私钥只在用户本机生成，绝不打包进二进制。一旦随发行版分发同一把私钥，
// 任何拿到二进制的人都能对所有用户伪造任意 HTTPS 站点。
package certmgr

import (
	cfgpkg "asa-server/config"
	"asa-server/logger"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// CACommonName 必须自解释：用户在证书管理器里看到它时要知道这是什么、从哪来
	CACommonName = "ASA Server Manager Local CA"
	organization = "ASA Server Manager"
)

// Options 描述一次 TLS 装配的输入
type Options struct {
	// CertFile / KeyFile 为用户自备证书（有域名时推荐）。两者都非空时直接使用，
	// 不生成本地 CA，也不碰系统受信任存储。
	CertFile string
	KeyFile  string

	// Trust 决定是否把本地 CA 写入系统受信任根存储。仅在使用本地 CA 时有意义。
	Trust bool

	// ExtraDomains 追加进叶子证书 SAN 的域名（反代对外域名、动态域名等）
	ExtraDomains []string
}

// CertsDir 返回证书存放目录 {BaseDir}/certs
func CertsDir() string {
	return filepath.Join(cfgpkg.BaseDir, "certs")
}

func caCertPath() string   { return filepath.Join(CertsDir(), "ca.crt") }
func caKeyPath() string    { return filepath.Join(CertsDir(), "ca.key") }
func leafCertPath() string { return filepath.Join(CertsDir(), "server.crt") }
func leafKeyPath() string  { return filepath.Join(CertsDir(), "server.key") }

// EnsureTLSConfig 准备好可直接交给 http.Server 的 TLS 配置。
//
// 使用自备证书时只做加载；否则确保本地 CA 与叶子证书就绪（SAN 变化或临期会自动重签），
// 并在 opts.Trust 为真时尽力把 CA 写进受信任存储——写入失败只降级为浏览器警告，
// 不影响 HTTPS 本身可用，因此只记警告不返回错误。
func EnsureTLSConfig(opts Options) (*tls.Config, error) {
	if opts.CertFile != "" && opts.KeyFile != "" {
		pair, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("加载自备证书失败: %w", err)
		}
		logger.GetLogger().Infof("使用自备证书: %s", opts.CertFile)
		return newTLSConfig(pair), nil
	}

	if err := os.MkdirAll(CertsDir(), 0700); err != nil {
		return nil, fmt.Errorf("创建证书目录失败: %w", err)
	}

	ca, err := ensureCA()
	if err != nil {
		return nil, err
	}

	pair, err := ensureLeaf(ca, opts.ExtraDomains)
	if err != nil {
		return nil, err
	}

	if opts.Trust {
		if err := TrustCA(); err != nil {
			logger.GetLogger().Warnf(
				"本地 CA 未能写入系统受信任存储（浏览器会提示证书警告，可手动执行 `asa-server cert install`）: %v", err)
		}
	}

	return newTLSConfig(pair), nil
}

// newTLSConfig 组装 TLS 配置。NextProtos 里的 h2 是浏览器经 ALPN 协商 HTTP/2 的依据；
// http/1.1 必须保留——WebSocket 升级依赖 http.Hijacker，而 HTTP/2 的 ResponseWriter
// 不实现它（见 webapi/actions.go 的 protocolsH1H2）。
func newTLSConfig(pair tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}
}
