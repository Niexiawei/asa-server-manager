// Package steamrt prefetches the Steam Linux Runtime archive a given GE-Proton
// build needs, into umu-launcher's own download cache, so umu's later
// wineboot-triggered download only has to resume the last sliver of the file
// instead of pulling 150-190 MB with no retry/proxy/timeout control of its
// own. It does not know about ASA, instances, or config — the caller supplies
// the Proton install to inspect and the cache directory to prefetch into.
//
// This file holds the pure logic: variant mapping, archive naming, and
// SHA256SUMS parsing. Deliberately free of a //go:build tag — like
// asa-server/pkg/archive, it is all string/file parsing with no
// platform-specific API, and "the steamrt4 archive is named
// SteamLinuxRuntime_4.tar.xz, not _sniper" is exactly the kind of fact that
// needs a test that runs on every host. See docs/STEAMRT_PREFETCH_PLAN.md
// §2.2.
package steamrt

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Host is Valve's official runtime repository — also the one umu itself
// hardcodes. Deliberately not a parameter: a caller that needs a proxy
// already routes through it via the http.Client it hands to requests made
// against this host (this package's Prefetch uses asa-server/pkg/download,
// whose client honors download.http_proxy).
const Host = "https://repo.steampowered.com"

// TailBytes is the tail length a prefetched cache file **deliberately**
// leaves missing.
//
// This is not caution, it is required. Once umu finds a cache hit it issues
// "Range: bytes=<cache size>-" to resume, and Host (a Google Edge Cache) does
// not answer an **out-of-range** Range with 416 — it answers with 200 and the
// full body:
//
//	Range: bytes=193105864-   =>  206  Content-Range: bytes 193105864-193105963/193105964
//	Range: bytes=193105964-   =>  200  Content-Length: 193105964      ← out of range, full body
//
// So a "complete" file in the cache makes umu append the entire archive again
// in "ab+" mode, doubling the file's length; its sha256 then fails to match
// and it raises "Digest mismatched" — that is not "the optimization didn't
// help", it is "a machine that would have installed fine now can't". Leaving
// a 1 MiB tail unwritten puts the Range squarely mid-file (206 confirmed by
// testing); once umu completes it, the file matches upstream byte-for-byte
// and umu's own checksum passes normally.
//
// 1 MiB rather than 1 byte: wide enough to steer clear of any CDN's edge
// behavior on a tiny Range; the cost is one extra 1 MiB transfer, negligible.
// See docs/STEAMRT_PREFETCH_PLAN.md §2.5 / §4.3.
const TailBytes = 1 << 20

// StampSuffix marks "dest is already tail-truncated and ready for umu to
// resume". Its content is "<sha256> <post-truncation size>", letting a rerun
// skip a full redownload.
const StampSuffix = ".asa-prefetch"

// Variant describes one Steam Linux Runtime variant.
type Variant struct {
	// Variant is both the path segment on Host and the directory name under
	// umu's UMU_LOCAL.
	Variant string
	// Codename is the "name" field in umu's RUNTIME_VERSIONS (the archive
	// name is derived from it).
	Codename string
	// Archive is the tar.xz file name to download.
	Archive string
}

// Steam's compat-tool appids — the same values a GE-Proton's toolmanifest.vdf
// require_tool_appid field carries.
const (
	appIDSniper   = "1628350" // Steam Linux Runtime 3.0 (sniper)
	appIDSteamrt4 = "4183110" // Steam Linux Runtime 4.0
)

// byAppID is the subset of umu 1.4.4's umu_runtime.py RUNTIME_VERSIONS table
// this package understands.
//
// x86_64 only: the ARK dedicated server has no arm64 build, so listing
// steamrt4-arm64 here would only add a branch that's never exercised and
// therefore never verified. An unrecognized variant returns false — let umu
// decide for itself rather than guess.
//
// The archive name is not "variant with a suffix slapped on" — umu derives
// it as:
//
//	if codename.removeprefix("steamrt").removesuffix("-arm64").isdigit():
//	    archive = f"SteamLinuxRuntime_{codename.removeprefix('steamrt')}.tar.xz"
//	else:
//	    archive = f"SteamLinuxRuntime_{codename}.tar.xz"
//
// sniper yields SteamLinuxRuntime_sniper.tar.xz; steamrt4 yields
// SteamLinuxRuntime_4.tar.xz — the latter is the one most likely to get
// written as _sniper by mistake, and that mistake is a 404.
var byAppID = map[string]Variant{
	appIDSniper:   {Variant: "steamrt3", Codename: "sniper", Archive: "SteamLinuxRuntime_sniper.tar.xz"},
	appIDSteamrt4: {Variant: "steamrt4", Codename: "steamrt4", Archive: "SteamLinuxRuntime_4.tar.xz"},
}

