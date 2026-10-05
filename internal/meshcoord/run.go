package meshcoord

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"asa-server/internal/mesh/meshpb"
	"asa-server/pkg/logger"
	"asa-server/pkg/meshid"
	"asa-server/pkg/meshjoin"
	"asa-server/pkg/stun"
)

const (
	identityKeyFile  = "coordinator.key"
	identityCertFile = "coordinator.crt"

	stunStatsInterval = 10 * time.Minute
	shutdownTimeout   = 10 * time.Second
)

// LoadIdentity 返回协调节点出示的证书与它的身份（SPKI 指纹）：配置了正式证书时用它，
// 否则用 data_dir 里的自签证书（首次运行时生成）。两种情况下 join blob 里写的都是这个身份。
func LoadIdentity(cfg *Config) (tls.Certificate, meshid.ID, error) {
	if cfg.TLS.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return tls.Certificate{}, meshid.ID{}, fmt.Errorf("加载 tls.cert_file / key_file: %w", err)
		}
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return tls.Certificate{}, meshid.ID{}, err
		}
		cert.Leaf = leaf
		return cert, meshid.FromCert(leaf), nil
	}
	return meshid.LoadOrCreateFiles(
		filepath.Join(cfg.DataDir, identityKeyFile),
		filepath.Join(cfg.DataDir, identityCertFile))
}

// JoinBlob 生成网络 networkID 的接入串。会在需要时建出 default 网络与自身身份，
// 所以 run 之前先跑 join-blob 也没问题。
func JoinBlob(cfg *Config, networkID string) (string, error) {
	store, err := OpenStore(cfg.DataDir)
	if err != nil {
		return "", err
	}
	defer store.Close()
	if networkID == DefaultNetworkID {
		if _, _, err := store.EnsureDefaultNetwork(); err != nil {
			return "", err
		}
	}
	n, err := store.Network(networkID)
	if err != nil {
		return "", fmt.Errorf("网络 %q: %w", networkID, err)
	}
	_, id, err := LoadIdentity(cfg)
	if err != nil {
		return "", err
	}
	return meshjoin.JoinBlob{
		Addr: cfg.PublicAddr, Coordinator: id, NetworkID: n.ID, NetworkSecret: n.Secret,
	}.Encode()
}

// NewGRPCServer 建协调节点的 gRPC 服务器：TLS 1.3、要求客户端证书但接受任何公钥
// （由 Server 按节点 ID 决定权限）、与管理器 20 秒心跳配套的 keepalive。
func NewGRPCServer(cert tls.Certificate, srv *Server) *grpc.Server {
	gs := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(meshid.AnyClientServerConfig(cert))),
		// MinTime 必须低于管理器的心跳间隔，否则管理器会被以 too_many_pings 踢掉。
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: time.Minute, Timeout: 20 * time.Second}),
	)
	meshpb.RegisterCoordinatorServer(gs, srv)
	return gs
}

// Run 运行协调节点直到 ctx 结束。
func Run(ctx context.Context, cfg *Config, version string) error {
	store, err := OpenStore(cfg.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()
	if _, created, err := store.EnsureDefaultNetwork(); err != nil {
		return err
	} else if created {
		// 不打印 join blob：它含网络密钥，日志文件会被备份、被贴出来求助（§8.2）。
		logger.WithConsole().Infof("[coord] 已创建 default 网络。运行 asa-coordinator join-blob 获取接入串")
	}
	cert, id, err := LoadIdentity(cfg)
	if err != nil {
		return err
	}

	lis, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", cfg.Listen, err)
	}
	// 显式配置的 STUN 端口绑不上就启动失败，不静默降级成一个。
	var pcs []net.PacketConn
	closeAll := func() {
		for _, pc := range pcs {
			pc.Close()
		}
	}
	for _, l := range cfg.STUN.Listen {
		pc, err := net.ListenPacket("udp", l)
		if err != nil {
			lis.Close()
			closeAll()
			return fmt.Errorf("STUN 监听 %s: %w", l, err)
		}
		pcs = append(pcs, pc)
	}
	defer closeAll()

	srv := NewServer(ServerOptions{Store: store, STUNAddrs: cfg.STUNAdvertise(), Limits: cfg.Limits})
	defer srv.Close()
	gs := NewGRPCServer(cert, srv)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- gs.Serve(lis) }()

	stats := &stun.Stats{}
	for _, pc := range pcs {
		go func(pc net.PacketConn) {
			err := stun.Serve(runCtx, pc, stun.ServerOptions{
				RatePerSource:  cfg.STUN.RatePerSource,
				BurstPerSource: cfg.STUN.BurstPerSource,
				RateGlobal:     cfg.STUN.RateGlobal,
				Stats:          stats,
				Warnf:          func(f string, a ...any) { logger.Warnf("[coord] "+f, a...) },
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.Errorf("[coord] STUN %s 停止: %v", pc.LocalAddr(), err)
			}
		}(pc)
	}
	if len(pcs) > 0 {
		go logSTUNStats(runCtx, stats)
	}

	logger.WithConsole().Infof("[coord] 协调节点已启动：身份 %s，监听 %s，对外地址 %s，STUN %v",
		id, cfg.Listen, cfg.PublicAddr, cfg.STUNAdvertise())

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		return fmt.Errorf("gRPC 服务退出: %w", err)
	}
	logger.WithConsole().Infof("[coord] 正在停止…")
	cancel()
	stopped := make(chan struct{})
	go func() { gs.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(shutdownTimeout):
		// Session 是长连接，GracefulStop 会等它们自己结束——不能无限等。
		gs.Stop()
	}
	return nil
}

// logSTUNStats 每 10 分钟在计数有变化时记一行。
func logSTUNStats(ctx context.Context, stats *stun.Stats) {
	t := time.NewTicker(stunStatsInterval)
	defer t.Stop()
	var last stun.Snapshot
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cur := stats.Snapshot()
			if cur == last {
				continue
			}
			logger.Infof("[coord] STUN 近 %s：收到 %d、成功 %d、420 %d、丢弃（格式）%d、丢弃（限流）%d",
				stunStatsInterval,
				cur.Received-last.Received, cur.Success-last.Success, cur.UnknownAttribute-last.UnknownAttribute,
				cur.DropMalformed-last.DropMalformed, cur.DropRateLimited-last.DropRateLimited)
			last = cur
		}
	}
}
