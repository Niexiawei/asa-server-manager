// Package svcmgr wraps kardianos/service to register this program as an OS
// service — the Windows SCM on Windows, systemd (or its predecessors) on
// Linux. kardianos already dispatches by platform internally; the
// platform-specific pieces this package adds on top (Linux-only systemd
// hardening: LimitNOFILE/Restart/HOME/WorkingDirectory) live in
// service_windows.go / service_linux.go via configurePlatform, so the
// exported API below is identical on both platforms.
//
// Renamed from internal/winservice: see docs/LINUX_COMPATIBILITY_PLAN.md §5.8.
package svcmgr

import (
	"asa-server/internal/actions"
	"asa-server/internal/certmgr"
	"asa-server/internal/runner"
	"asa-server/internal/webapi"
	"asa-server/pkg/logger"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/kardianos/service"
	"github.com/urfave/cli/v3"
)

// ServiceName is the name of the OS service
const ServiceName = "ASA-Server-Manager"

// ServiceDisplayName is the display name of the OS service
const ServiceDisplayName = "ASA Server Manager"

// ServiceDescription is the description of the OS service
const ServiceDescription = "ARK Server Ascended Instance Management Service"

// program implements the service.Service interface
type program struct {
	apiServer *webapi.APIServer
}

// Start starts the service
func (p *program) Start(s service.Service) error {
	log.Printf("Starting %s service \n", ServiceName)

	// Warn (never block) if the base environment isn't ready: a service that
	// hard-exits here would just restart-loop under systemd/SCM. The API and
	// web UI still come up; instance starts will report the same thing.
	// See docs/SETUP_FLOW_OPTIMIZATION_PLAN.md §3.3.
	if err := actions.VerifyEnvironmentReady(); err != nil {
		logger.WithConsole().Warnf("基础环境尚未初始化，服务已启动但实例暂时无法运行：\n%v", err)
	}

	// Create API server
	p.apiServer = webapi.NewAPIServer()

	// frp 的启动不在这里：APIServer.Start() 已经做了，且与它的 Stop() 对称。
	// 这里再来一次的话第二次必然撞上 "frpc is already running"，被记成 ERROR ——
	// 服务模式每次开机都刷一条假错误。

	go func() {
		if err := p.apiServer.Start(); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("Failed to start API server: %v\n", err)
		}
	}()

	return nil
}

// Stop stops the service
func (p *program) Stop(s service.Service) error {
	log.Printf("Stopping %s service \n", ServiceName)
	// 与 webapi.ActionAPI 的收尾同源：关掉运行时插件持有的进程级资源（自管的
	// Xvfb）。放在最前面是因为它与 apiServer 是否初始化无关 —— 下面那个 nil
	// 分支会直接 return。
	runner.Close()
	if p.apiServer == nil {
		log.Printf("API server not initialized, nothing to stop\n")
		return nil
	}
	if err := p.apiServer.Stop(); err != nil {
		log.Printf("Error stopping API server: %v\n", err)
		return err
	}
	// Give it a moment to shut down gracefully
	time.Sleep(1 * time.Second)
	return nil
}

// newServiceConfig builds the base service.Config and lets the platform file
// (service_windows.go / service_linux.go) layer on anything OS-specific.
func newServiceConfig() *service.Config {
	cfg := &service.Config{
		Name:        ServiceName,
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
	}
	configurePlatform(cfg)
	injectConfigLocation(cfg, os.Getenv("ASA_CFG"))
	return cfg
}

