package winservice

import (
	"asa-server/asaserver"
	"asa-server/webapi"
	"context"
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
	cancel    context.CancelFunc
}

// Start starts the service
func (p *program) Start(s service.Service) error {
	log.Printf("Starting %s service \n", ServiceName)

	// Ensure directories exist
	if err := asaserver.EnsureDirectories(); err != nil {
		log.Printf("Failed to ensure directories: %v\n", err)
		return err
	}

	// Initialize log mapping from persistent storage
	if err := asaserver.InitializeLogMapping(); err != nil {
		log.Printf("Failed to initialize log mapping: %v\n", err)
		return err
	}

	// Create API server
	p.apiServer = webapi.NewAPIServer(8080)

	// Start the API server in a separate goroutine
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go func() {
		// Wait a bit for the service to be fully initialized
		time.Sleep(2 * time.Second)

		if err := p.apiServer.StartWithContext(ctx); err != nil && err != context.Canceled {
			log.Printf("Failed to start API server: %v\n", err)
		}
	}()

	return nil
}

// Stop stops the service
func (p *program) Stop(s service.Service) error {
	log.Printf("Stopping %s service \n", ServiceName)

	// Cancel the context to gracefully shutdown API server
	if p.cancel != nil {
		p.cancel()
	}

	// Give it a moment to shutdown gracefully
	time.Sleep(1 * time.Second)

	return nil
}

// installService installs the Windows service
func installService() error {
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

// removeService removes the Windows service
func removeService() error {
	prg := &program{}
	s, err := service.New(prg, &service.Config{
		Name:        ServiceName,
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
	})
	if err != nil {
		return err
	}

	err = s.Uninstall()
	if err != nil {
		return err
	}

	fmt.Printf("Service %s removed successfully\n", ServiceName)
	return nil
}

// startService starts the Windows service
func startService() error {
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

// stopService stops the Windows service
func stopService() error {
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
func RunService(isDebug bool) error {
	prg := &program{}
	s, err := service.New(prg, &service.Config{
		Name:        ServiceName,
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
	})
	if err != nil {
		log.Fatal(err)
	}

	err = s.Run()
	if err != nil {
		log.Fatal(err)
	}
	return nil
}

// ActionServiceInstall installs the Windows service
func ActionServiceInstall(ctx context.Context, cmd *cli.Command) error {
	return installService()
}

// ActionServiceRemove removes the Windows service
func ActionServiceRemove(ctx context.Context, cmd *cli.Command) error {
	return removeService()
}

// ActionServiceStart starts the Windows service
func ActionServiceStart(ctx context.Context, cmd *cli.Command) error {
	return startService()
}

// ActionServiceStop stops the Windows service
func ActionServiceStop(ctx context.Context, cmd *cli.Command) error {
	return stopService()
}