var requireToolAppIDRe = regexp.MustCompile(`(?i)"require_tool_appid"\s+"(\d+)"`)

// ForProton resolves which Steam Linux Runtime variant a given GE-Proton
// build needs.
//
// The authoritative source is {protonDir}/toolmanifest.vdf's
// require_tool_appid — umu itself resolves it the same way
// (CompatLayer.required_runtime), so following suit avoids diverging from
// umu's own judgment across a GE-Proton upgrade. When it isn't installed yet,
// or the manifest format changed, this falls back to a version-name-prefix
// heuristic; failing that too, it returns false ("don't know, don't guess").
func ForProton(protonDir, protonVersion string) (Variant, bool) {
	if appID, ok := protonRequiredToolAppID(protonDir); ok {
		v, known := byAppID[appID]
		return v, known
	}
	return forProtonVersion(protonVersion)
}

// protonRequiredToolAppID digs require_tool_appid out of toolmanifest.vdf.
// A regexp rather than a VDF parser: there is exactly one scalar field to
// read and the manifest is a handful of lines.
func protonRequiredToolAppID(protonDir string) (string, bool) {
	if protonDir == "" {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(protonDir, "toolmanifest.vdf"))
	if err != nil {
		return "", false
	}
	m := requireToolAppIDRe.FindSubmatch(data)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

// forProtonVersion is the version-name-prefix fallback: GE-Proton 9/10 use
// steamrt3 (sniper), GE-Proton 11 uses steamrt4. Only consulted when
// toolmanifest.vdf can't be read.
func forProtonVersion(protonVersion string) (Variant, bool) {
	switch {
	case strings.HasPrefix(protonVersion, "GE-Proton9-"), strings.HasPrefix(protonVersion, "GE-Proton10-"):
		return byAppID[appIDSniper], true
	case strings.HasPrefix(protonVersion, "GE-Proton11-"):
		return byAppID[appIDSteamrt4], true
	}
	return Variant{}, false
}

// CacheName reproduces umu's own cache file naming: f"{archive}.{buildid}.parts".
// A name mismatch causes no error, just an empty cache — umu falls back to a
// full download.
func CacheName(v Variant, buildID string) string {
	return v.Archive + "." + buildID + ".parts"
}

func imagesURL(v Variant) string {
	return Host + "/" + v.Variant + "/images"
}

func fileURL(v Variant, version, name string) string {
	return imagesURL(v) + "/" + version + "/" + name
}

// tokenRe bounds the accepted characters for version / BUILD_ID. Both come
// from the remote end and get spliced into a URL, and BUILD_ID becomes part
// of a cache file name too — skipping validation would let upstream (or a
// man-in-the-middle, or a misconfigured mirror) decide which path we write
// to.
var tokenRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)

// SafeToken validates a version/BUILD_ID-shaped token.
func SafeToken(kind, s string) (string, error) {
	s = strings.TrimSpace(s)
	if !tokenRe.MatchString(s) || strings.Contains(s, "..") {
		return "", fmt.Errorf("%s 取值异常: %q", kind, s)
	}
	return s, nil
}

// ParseSHA256Sums extracts the digest for name out of a sha256sum-format
// manifest ("<hex>  <filename>", binary mode prefixing the filename with a
// '*').
//
// Matched by exact field, not HasSuffix: the same images directory holds both
// SteamLinuxRuntime_4.tar.xz and SteamLinuxRuntime_4-arm64.tar.xz, and
// SHA256SUMS carries a few hundred unrelated entries besides.
func ParseSHA256Sums(data []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("解析 SHA256SUMS 失败: %w", err)
	}
	return "", fmt.Errorf("SHA256SUMS 里没有 %s 的条目", name)
}
