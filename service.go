package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

var elog debug.Log

// ServiceName is the name of the Windows service
const ServiceName = "ASA-Server-Manager"

// ServiceDisplayName is the display name of the Windows service
const ServiceDisplayName = "ASA Server Manager"

// ServiceDescription is the description of the Windows service
const ServiceDescription = "ARK Server Ascended Instance Management Service"

// Service implements the Windows service handler
type Service struct{}

// Execute implements the Windows service handler interface
func (s *Service) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	// Start the API server in a separate goroutine
	go func() {
		// Wait a bit for the service to be fully initialized
		time.Sleep(2 * time.Second)

		// Start the API server on port 8080
		apiServer := NewAPIServer(8080)
		if err := apiServer.Start(); err != nil {
			if elog != nil {
				elog.Error(1, fmt.Sprintf("Failed to start API server: %v", err))
			}
		}
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				// Log shutdown
				if elog != nil {
					elog.Info(1, "Service shutdown requested")
				}

				// Set status to stop pending
				changes <- svc.Status{State: svc.StopPending}
				break loop
			default:
				if elog != nil {
					elog.Error(1, fmt.Sprintf("unexpected control request #%d", c.Cmd))
				}
			}
		}
	}

	changes <- svc.Status{State: svc.Stopped}
	return
}

// RunService runs the service
func RunService(isDebug bool) {
	var err error
	if isDebug {
		elog = debug.New(ServiceName)
	} else {
		elog, err = eventlog.Open(ServiceName)
		if err != nil {
			return
		}
	}
	defer elog.Close()

	elog.Info(1, fmt.Sprintf("starting %s service", ServiceName))
	run := svc.Run
	if isDebug {
		run = debug.Run
	}
	err = run(ServiceName, &Service{})
	if err != nil {
		elog.Error(1, fmt.Sprintf("%s service failed: %v", ServiceName, err))
		return
	}
	elog.Info(1, fmt.Sprintf("%s service stopped", ServiceName))
}

// installService installs the Windows service
func installService() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	// Connect to the Windows service manager
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	// Check if service already exists
	s, err := m.OpenService(ServiceName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists", ServiceName)
	}

	// Create the service
	config := mgr.Config{
		ServiceType:      windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:        mgr.StartAutomatic,
		ErrorControl:     mgr.ErrorNormal,
		DisplayName:      ServiceDisplayName,
		Description:      ServiceDescription,
		DelayedAutoStart: true,
	}

	s, err = m.CreateService(ServiceName, exePath, config, "service")
	if err != nil {
		return err
	}
	defer s.Close()

	// Set recovery actions
	recoveryActions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
	}

	err = s.SetRecoveryActions(recoveryActions, 60)
	if err != nil {
		return err
	}

	fmt.Printf("Service %s installed successfully\n", ServiceName)
	return nil
}

// removeService removes the Windows service
func removeService() error {
	// Connect to the Windows service manager
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	// Open the service
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("service %s is not installed", ServiceName)
	}
	defer s.Close()

	// Check service status
	status, err := s.Query()
	if err != nil {
		return fmt.Errorf("failed to query service status: %v", err)
	}

	// If service is running, stop it first
	if status.State == svc.Running || status.State == svc.StartPending {
		_, err = s.Control(svc.Stop)
		if err != nil {
			return fmt.Errorf("failed to stop service: %v", err)
		}

		// Wait for service to stop
		for i := 0; i < 30; i++ { // Wait up to 30 seconds
			time.Sleep(1 * time.Second)
			status, err = s.Query()
			if err != nil {
				break
			}
			if status.State != svc.Running && status.State != svc.StopPending {
				break
			}
		}
	}

	// Delete the service
	err = s.Delete()
	if err != nil {
		return err
	}

	fmt.Printf("Service %s removed successfully\n", ServiceName)
	return nil
}

// startService starts the Windows service
func startService() error {
	// Connect to the Windows service manager
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	// Open the service
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("could not access service: %v", err)
	}
	defer s.Close()

	// Start the service
	err = s.Start("service")
	if err != nil {
		return fmt.Errorf("could not start service: %v", err)
	}

	fmt.Printf("Service %s started successfully\n", ServiceName)
	return nil
}

// stopService stops the Windows service
func stopService() error {
	// Connect to the Windows service manager
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	// Open the service
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("could not access service: %v", err)
	}
	defer s.Close()

	// Stop the service
	_, err = s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("could not stop service: %v", err)
	}

	fmt.Printf("Service %s stopped successfully\n", ServiceName)
	return nil
}

// isService checks if the application is running as a Windows service
func isService() bool {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return isService
}
