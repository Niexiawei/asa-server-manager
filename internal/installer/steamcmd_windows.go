//go:build windows

package installer

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// steamCmdURL / steamCmdBinaryName were previously cfgpkg.SteamCmdURL and a
// hardcoded "steamcmd.exe" — moved here because config shouldn't know
// download URLs (see docs/LINUX_COMPATIBILITY_PLAN.md §5.5), and because
// Linux needs a different URL, archive format and binary name entirely.
const (
	steamCmdURL        = "https://steamcdn-a.akamaihd.net/client/installer/steamcmd.zip"
	steamCmdBinaryName = "steamcmd.exe"
	steamCmdArchiveExt = "zip"
)

// extractSteamCmdArchive unpacks the Windows steamcmd.zip release.
func extractSteamCmdArchive(archivePath, destDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, file := range reader.File {
		fpath := filepath.Join(destDir, file.Name)

		// Zip Slip protection: ensure the path is within destDir
		if !strings.HasPrefix(filepath.Clean(fpath), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in zip: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		infile, err := file.Open()
		if err != nil {
			return err
		}

		outfile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			infile.Close()
			return err
		}

		_, err = io.Copy(outfile, infile)
		infile.Close()
		outfile.Close()

		if err != nil {
			return err
		}
	}

	return nil
}
