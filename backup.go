package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// BackupInstanceWorld creates a backup of an instance world
func BackupInstanceWorld(instanceName string, worldFolder string) error {
	running, err := IsServerRunning(instanceName)
	if err == nil && running {
		fmt.Printf("❌ Server for instance '%s' is running. Stop it before creating a backup.\n", instanceName)
		return fmt.Errorf("server is running")
	}

	instanceDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved", instanceName)
	worldPath := filepath.Join(instanceDir, worldFolder)

	if _, err := os.Stat(worldPath); err != nil {
		return fmt.Errorf("world folder '%s' not found in instance '%s'", worldFolder, instanceName)
	}

	// Create backups directory
	if err := os.MkdirAll(BackupsDir, 0755); err != nil {
		return fmt.Errorf("failed to create backups directory: %w", err)
	}

	// Create archive name with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	archiveName := fmt.Sprintf("%s_%s_%s.tar.gz", instanceName, worldFolder, timestamp)
	archivePath := filepath.Join(BackupsDir, archiveName)

	fmt.Printf("📦 Creating backup for world: %s...\n", worldFolder)

	// Create the archive
	file, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("failed to create archive file: %w", err)
	}
	defer file.Close()

	gw := gzip.NewWriter(file)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Add files to archive
	if err := addFilesToArchive(tw, worldPath, worldFolder); err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("failed to create archive: %w", err)
	}

	fmt.Printf("✅ Backup successfully created: %s\n", archivePath)
	return nil
}

// RestoreBackupToInstance restores a backup to an instance
func RestoreBackupToInstance(instanceName string, backupFile string) error {
	running, err := IsServerRunning(instanceName)
	if err == nil && running {
		fmt.Printf("❌ Server for instance '%s' is running. Stop it before restoring a backup.\n", instanceName)
		return fmt.Errorf("server is running")
	}

	// Check if backup file exists
	if _, err := os.Stat(backupFile); err != nil {
		return fmt.Errorf("backup file not found: %s", backupFile)
	}

	// Create target directory
	targetDir := filepath.Join(ServerFilesDir, "ShooterGame/Saved", instanceName)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	fmt.Printf("📂 Extracting backup to instance '%s'...\n", instanceName)

	// Extract archive
	file, err := os.Open(backupFile)
	if err != nil {
		return fmt.Errorf("failed to open backup file: %w", err)
	}
	defer file.Close()

	gr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// Create the target path
		target := filepath.Join(targetDir, header.Name)

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

		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return fmt.Errorf("failed to write file: %w", err)
		}
		f.Close()
	}

	fmt.Printf("✅ Backup successfully loaded into instance '%s'.\n", instanceName)
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
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".gz" {
			backups = append(backups, filepath.Join(BackupsDir, entry.Name()))
		}
	}

	return backups, nil
}

// addFilesToArchive recursively adds files to a tar archive
func addFilesToArchive(tw *tar.Writer, basePath string, prefix string) error {
	return filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path for archive
		relPath, err := filepath.Rel(filepath.Dir(basePath), path)
		if err != nil {
			return err
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		// Set the name in archive
		header.Name = filepath.Join(prefix, relPath)

		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// If it's not a directory, write the content
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
