//go:build linux

package runner

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"asa-server/internal/logger"
	"asa-server/pkg/download"
	"asa-server/pkg/procx"
)

// Directory layout, all rooted at Config.BaseDir — see
// docs/LINUX_COMPATIBILITY_PLAN.md §4.1.
func umuDir(cfg Config) string        { return filepath.Join(cfg.BaseDir, "umu-launcher") }
func umuRunPath(cfg Config) string    { return filepath.Join(umuDir(cfg), "umu-run") }
func protonBaseDir(cfg Config) string { return filepath.Join(cfg.BaseDir, "proton") }
func protonPath(cfg Config) string {
	return filepath.Join(protonBaseDir(cfg), cfg.ProtonVersion)
}

// prefixDir resolves the Wine prefix directory for one launch. key is
// opt.PrefixKey; empty means the default shared prefix regardless of
// PrefixMode (per-instance callers must supply a key to actually get
// isolation — see docs/LINUX_COMPATIBILITY_PLAN.md §6 risk 6).
func prefixDir(cfg Config, key string) string {
	base := cfg.PrefixDir
	if base == "" {
		base = filepath.Join(cfg.BaseDir, "umu-prefix")
	}
	if cfg.PrefixMode == "per-instance" && key != "" {
		return base + "-" + key
	}
	return base
}

// runtimeMu serializes EnsureRuntime: concurrent first-time initialization
// (two instances starting at once on a fresh install) would otherwise race
// on the same umu-run/GE-Proton download and prefix warm-up — see
// docs/LINUX_COMPATIBILITY_PLAN.md §6 risk 6.
var runtimeMu sync.Mutex

// ensureRuntime downloads umu-run + the pinned GE-Proton build if missing,
// and warms the default shared Wine prefix. Mirrors
// scripts/ark_instance_manager.sh's install_base_server() umu/Proton
// section — that script is the verified reference this logic is copied
// from, not re-derived.
func ensureRuntime(ctx context.Context, progress io.Writer) error {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()

	cfg := getConfig()
	logf := progressLogger(progress)

	if cfg.Runtime == "custom" {
		logf("linux runtime mode is \"custom\": skipping umu/GE-Proton download, expecting a pre-configured PROTONPATH")
		return nil
	}
	if !cfg.AutoDownload {
		return fmt.Errorf("runner: auto_download is disabled and runtime is not fully installed (see GET /api/system/preflight)")
	}

	if err := ensureUmu(ctx, cfg, logf); err != nil {
		return fmt.Errorf("failed to install umu-launcher: %w", err)
	}
	if err := ensureGEProton(ctx, cfg, logf); err != nil {
		return fmt.Errorf("failed to install %s: %w", cfg.ProtonVersion, err)
	}
	if err := warmPrefix(ctx, cfg, logf); err != nil {
		return fmt.Errorf("failed to prepare Wine prefix: %w", err)
	}
	return nil
}

func progressLogger(w io.Writer) func(format string, args ...any) {
	return func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		logger.GetLogger().Info(msg)
		if w != nil {
			fmt.Fprintln(w, msg)
		}
	}
}

// ensureUmu downloads+extracts the umu-launcher zipapp if umu-run isn't
// already present at the pinned version's expected path.
func ensureUmu(ctx context.Context, cfg Config, logf func(string, ...any)) error {
	bin := umuRunPath(cfg)
	if fi, err := os.Stat(bin); err == nil && fi.Mode()&0111 != 0 {
		return nil
	}

	const owner, repo = "Open-Wine-Components", "umu-launcher"
	asset := fmt.Sprintf("umu-launcher-%s-zipapp.tar", cfg.UmuVersion)
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, cfg.UmuVersion, asset)

	logf("downloading umu-launcher %s from %s", cfg.UmuVersion, url)

	// umu's release page publishes no standalone checksum file (unlike
	// GE-Proton's .sha512sum) — the only trustworthy value is the digest
	// GitHub computes for the asset, exposed via the Releases API. This is
	// a single small metadata GET (not a "resolve latest" call, and not on
	// the hot path of every server start), so it doesn't reintroduce the
	// api.github.com rate-limit problem §4.3 warns about — that problem is
	// specifically about resolving "latest"/aliases on every run.
	// A failure here degrades to an unverified download rather than
	// blocking setup entirely: umu-run is a small, auditable launcher, not
	// the large untrusted payload GE-Proton is.
	checksum := ""
	if digest, err := fetchGithubAssetDigest(ctx, owner, repo, cfg.UmuVersion, asset); err != nil {
		logf("warning: could not fetch umu-launcher checksum (%v); proceeding without verification", err)
	} else {
		checksum = digest
	}

	archivePath := filepath.Join(umuDir(cfg), asset)
	if err := download.Fetch(ctx, download.Options{
		URL: url, Dest: archivePath, Checksum: checksum, Resume: true,
	}); err != nil {
		return err
	}
	defer os.Remove(archivePath)

	// The tar contains a single "umu/" directory (umu-run + a symlink);
	// strip that prefix so umu-run lands directly at umuDir(cfg)/umu-run.
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := extractTar(f, umuDir(cfg), "umu/"); err != nil {
		return fmt.Errorf("failed to extract umu-launcher archive: %w", err)
	}

	if err := os.Chmod(bin, 0755); err != nil {
		return fmt.Errorf("failed to make umu-run executable: %w", err)
	}
	logf("umu-launcher %s installed at %s", cfg.UmuVersion, umuDir(cfg))
	return nil
}

