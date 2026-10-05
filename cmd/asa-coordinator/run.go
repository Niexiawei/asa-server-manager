package main

import (
	"context"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/kardianos/service"
	"github.com/urfave/cli/v3"

	"asa-server/internal/meshcoord"
	"asa-server/pkg/logger"
)

const (
	serviceName        = "asa-coordinator"
	serviceDisplayName = "ASA Coordinator"
	serviceDescription = "ASA Server Manager 协调节点（管理器互控的牵线与中转）"
)

func actionRun(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	logger.InitLoggerWithBaseDir(cfg.DataDir, logger.WithLogFileName("coordinator.log"))
	defer logger.Sync()
	logger.WithConsole().Infof("[coord] 配置文件：%s", cfg.Path())

	if !service.Interactive() {
		// 由 Windows SCM / systemd 拉起：交给 kardianos 处理启停信号。
		prg := &program{cfg: cfg}
		svc, err := service.New(prg, serviceConfig(nil))
		if err != nil {
			return err
		}
		return svc.Run()
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return meshcoord.Run(ctx, cfg, version)
}

// program 实现 service.Interface。
type program struct {
	cfg    *meshcoord.Config
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		if err := meshcoord.Run(ctx, p.cfg, version); err != nil {
			logger.Errorf("[coord] 协调节点退出: %v", err)
		}
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	if p.cancel != nil {
		p.cancel()
		<-p.done
	}
	return nil
}

// serviceConfig 生成服务定义。args 是服务启动时的参数（安装时写入 `run -c <绝对路径>`；
// 运行时 kardianos 不用它，传 nil）。
func serviceConfig(args []string) *service.Config {
	cfg := &service.Config{
		Name:        serviceName,
		DisplayName: serviceDisplayName,
		Description: serviceDescription,
		Arguments:   args,
		Option:      service.KeyValue{},
	}
	if runtime.GOOS == "linux" {
		// 网络就绪后再起：要绑公网端口、要被管理器连。Windows 上 Dependencies 是服务名，不能这么写。
		cfg.Dependencies = []string{"Wants=network-online.target", "After=network-online.target"}
		cfg.Option["Restart"] = "on-failure"
		cfg.Option["LimitNOFILE"] = 65536
	}
	return cfg
}