// injectConfigLocation 把安装时生效的 ASA_CFG 烤进服务自己的环境。
//
// 服务进程看不到安装者的环境：Windows 服务以 LocalSystem 运行，读不到安装者
// HKCU 里的用户级环境变量（GUI 向导「自定义配置目录」写的正是那里）；systemd
// 服务也不继承 shell 里 export 的变量。不注入的话，服务会按「exe 同级 > 系统目录」
// 去找配置，读到另一份（或者一份都没有）。kardianos 在 Windows 上把 EnvVars 写进
// 服务注册表项的 Environment（REG_MULTI_SZ），在 systemd 上写成 unit 的 Environment=。
//
// 这是安装时的快照：之后改配置位置要重装服务。见
// docs/SETUP_FLOW_OPTIMIZATION_PLAN.md Part 2 §P2-3.5.1。
func injectConfigLocation(cfg *service.Config, asaCfg string) {
	if asaCfg == "" {
		return
	}
	if abs, err := filepath.Abs(asaCfg); err == nil {
		asaCfg = abs
	}
	if cfg.EnvVars == nil {
		cfg.EnvVars = map[string]string{}
	}
	cfg.EnvVars["ASA_CFG"] = asaCfg
}

// InstallService installs the OS service
func InstallService() error {
	warnBeforeInstall()

	prg := &program{}
	s, err := service.New(prg, newServiceConfig())
	if err != nil {
		return err
	}

	err = s.Install()
	if err != nil {
		return err
	}

	fmt.Printf("Service %s installed successfully\n", ServiceName)
	return nil
}

// RemoveService removes the OS service
func RemoveService() error {
	prg := &program{}
	s, err := service.New(prg, newServiceConfig())
	if err != nil {
		return err
	}

	// Try to stop first
	_ = s.Stop()
	time.Sleep(500 * time.Millisecond)

	err = s.Uninstall()
	if err != nil {
		return err
	}

	// 服务默认以系统身份运行（Windows LocalSystem / Linux root），本地 CA
	// 多半装在系统级信任存储里。往用户系统里装了根证书就必须在移除时清理掉，
	// 否则它会一直留在受信任列表里。
	if err := certmgr.UntrustCAOnCleanup(); err != nil {
		fmt.Printf("Warning: failed to remove local CA from the trusted root store: %v\n", err)
	}

	fmt.Printf("Service %s removed successfully\n", ServiceName)
	return nil
}

// StartService starts the OS service
func StartService() error {
	prg := &program{}
	s, err := service.New(prg, newServiceConfig())
	if err != nil {
		return err
	}

	err = s.Start()
	if err != nil {
		return err
	}

	fmt.Printf("Service %s started successfully\n", ServiceName)
	return nil
}

// StopService stops the OS service
func StopService() error {
	prg := &program{}
	s, err := service.New(prg, newServiceConfig())
	if err != nil {
		return err
	}

	err = s.Stop()
	if err != nil {
		return err
	}

	fmt.Printf("Service %s stopped successfully\n", ServiceName)
	return nil
}

// RunService runs the service
func RunService() error {
	prg := &program{}
	s, err := service.New(prg, newServiceConfig())
	if err != nil {
		log.Printf("Failed to create service: %v\n", err)
		return err
	}

	err = s.Run()
	if err != nil {
		log.Printf("Failed to run service: %v\n", err)
		return err
	}
	return nil
}

// ActionServiceInstall installs the OS service. Refuses when the base
// environment isn't initialised (a service that can't run any instance is a
// foot-gun) unless --force is given — see
// docs/SETUP_FLOW_OPTIMIZATION_PLAN.md §3.4.
func ActionServiceInstall(ctx context.Context, cmd *cli.Command) error {
	if cmd == nil || !cmd.Bool("force") {
		if err := actions.VerifyEnvironmentReady(); err != nil {
			return fmt.Errorf("%w\n\n装成服务前请先完成环境初始化；确需强行安装可加 --force", err)
		}
	}
	return InstallService()
}

// ActionServiceRemove removes the OS service
func ActionServiceRemove(ctx context.Context, cmd *cli.Command) error {
	return RemoveService()
}

// ActionServiceStart starts the OS service
func ActionServiceStart(ctx context.Context, cmd *cli.Command) error {
	return StartService()
}

// ActionServiceStop stops the OS service
func ActionServiceStop(ctx context.Context, cmd *cli.Command) error {
	return StopService()
}