// ensureGEProton downloads+extracts the pinned GE-Proton build if its
// `proton` entry point isn't already present.
func ensureGEProton(ctx context.Context, cfg Config, logf func(string, ...any)) error {
	protonBin := filepath.Join(protonPath(cfg), "proton")
	if fi, err := os.Stat(protonBin); err == nil && !fi.IsDir() {
		return nil
	}

	const owner, repo = "GloriousEggroll", "proton-ge-custom"
	tag := cfg.ProtonVersion
	assetName := tag + ".tar.gz"
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, tag, assetName)

	logf("downloading %s (~450 MB, this can take a while)", tag)

	checksum, err := fetchSha512Checksum(ctx, owner, repo, tag, assetName)
	if err != nil {
		// Unlike umu, GE-Proton is a large third-party binary blob running
		// as the actual game process's runtime — docs/LINUX_COMPATIBILITY_PLAN.md
		// risk #17 is explicit that a proxy/CDN silently returning a
		// truncated or tampered file here fails in the worst possible way
		// (GE-Proton11-style silent hang, no log line at all). Refuse to
		// proceed without a verified checksum.
		return fmt.Errorf("failed to fetch published checksum for %s (refusing to download unverified): %w", assetName, err)
	}

	archivePath := filepath.Join(protonBaseDir(cfg), assetName)
	if err := download.Fetch(ctx, download.Options{
		URL: url, Dest: archivePath, Checksum: checksum, Resume: true,
	}); err != nil {
		return err
	}
	defer os.Remove(archivePath)

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to open %s as gzip: %w", assetName, err)
	}
	defer gz.Close()

	// No strip-prefix: the archive's own top-level entry is "GE-Proton10-34/",
	// which is exactly the directory name we want under protonBaseDir.
	if err := extractTar(gz, protonBaseDir(cfg), ""); err != nil {
		return fmt.Errorf("failed to extract %s: %w", assetName, err)
	}

	if fi, statErr := os.Stat(protonBin); statErr != nil || fi.IsDir() {
		return fmt.Errorf("%s extracted but %s is missing — the archive layout may have changed", tag, protonBin)
	}
	logf("%s installed at %s", tag, protonPath(cfg))
	return nil
}

// warmPrefix runs `umu-run wineboot --init` once to force both the Steam
// Linux Runtime download and Wine prefix creation, exactly as
// scripts/ark_instance_manager.sh's install_base_server() does — including
// its two preconditions: a version-mismatch check that recreates a prefix
// left by an incompatible Proton generation, and a wineserver-drain poll
// afterward so a caller-visible "ready" doesn't race the prefix still being
// held open.
func warmPrefix(ctx context.Context, cfg Config, logf func(string, ...any)) error {
	prefix := prefixDir(cfg, "")
	if err := os.MkdirAll(prefix, 0755); err != nil {
		return err
	}

	if err := reconcilePrefixVersion(prefix, cfg.ProtonVersion, logf); err != nil {
		return err
	}

	prefixReady := fileExists(filepath.Join(prefix, "system.reg")) &&
		dirExists(filepath.Join(prefix, "drive_c", "windows", "system32"))
	runtimeReady := steamLinuxRuntimeReady(cfg.ProtonVersion)

	if prefixReady && runtimeReady {
		return writePrefixMarker(prefix, cfg.ProtonVersion)
	}

	logf("first-time umu setup: downloading Steam Linux Runtime and initializing the Wine prefix (this can take several minutes)")

	bin := umuRunPath(cfg)
	cmd := exec.CommandContext(ctx, bin, "wineboot", "--init")
	cmd.Env = append(os.Environ(),
		"WINEPREFIX="+prefix,
		"GAMEID="+cfg.GameID,
		"PROTONPATH="+protonPath(cfg),
		// Deliberately no UMU_RUNTIME_UPDATE=0 here: this is the one
		// invocation that must be allowed to fetch a missing runtime.
	)
	cmd.Stdout = progressWriter{logf}
	cmd.Stderr = progressWriter{logf}
	// Best-effort like the reference script (`|| true`): a non-zero exit
	// from wineboot doesn't necessarily mean the prefix wasn't created.
	_ = cmd.Run()

	waitForWineserverDrain(prefix)

	logf("umu runtime and Wine prefix ready")
	return writePrefixMarker(prefix, cfg.ProtonVersion)
}

type progressWriter struct{ logf func(string, ...any) }

