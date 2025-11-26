package asaserver

import (
	"archive/tar"
	"asa-server/logger"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// BackupInstanceWorld creates a backup of an instance world using its SaveDir from configuration
func BackupInstanceWorld(instanceName string) error {
	running, err := IsServerRunning(instanceName)
	if err == nil && running {
		logger.GetLogger().Warnf("Server for instance '%s' is running. Stop it before creating a backup.", instanceName)
		return fmt.Errorf("server is running")
	}

	// Load instance configuration to get SaveDir
	config, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return fmt.Errorf("failed to load instance config: %w", err)
	}

	saveDir := config.SaveDir
	if saveDir == "" {
		return fmt.Errorf("SaveDir not configured for instance '%s'", instanceName)
	}

	savePath := filepath.Join(ServerFilesDir, "ShooterGame/Saved", instanceName)

	if _, err := os.Stat(savePath); err != nil {
		return fmt.Errorf("SaveDir '%s' not found in instance '%s'", saveDir, instanceName)
	}

	// Create backups directory
	if err := os.MkdirAll(BackupsDir, 0755); err != nil {
		return fmt.Errorf("failed to create backups directory: %w", err)
	}

	// Create archive name with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	archiveName := fmt.Sprintf("%s_%s.tar.zstd", instanceName, timestamp)
	archivePath := filepath.Join(BackupsDir, archiveName)

	logger.GetLogger().Infof("Creating backup for instance: %s...", instanceName)

	// Create the archive
	zw, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("failed to create archive file: %w", err)
	}
	defer zw.Close()

	zstdWriter, err := zstd.NewWriter(zw)
	if err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("failed to create zstd writer: %w", err)
	}
	defer zstdWriter.Close()

	tarWriter := tar.NewWriter(zstdWriter)
	defer tarWriter.Close()

	instanceBaseDir := filepath.Join(InstancesDir, instanceName)

	// Add SaveDir (world data) to archive
	if err := addFilesToTar(tarWriter, savePath, "worldfile"); err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("failed to add SaveDir to archive: %w", err)
	}

	// Add instance_config.ini to archive
	configFile := filepath.Join(instanceBaseDir, "instance_config.ini")
	if _, err := os.Stat(configFile); err == nil {
		if err := addFileToTar(tarWriter, configFile, "instance_config.ini"); err != nil {
			os.Remove(archivePath)
			return fmt.Errorf("failed to add instance_config.ini to archive: %w", err)
		}
	}

	// Add Config directory to archive
	configDir := filepath.Join(instanceBaseDir, "Config")
	if _, err := os.Stat(configDir); err == nil {
		if err := addFilesToTar(tarWriter, configDir, "instanceconfig"); err != nil {
			os.Remove(archivePath)
			return fmt.Errorf("failed to add Config directory to archive: %w", err)
		}
	}

	logger.GetLogger().Infof("Backup successfully created: %s", archivePath)
	return nil
}

// RestoreBackupToInstance restores a backup to an instance
func RestoreBackupToInstance(instanceName string, backupFile string) error {
	running, err := IsServerRunning(instanceName)
	if err == nil && running {
		logger.GetLogger().Warnf("Server for instance '%s' is running. Stop it before restoring a backup.", instanceName)
		return fmt.Errorf("server is running")
	}

	// Check if backup file exists
	if _, err := os.Stat(backupFile); err != nil {
		return fmt.Errorf("backup file not found: %s", backupFile)
	}

	// Load instance configuration
	config, err := LoadInstanceConfig(instanceName)
	if err != nil {
		return fmt.Errorf("failed to load instance config: %w", err)
	}

	instanceBaseDir := filepath.Join(InstancesDir, instanceName)

	logger.GetLogger().Infof("Extracting backup to instance '%s'...", instanceName)

	// Extract archive
	file, err := os.Open(backupFile)
	if err != nil {
		return fmt.Errorf("failed to open backup file: %w", err)
	}
	defer file.Close()

	// Get file size
	_, err = file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	zstdReader, err := zstd.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create zstd reader: %w", err)
	}
	defer zstdReader.Close()

	tarReader := tar.NewReader(zstdReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar archive: %w", err)
		}

		// Determine target path based on file location in archive
		var target string
		name := header.Name

		switch {
		case len(name) > 0 && name[0:1] == "w" && len(name) > 9 && name[0:9] == "worldfile":
			// worldfile/* -> SaveDir/...
			relPath := name[9:]
			if len(relPath) > 0 && relPath[0] == '/' {
				relPath = relPath[1:]
			}
			target = filepath.Join(config.SaveDir, relPath)
		case name == "instance_config.ini":
			// instance_config.ini -> instanceBaseDir/instance_config.ini
			target = filepath.Join(instanceBaseDir, "instance_config.ini")
		case len(name) > 0 && name[0:1] == "i" && len(name) > 14 && name[0:14] == "instanceconfig":
			// instanceconfig/* -> instanceBaseDir/Config/...
			relPath := name[14:]
			if len(relPath) > 0 && relPath[0] == '/' {
				relPath = relPath[1:]
			}
			target = filepath.Join(instanceBaseDir, "Config", relPath)
		default:
			// Skip unknown files
			continue
		}

		// Create directories as needed
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			continue
		}

		// Create file
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory: %w", err)
		}

		f, err := os.Create(target)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}

		if _, err := io.Copy(f, tarReader); err != nil {
			f.Close()
			return fmt.Errorf("failed to write file: %w", err)
		}
		f.Close()
	}

	logger.GetLogger().Infof("Backup successfully loaded into instance '%s'.", instanceName)
	return nil
}

// GetAvailableBackups returns a list of available backups
func GetAvailableBackups() ([]string, error) {
	entries, err := os.ReadDir(BackupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read backups directory: %w", err)
	}

	var backups []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".zstd" {
			backups = append(backups, filepath.Join(BackupsDir, entry.Name()))
		}
	}

	return backups, nil
}

// addFileToTar adds a single file to a tar archive
func addFileToTar(tw *tar.Writer, filePath string, archiveName string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}

	header.Name = archiveName

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	if _, err := io.Copy(tw, file); err != nil {
		return err
	}

	return nil
}

// addFilesToTar recursively adds files to a tar archive
func addFilesToTar(tw *tar.Writer, basePath string, prefix string) error {
	return filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path for archive
		relPath, err := filepath.Rel(basePath, path)
		if err != nil {
			return err
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		// Set the name in archive using forward slashes for tar format
		header.Name = prefix + "/" + filepath.ToSlash(relPath)

		if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
			header.Name += "/"
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tw, file); err != nil {
				return err
			}
		}

		return nil
	})
}
