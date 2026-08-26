//go:build linux

package installer

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"

	"asa-server/pkg/archive"
)

// SteamCMD's Linux release is a native 32-bit ELF binary (steamcmd.sh, which
// bootstraps and re-execs the real binary) — it runs directly on the host,
// never through umu-run/Wine. See
// docs/LINUX_COMPATIBILITY_PLAN.md §5.5.
const (
	steamCmdURL        = "https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz"
	steamCmdBinaryName = "steamcmd.sh"
	steamCmdArchiveExt = "tar.gz"
)

// extractSteamCmdArchive unpacks the Linux steamcmd_linux.tar.gz release
// (a flat archive, no wrapping top-level directory to strip) and marks
// steamcmd.sh executable — the archive doesn't preserve the exec bit
// through every distribution's tar/gzip toolchain reliably enough to trust.
func extractSteamCmdArchive(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to open steamcmd archive as gzip: %w", err)
	}
	defer gz.Close()

	if err := archive.ExtractTar(gz, destDir, ""); err != nil {
		return err
	}
	return os.Chmod(filepath.Join(destDir, steamCmdBinaryName), 0755)
}
