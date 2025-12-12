package webapi

import (
	"asa-server/asaserver"
	"asa-server/logger"
	"fmt"
)

// runUpdateTask executes the server update task
func (s *APIServer) runUpdateTask() {
	defer s.stopUpdateTask()

	defer func() {
		if r := recover(); r != nil {
			logger.GetLogger().Errorf("Server update panic: %v", r)
			s.updateProgressChan <- fmt.Sprintf("Error: Server update panic: %v", r)
		}
	}()

	// Create progress writer
	writer := &UpdateProgressWriter{s: s}

	// Send SteamCMD download and extract message
	s.updateProgressChan <- "Downloading and extracting SteamCMD..."
	if err := asaserver.DownloadAndExtractSteamCmd(writer); err != nil {
		s.updateProgressChan <- fmt.Sprintf("Error: Failed to download SteamCMD: %v", err)
		return
	}

	// Send ARK server update message
	s.updateProgressChan <- "Downloading and updating ARK server files..."
	if err := asaserver.DownloadAndUpdateArkServer(writer); err != nil {
		s.updateProgressChan <- fmt.Sprintf("Error: Failed to update ARK server: %v", err)
		return
	}

	// Server verification
	s.updateProgressChan <- "Verifying server installation..."
	if err := asaserver.VerifyServerInstallation(false); err != nil {
		s.updateProgressChan <- fmt.Sprintf("Error: Server verification failed: %v", err)
		return
	}

	// Update completed
	s.updateProgressChan <- "[COMPLETED] Server update completed successfully!"
}
