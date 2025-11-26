package backup

import (
	"archive/tar"
	"asa-server/asaserver"
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
	running, err := asaserver.IsServerRunning(instanceName)
	if err == nil && running {
		logger.GetLogger().Warnf("Server for instance '%s' is running. Stop it before creating a backup.", instanceName)
		return fmt.Errorf("server is running")
	}

	// Load instance configuration to get SaveDir
	config, err := asaserver.LoadInstanceConfig(instanceName)
	if err != nil {
		return fmt.Errorf("failed to load instance config: %w", err)
	}

	saveDir := config.SaveDir
	if saveDir == "" {
		return fmt.Errorf("SaveDir not configured for instance '%s'", instanceName)
	}

	savePath := filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", instanceName)

	if _, err := os.Stat(savePath); err != nil {
		return fmt.Errorf("SaveDir '%s' not found in instance '%s'", saveDir, instanceName)
	}

	// Create backups directory
	if err := os.MkdirAll(asaserver.BackupsDir, 0755); err != nil {
		return fmt.Errorf("failed to create backups directory: %w", err)
	}

	// Create archive name with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	archiveName := fmt.Sprintf("%s_%s.tar.zstd", instanceName, timestamp)
	archivePath := filepath.Join(asaserver.BackupsDir, archiveName)

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

	instanceBaseDir := filepath.Join(asaserver.InstancesDir, instanceName)

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

// RestoreOption defines which components to restore from backup
type RestoreOption struct {
	RestoreWorldfile      bool // Restore worldfile (SaveDir content)
	RestoreInstanceConfig bool // Restore instance_config.ini
	RestoreGameConfig     bool // Restore instanceconfig (Config directory)
}

// RestoreOptionFunc is a function that modifies RestoreOption
type RestoreOptionFunc func(*RestoreOption)

// WithRestoreWorldfile enables restoring worldfile
func WithRestoreWorldfile() RestoreOptionFunc {
	return func(opt *RestoreOption) {
		opt.RestoreWorldfile = true
	}
}

// WithRestoreInstanceConfig enables restoring instance_config.ini
func WithRestoreInstanceConfig() RestoreOptionFunc {
	return func(opt *RestoreOption) {
		opt.RestoreInstanceConfig = true
	}
}

// WithRestoreGameConfig enables restoring game config (Config directory)
func WithRestoreGameConfig() RestoreOptionFunc {
	return func(opt *RestoreOption) {
		opt.RestoreGameConfig = true
	}
}

// WithRestoreAll enables restoring all components
func WithRestoreAll() RestoreOptionFunc {
	return func(opt *RestoreOption) {
		opt.RestoreWorldfile = true
		opt.RestoreInstanceConfig = true
		opt.RestoreGameConfig = true
	}
}

// NewRestoreOption creates a default RestoreOption with all components enabled
func NewRestoreOption(optFuncs ...RestoreOptionFunc) *RestoreOption {
	// Default: restore everything
	opt := &RestoreOption{
		RestoreWorldfile:      false,
		RestoreInstanceConfig: false,
		RestoreGameConfig:     false,
	}

	// Apply option functions
	for _, fn := range optFuncs {
		fn(opt)
	}

	return opt
}

// RestoreBackupToInstance restores a backup to an instance
// If instanceName is empty and instance doesn't exist, creates new instance
// Instance config and game config are optional (may not exist in backup)
// Can customize what to restore using RestoreOptionFunc options
func RestoreBackupToInstance(instanceName string, backupFile string, optFuncs ...RestoreOptionFunc) error {
	// Create restore options
	options := NewRestoreOption(optFuncs...)
	// If instance name is empty, use default or return error
	if instanceName == "" {
		return fmt.Errorf("instance name cannot be empty")
	}

	running, err := asaserver.IsServerRunning(instanceName)
	if err == nil && running {
		logger.GetLogger().Warnf("Server for instance '%s' is running. Stop it before restoring a backup.", instanceName)
		return fmt.Errorf("server is running")
	}

	// Check if backup file exists
	if _, err := os.Stat(backupFile); err != nil {
		return fmt.Errorf("backup file not found: %s", backupFile)
	}

	// Check if instance exists, if not create it
	instanceBaseDir := filepath.Join(asaserver.InstancesDir, instanceName)
	if _, err := os.Stat(instanceBaseDir); os.IsNotExist(err) {
		logger.GetLogger().Infof("Instance '%s' does not exist. Creating new instance...", instanceName)
		// Create instance directory structure
		if err := os.MkdirAll(filepath.Join(instanceBaseDir, "Config"), 0755); err != nil {
			return fmt.Errorf("failed to create instance directory: %w", err)
		}
		// Create default configuration
		config := asaserver.CreateDefaultInstanceConfig(instanceName)
		if err := asaserver.SaveInstanceConfig(instanceName, config); err != nil {
			return fmt.Errorf("failed to create default instance config: %w", err)
		}
		logger.GetLogger().Infof("Instance '%s' created successfully", instanceName)
	}

	// Load instance configuration (may fail if not in backup, that's OK)
	config, configLoadErr := asaserver.LoadInstanceConfig(instanceName)
	if configLoadErr != nil {
		logger.GetLogger().Warnf("Failed to load instance config: %v (will use default)", configLoadErr)
	}

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

	var restoredCount int

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
		case options.RestoreWorldfile && len(name) > 0 && name[0:1] == "w" && len(name) > 9 && name[0:9] == "worldfile":
			// worldfile/* -> SaveDir/...
			relPath := name[9:]
			if len(relPath) > 0 && relPath[0] == '/' {
				relPath = relPath[1:]
			}
			// Use SaveDir from loaded config, or use instance name as default
			saveDir := instanceName
			if configLoadErr == nil && config.SaveDir != "" {
				saveDir = config.SaveDir
			}
			target = filepath.Join(filepath.Join(asaserver.ServerFilesDir, "ShooterGame/Saved", saveDir), relPath)
		case options.RestoreInstanceConfig && name == "instance_config.ini":
			// instance_config.ini -> instanceBaseDir/instance_config.ini (optional)
			target = filepath.Join(instanceBaseDir, "instance_config.ini")
		case options.RestoreGameConfig && len(name) > 0 && name[0:1] == "i" && len(name) > 14 && name[0:14] == "instanceconfig":
			// instanceconfig/* -> instanceBaseDir/Config/... (optional)
			relPath := name[14:]
			if len(relPath) > 0 && relPath[0] == '/' {
				relPath = relPath[1:]
			}
			target = filepath.Join(instanceBaseDir, "Config", relPath)
		default:
			// Skip unknown files or disabled restore options
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
		restoredCount++
	}

	logger.GetLogger().Infof("Backup successfully loaded into instance '%s' (%d files restored).", instanceName, restoredCount)
	return nil
}

// GetAvailableBackups returns a list of available backups
func GetAvailableBackups() ([]string, error) {
	entries, err := os.ReadDir(asaserver.BackupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read backups directory: %w", err)
	}

	var backups []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".zstd" {
			backups = append(backups, filepath.Join(asaserver.BackupsDir, entry.Name()))
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
