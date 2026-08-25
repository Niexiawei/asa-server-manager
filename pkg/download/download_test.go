package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// noRetryConfig avoids the retry backoff sleep in tests that expect a
// single, deterministic attempt.
func noRetryConfig(t *testing.T) {
	t.Helper()
	Configure(Config{Retries: 1})
	t.Cleanup(func() { Configure(Config{}) })
}

func TestFetchDownloadsFile(t *testing.T) {
	noRetryConfig(t)

	const body = "hello from the test server"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	var lastDone, lastTotal int64
	err := Fetch(context.Background(), Options{
		URL:  srv.URL,
		Dest: dest,
		Progress: func(done, total int64) {
			lastDone, lastTotal = done, total
		},
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(got) != body {
		t.Errorf("downloaded content = %q, want %q", got, body)
	}
	if lastDone != int64(len(body)) || lastTotal != int64(len(body)) {
		t.Errorf("final progress = (%d, %d), want (%d, %d)", lastDone, lastTotal, len(body), len(body))
	}

	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Errorf(".part file left behind after successful download")
	}
}

func TestFetchChecksumMismatchDeletesPartFile(t *testing.T) {
	noRetryConfig(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("wrong content"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := Fetch(context.Background(), Options{
		URL:      srv.URL,
		Dest:     dest,
		Checksum: "sha256:" + hex.EncodeToString(sha256.New().Sum(nil)), // checksum of empty content, won't match
	})
	if err == nil {
		t.Fatal("Fetch() error = nil, want checksum mismatch error")
	}

	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Errorf(".part file left behind after checksum failure")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("final file created despite checksum failure")
	}
}

func TestFetchChecksumMatchSucceeds(t *testing.T) {
	noRetryConfig(t)

	const body = "verified content"
	sum := sha256.Sum256([]byte(body))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := Fetch(context.Background(), Options{
		URL:      srv.URL,
		Dest:     dest,
		Checksum: "sha256:" + hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
}

func TestFetchBadStatus(t *testing.T) {
	noRetryConfig(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := Fetch(context.Background(), Options{URL: srv.URL, Dest: dest})
	if err == nil {
		t.Fatal("Fetch() error = nil, want error for 404 status")
	}
}

func TestFetchResume(t *testing.T) {
	noRetryConfig(t)

	const full = "0123456789ABCDEF"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.Write([]byte(full))
			return
		}
		spec := strings.TrimSuffix(strings.TrimPrefix(rangeHeader, "bytes="), "-")
		offset, err := strconv.Atoi(spec)
		if err != nil {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", "bytes */*")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(full[offset:]))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	// Pre-seed a partial .part file to simulate a prior interrupted download.
	if err := os.WriteFile(dest+".part", []byte(full[:8]), 0o644); err != nil {
		t.Fatalf("seeding .part file: %v", err)
	}

	err := Fetch(context.Background(), Options{URL: srv.URL, Dest: dest, Resume: true})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(got) != full {
		t.Errorf("resumed content = %q, want %q", got, full)
	}
}
