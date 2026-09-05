// Package asaversion extracts the ARK: Survival Ascended version string
// embedded in ArkAscendedServer.exe. Pure binary parsing — it does not know
// about instances, mirrors, or config; the caller resolves which exe path to
// ask about.
package asaversion

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// versionTarget is "ArkVersion\0" encoded as UTF-16LE.
var versionTarget = []byte{
	0x41, 0x00, 0x72, 0x00, 0x6B, 0x00, 0x56, 0x00, 0x65, 0x00, 0x72, 0x00, 0x73, 0x00, 0x69,
	0x00, 0x6F, 0x00, 0x6E, 0x00, 0x00, 0x00,
}

// Get extracts the ASA version from exePath by searching for the UTF-16LE
// marker "ArkVersion\0" and reading the UTF-16 string that immediately
// follows it. Opens the file read-only, so it does not disturb a server
// process that currently has it open.
func Get(exePath string) (string, error) {
	file, err := os.Open(exePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	buffer := make([]byte, 1024*1024)
	overlap := len(versionTarget) - 1

	// 分块扫描，块间保留 overlap 字节重叠，避免目标串跨块被漏掉
	var fileOffset int64
	var foundOffset int64 = -1
	validLen, err := io.ReadFull(file, buffer)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}

	for {
		if idx := bytes.Index(buffer[:validLen], versionTarget); idx >= 0 {
			foundOffset = fileOffset + int64(idx)
			break
		}

		if validLen < len(buffer) {
			break // EOF
		}

		copy(buffer, buffer[validLen-overlap:validLen])
		fileOffset += int64(validLen - overlap)

		n, err := io.ReadFull(file, buffer[overlap:])
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return "", err
		}
		validLen = overlap + n
	}

	if foundOffset < 0 {
		return "", fmt.Errorf("failed to find ArkVersion string in the executable")
	}

	// 读取标记后紧跟的 UTF-16LE 字符串，直到 0x0000 结束
	if _, err := file.Seek(foundOffset+int64(len(versionTarget)), io.SeekStart); err != nil {
		return "", err
	}

	reader := bufio.NewReader(file)
	var version strings.Builder
	buf := make([]byte, 2)
	for {
		if _, err := io.ReadFull(reader, buf); err != nil {
			break
		}
		unicodeVal := uint16(buf[0]) | uint16(buf[1])<<8
		if unicodeVal == 0 {
			break
		}
		r := rune(unicodeVal)
		if !utf8.ValidRune(r) {
			return "", fmt.Errorf("failed to convert UTF-16 code unit while reading version: %#06X", unicodeVal)
		}
		version.WriteRune(r)
	}
	return version.String(), nil
}

type cacheEntry struct {
	modTime time.Time
	size    int64
	version string
}

// Resolver caches Get results keyed by exe path + (modTime, size), so a
// server update invalidates the cache automatically without needing a
// separate signal.
type Resolver struct {
	cache sync.Map // exe path -> cacheEntry
}

// New returns a Resolver with an empty cache.
func New() *Resolver {
	return &Resolver{}
}

// Get returns the ASA version for exePath, using the cached value when the
// file's mtime and size are unchanged since the last call.
func (r *Resolver) Get(exePath string) (string, error) {
	stat, err := os.Stat(exePath)
	if err != nil {
		return "", err
	}

	if v, ok := r.cache.Load(exePath); ok {
		entry := v.(cacheEntry)
		if entry.modTime.Equal(stat.ModTime()) && entry.size == stat.Size() {
			return entry.version, nil
		}
	}

	version, err := Get(exePath)
	if err != nil {
		return "", err
	}
	r.cache.Store(exePath, cacheEntry{modTime: stat.ModTime(), size: stat.Size(), version: version})
	return version, nil
}
