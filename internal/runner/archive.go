package runner

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractTar extracts a tar stream (caller has already unwrapped any gzip
// layer) into destDir. When stripPrefix is non-empty, that leading path
// component is removed from every entry's name and entries outside it are
// skipped (the `tar --strip-components=1` equivalent for a single known
// top-level directory). Every entry's target is verified to stay under
// destDir first — a maliciously crafted archive with ".." path segments
// (zip-slip) must not be able to write outside it.
//
// No build constraint: this is pure archive/tar + stdlib I/O with nothing
// platform-specific in it, only used today by umu_linux.go's umu-launcher
// and GE-Proton installation — kept unconstrained so it can be unit tested
// on any host, not just Linux.
func extractTar(r io.Reader, destDir, stripPrefix string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	cleanDest := filepath.Clean(destDir)

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		name := hdr.Name
		if stripPrefix != "" {
			if !strings.HasPrefix(name, stripPrefix) {
				continue
			}
			name = strings.TrimPrefix(name, stripPrefix)
			if name == "" {
				continue
			}
		}

		target := filepath.Join(destDir, filepath.FromSlash(name))
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("tar entry %q escapes destination directory", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			os.Remove(target) // allow re-extraction over a previous run
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			linkTarget := filepath.Join(destDir, filepath.FromSlash(hdr.Linkname))
			os.Remove(target)
			if err := os.Link(linkTarget, target); err != nil {
				return err
			}
		}
	}
	return nil
}
