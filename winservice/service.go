package winservice

import (
	"asa-server/certmgr"
	"asa-server/frpmanage"
	"asa-server/webapi"
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/kardianos/service"
	"github.com/urfave/cli/v3"
)

// ServiceName is the name of the Windows service
const ServiceName = "ASA-Server-Manager"

// ServiceDisplayName is the display name of the Windows service
const ServiceDisplayName = "ASA Server Manager"

// ServiceDescription is the description of the Windows service
const ServiceDescription = "ARK Server Ascended Instance Management Service"

// program implements the service.Service interface
type program struct {
	apiServer *webapi.APIServer
}

// Start starts the service
func (p *program) Start(s service.Service) error {
	log.Printf("Starting %s service \n", ServiceName)
	// Create API server
	p.apiServer = webapi.NewAPIServer()

	if frp := frpmanage.GetGlobalManager(); frp != nil {
		if err := frp.Start(); err != nil {
			log.Printf("frp start err :%v \n", err)
		}
	}

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

// InstallService installs the Windows service
func InstallService() error {
	prg := &program{}
	s, err := service.New(prg, &service.Config{
		Name:        ServiceName,
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
	})
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

// RemoveService removes the Windows service
func RemoveService() error {
	prg := &program{}
	s, err := service.New(prg, &service.Config{
		Name:        ServiceName,
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
	})
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

	// 服务以 LocalSystem 运行，本地 CA 多半装在 LocalMachine\Root。
	// 往用户系统里装了根证书就必须在移除时清理掉，否则它会一直留在受信任列表里。
	if err := certmgr.UntrustCAOnCleanup(); err != nil {
		fmt.Printf("Warning: failed to remove local CA from the trusted root store: %v\n", err)
	}

	fmt.Printf("Service %s removed successfully\n", ServiceName)
	return nil
}

// StartService starts the Windows service
func StartService() error {
	prg := &program{}
	s, err := service.New(prg, &service.Config{
		Name:        ServiceName,
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
	})
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

// StopService stops the Windows service
func StopService() error {
	prg := &program{}
	s, err := service.New(prg, &service.Config{
		Name:        ServiceName,
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
	})
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
	s, err := service.New(prg, &service.Config{
		Name:        ServiceName,
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
	})
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

// ActionServiceInstall installs the Windows service
func ActionServiceInstall(ctx context.Context, cmd *cli.Command) error {
	return InstallService()
}

// ActionServiceRemove removes the Windows service
func ActionServiceRemove(ctx context.Context, cmd *cli.Command) error {
	return RemoveService()
}

// ActionServiceStart starts the Windows service
func ActionServiceStart(ctx context.Context, cmd *cli.Command) error {
	return StartService()
}

// ActionServiceStop stops the Windows service
func ActionServiceStop(ctx context.Context, cmd *cli.Command) error {
	return StopService()
}