func (w progressWriter) Write(p []byte) (int, error) {
	if line := strings.TrimSpace(string(p)); line != "" {
		w.logf("%s", line)
	}
	return len(p), nil
}

// reconcilePrefixVersion moves an existing prefix aside if it was created
// by a different Proton build than cfg.ProtonVersion (Wine prefixes don't
// tolerate cross-generation reuse — see
// docs/LINUX_COMPATIBILITY_PLAN.md §6 risk 5). A prefix with no marker at
// all is treated as unknown provenance and rebuilt too — that covers every
// prefix created before this mechanism existed, including ones from the
// briefly-pinned, ASA-incompatible GE-Proton11-1.
func reconcilePrefixVersion(prefix, wantVersion string, logf func(string, ...any)) error {
	markerPath := filepath.Join(prefix, ".created-by-proton")
	if !fileExists(filepath.Join(prefix, "system.reg")) {
		return nil // nothing to reconcile yet
	}

	got, _ := os.ReadFile(markerPath)
	gotVersion := strings.TrimSpace(string(got))
	if gotVersion == wantVersion {
		return nil
	}

	backup := prefix + ".bak-" + firstNonEmpty(gotVersion, "unknown")
	logf("existing Wine prefix was created by %q, current is %q; moving it to %s and creating a fresh prefix",
		firstNonEmpty(gotVersion, "an unknown Proton build"), wantVersion, backup)

	_ = os.RemoveAll(backup)
	if err := os.Rename(prefix, backup); err != nil {
		return fmt.Errorf("failed to move aside stale prefix: %w", err)
	}
	return os.MkdirAll(prefix, 0755)
}

func writePrefixMarker(prefix, version string) error {
	return os.WriteFile(filepath.Join(prefix, ".created-by-proton"), []byte(version), 0644)
}

// steamLinuxRuntimeReady checks for the toolmanifest the Proton generation
// behind version needs under ~/.local/share/umu (umu's own runtime cache,
// independent of WINEPREFIX — see docs/LINUX_COMPATIBILITY_PLAN.md §4.1).
// GE-Proton 9/10 use steamrt3 ("sniper"), GE-Proton 11 uses steamrt4; a
// present steamrt4 must not mask a missing steamrt3 after a downgrade, so
// an unrecognized/future generation conservatively accepts any installed
// runtime rather than forcing a re-download.
func steamLinuxRuntimeReady(protonVersion string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	glob := "steamrt*"
	switch {
	case strings.HasPrefix(protonVersion, "GE-Proton9-"), strings.HasPrefix(protonVersion, "GE-Proton10-"):
		glob = "steamrt3"
	case strings.HasPrefix(protonVersion, "GE-Proton11-"):
		glob = "steamrt4"
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".local/share/umu", glob, "toolmanifest.vdf"))
	return len(matches) > 0
}

// waitForWineserverDrain polls (up to 90s, matching the reference script)
// for no wineserver process to still be holding prefix open, so a
// caller-visible "runtime ready" doesn't race a prefix that's still being
// written to.
func waitForWineserverDrain(prefix string) {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		procs, err := procx.QueryProcess("wineserver", prefix)
		if err != nil || len(procs) == 0 {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- Checksums ---

type ghAsset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type ghRelease struct {
	Assets []ghAsset `json:"assets"`
}

// fetchGithubAssetDigest fetches the sha256 digest GitHub computes for one
// release asset via the Releases API (GET .../releases/tags/{tag}, a single
// small metadata request — not the "resolve latest" pattern
// docs/LINUX_COMPATIBILITY_PLAN.md §4.3 avoids). Uses download.Client() so
// it still honors the configured corporate HTTPProxy fallback.
func fetchGithubAssetDigest(ctx context.Context, owner, repo, tag, assetName string) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := download.Client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api returned %s", resp.Status)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	for _, a := range rel.Assets {
		if a.Name == assetName {
			if a.Digest == "" {
				return "", fmt.Errorf("asset %s has no digest in the API response", assetName)
			}
			return a.Digest, nil
		}
	}
	return "", fmt.Errorf("asset %s not found in release %s", assetName, tag)
}

// fetchSha512Checksum downloads GE-Proton's published "<tag>.sha512sum"
// companion file (a normal release-download URL — proxy-able, not
// rate-limited, unlike the GitHub API) and extracts the hash for
// assetName. The file's format is the standard `sha512sum` tool output:
// "<hex>  <filename>" per line.
func fetchSha512Checksum(ctx context.Context, owner, repo, tag, assetName string) (string, error) {
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s.sha512sum", owner, repo, tag, tag)

	tmp, err := os.CreateTemp("", "ge-proton-sha512sum-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := download.Fetch(ctx, download.Options{URL: url, Dest: tmpPath}); err != nil {
		return "", err
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash, name := fields[0], fields[1]
		if strings.TrimPrefix(name, "*") == assetName {
			return "sha512:" + hash, nil
		}
	}
	return "", fmt.Errorf("%s.sha512sum has no entry for %s", tag, assetName)
}
