package backup

import (
	"archive/tar"
	"asa-server/asaserver"
	"asa-server/logger"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// createWorldArchive packs the instance's Save/ directory into archivePath.
// The archive contains only world-save content, unwrapped at the tar root.
// On failure the partial archive is removed.
func createWorldArchive(instanceName, archivePath string) error {
	instanceBaseDir := filepath.Join(asaserver.InstancesDir, instanceName)
	savePath := filepath.Join(instanceBaseDir, "Save")

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

	if err := addFilesToTar(tarWriter, savePath); err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("failed to add SaveDir to archive: %w", err)
	}

	return nil
}

// pruneOldWorldBackups keeps at most 10 regular backups (latest snapshot excluded).
// Oldest by modification time are removed first.
func pruneOldWorldBackups(instanceName string) error {
	backupDir := filepath.Join(asaserver.InstancesDir, instanceName, "backup")
	latestName := instanceName + "_latest.zstd"

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil
	}

	type fileEntry struct {
		name    string
		modTime time.Time
	}
	var regular []fileEntry
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".zstd" || e.Name() == latestName {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		regular = append(regular, fileEntry{name: e.Name(), modTime: info.ModTime()})
	}

	if len(regular) <= 10 {
		return nil
	}

	sort.Slice(regular, func(i, j int) bool {
		return regular[i].modTime.Before(regular[j].modTime)
	})

	toDelete := regular[:len(regular)-10]
	for _, f := range toDelete {
		path := filepath.Join(backupDir, f.name)
		if err := os.Remove(path); err != nil {
			logger.GetLogger().Warnf("Failed to prune old backup '%s': %v", f.name, err)
		} else {
			logger.GetLogger().Infof("Pruned old backup: %s", f.name)
		}
	}

	return nil
}

// BackupInstanceWorld creates a timestamped backup of an instance and prunes old ones.
func BackupInstanceWorld(instanceName string) error {
	config, err := asaserver.LoadInstanceConfig(instanceName)
	if err != nil {
		return fmt.Errorf("failed to load instance config: %w", err)
	}

	saveDir := config.SaveDir
	if saveDir == "" {
		return fmt.Errorf("SaveDir not configured for instance '%s'", instanceName)
	}

	savePath := filepath.Join(asaserver.InstancesDir, instanceName, "Save")
	if _, err := os.Stat(savePath); err != nil {
		return fmt.Errorf("SaveDir '%s' not found in instance '%s'", saveDir, instanceName)
	}

	backupDir := filepath.Join(asaserver.InstancesDir, instanceName, "backup")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	archiveName := fmt.Sprintf("%s_%s.zstd", instanceName, timestamp)
	archivePath := filepath.Join(backupDir, archiveName)

	logger.GetLogger().Infof("Creating backup for instance '%s': %s", instanceName, archiveName)
	if err := createWorldArchive(instanceName, archivePath); err != nil {
		return err
	}
	logger.GetLogger().Infof("Backup created: %s", archivePath)

	return pruneOldWorldBackups(instanceName)
}

// BackupLatestWorldSnapshot creates or overwrites the latest snapshot for an instance.
// Called automatically before restoring to preserve the current state.
func BackupLatestWorldSnapshot(instanceName string) error {
	backupDir := filepath.Join(asaserver.InstancesDir, instanceName, "backup")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	latestPath := filepath.Join(backupDir, instanceName+"_latest.zstd")
	logger.GetLogger().Infof("Creating latest snapshot for instance '%s'", instanceName)
	return createWorldArchive(instanceName, latestPath)
}

// DeleteWorldBackup deletes a specific backup file from the instance's backup directory.
func DeleteWorldBackup(instanceName, filename string) error {
	backupPath := filepath.Join(asaserver.InstancesDir, instanceName, "backup", filename)
	if _, err := os.Stat(backupPath); err != nil {
		return fmt.Errorf("backup file not found: %s", filename)
	}
	return os.Remove(backupPath)
}

// RestoreInstanceWorld restores the world save from a backup to an instance.
// backupFile must be a full absolute path to the archive.
func RestoreInstanceWorld(instanceName string, backupFile string) error {
	if instanceName == "" {
		return fmt.Errorf("instance name cannot be empty")
	}

	running, err := asaserver.IsServerRunning(instanceName)
	if err == nil && running {
		logger.GetLogger().Warnf("Server for instance '%s' is running. Stop it before restoring a backup.", instanceName)
		return fmt.Errorf("server is running")
	}

	if _, err := os.Stat(backupFile); err != nil {
		return fmt.Errorf("backup file not found: %s", backupFile)
	}

	instanceBaseDir := filepath.Join(asaserver.InstancesDir, instanceName)
	if _, err := os.Stat(instanceBaseDir); os.IsNotExist(err) {
		logger.GetLogger().Infof("Instance '%s' does not exist. Creating new instance...", instanceName)
		if err := os.MkdirAll(filepath.Join(instanceBaseDir, "Config"), 0755); err != nil {
			return fmt.Errorf("failed to create instance directory: %w", err)
		}
		config := asaserver.CreateDefaultInstanceConfig(instanceName)
		if err := asaserver.SaveInstanceConfig(instanceName, config); err != nil {
			return fmt.Errorf("failed to create default instance config: %w", err)
		}
		logger.GetLogger().Infof("Instance '%s' created successfully", instanceName)
	}

	_, configLoadErr := asaserver.LoadInstanceConfig(instanceName)
	if configLoadErr != nil {
		logger.GetLogger().Warnf("Failed to load instance config: %v (will use default)", configLoadErr)
	}

	logger.GetLogger().Infof("Extracting backup to instance '%s'...", instanceName)

	file, err := os.Open(backupFile)
	if err != nil {
		return fmt.Errorf("failed to open backup file: %w", err)
	}
	defer file.Close()

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

		target := filepath.Join(asaserver.InstancesDir, instanceName, "Save", header.Name)

		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			continue
		}

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

// GetAvailableWorldBackups returns a list of available backup filenames for the given instance.
func GetAvailableWorldBackups(instanceName string) ([]string, error) {
	backupDir := filepath.Join(asaserver.InstancesDir, instanceName, "backup")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read backup directory: %w", err)
	}

	var backups []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".zstd" {
			backups = append(backups, entry.Name())
		}
	}

	return backups, nil
}

// addFilesToTar recursively adds files to a tar archive, rooted at basePath
// (the walk root itself is not written as an entry).
func addFilesToTar(tw *tar.Writer, basePath string) error {
	return filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(basePath, path)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		header.Name = filepath.ToSlash(relPath)

		if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
			header.Name += "/"
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !info.IsDir() {
			if err := func() error {
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				defer file.Close()

				if _, err := io.Copy(tw, file); err != nil {
					return err
				}
				return nil
			}(); err != nil {
				return err
			}
		}

		return nil
	})
}
