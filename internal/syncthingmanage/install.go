package syncthingmanage

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"asa-server/pkg/download"
	"asa-server/pkg/logger"
)

// syncthingVersion is pinned rather than "latest" for the same reason
// GE-Proton is pinned in the Linux runtime plan: a fixed, previously-verified
// version beats surprise upgrades and unauthenticated GitHub API lookups.
const syncthingVersion = "2.0.11"

// binaryName is the executable name inside the release archive and on disk.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "syncthing.exe"
	}
	return "syncthing"
}

// ensureSyncthingBinary returns the path to a working syncthing binary in
// dir, downloading and unpacking the pinned release from GitHub if it isn't
// there yet. dir is expected to already exist.
func ensureSyncthingBinary(ctx context.Context, dir string) (string, error) {
	binPath := filepath.Join(dir, binaryName())
	if fi, err := os.Stat(binPath); err == nil && fi.Mode().IsRegular() {
		return binPath, nil
	}

	assetName, ext, err := releaseAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf(
		"https://github.com/syncthing/syncthing/releases/download/v%s/%s",
		syncthingVersion, assetName,
	)
	archivePath := filepath.Join(dir, assetName)

	logger.Infof("downloading syncthing v%s from %s", syncthingVersion, url)
	if err := download.Fetch(ctx, download.Options{URL: url, Dest: archivePath, Resume: true}); err != nil {
		return "", fmt.Errorf("failed to download syncthing: %w", err)
	}
	defer os.Remove(archivePath)

	if ext == "zip" {
		err = extractBinaryFromZip(archivePath, binaryName(), binPath)
	} else {
		err = extractBinaryFromTarGz(archivePath, binaryName(), binPath)
	}
	if err != nil {
		return "", fmt.Errorf("failed to extract syncthing binary: %w", err)
	}

	// no-op on Windows; on Linux the extracted file needs the exec bit.
	if err := os.Chmod(binPath, 0755); err != nil {
		return "", fmt.Errorf("failed to make syncthing binary executable: %w", err)
	}
	return binPath, nil
}

// releaseAsset maps a Go GOOS/GOARCH pair to the matching syncthing release
// asset name and archive format. Only the two platforms this program
// actually ships for are wired up.
func releaseAsset(goos, goarch string) (name, ext string, err error) {
	switch goos {
	case "windows":
		ext = "zip"
	case "linux":
		ext = "tar.gz"
	default:
		return "", "", fmt.Errorf("syncthingmanage: unsupported GOOS %q", goos)
	}
	return fmt.Sprintf("syncthing-%s-%s-v%s.%s", goos, goarch, syncthingVersion, ext), ext, nil
}

// extractBinaryFromZip pulls the single entry named entryBase out of a zip
// archive (Windows release asset) and writes it to destPath, without
// extracting anything else in the archive.
func extractBinaryFromZip(archivePath, entryBase, destPath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if filepath.Base(f.Name) != entryBase {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeBinary(destPath, rc)
	}
	return fmt.Errorf("archive %s has no entry named %s", archivePath, entryBase)
}

// extractBinaryFromTarGz is the tar.gz equivalent of extractBinaryFromZip
// (Linux release assets ship as .tar.gz, not .zip).
func extractBinaryFromTarGz(archivePath, entryBase, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != entryBase {
			continue
		}
		return writeBinary(destPath, tr)
	}
	return fmt.Errorf("archive %s has no entry named %s", archivePath, entryBase)
}

// writeBinary streams src to a fresh file at destPath.
func writeBinary(destPath string, src io.Reader) error {
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, src)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(destPath)
		return copyErr
	}
	return closeErr
}
