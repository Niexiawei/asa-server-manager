package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(full)-1, len(full)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(full[offset:]))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	// Pre-seed a partial .part file to simulate a prior interrupted download.
	if err := os.WriteFile(dest+".part", []byte(full[:8]), 0o644); err != nil {
		t.Fatalf("seeding .part file: %v", err)
	}

	var ranged int
	err := Fetch(context.Background(), Options{URL: srv.URL, Dest: dest, Resume: true,
		Progress: func(done, total int64) {
			if total != int64(len(full)) {
				t.Errorf("progress total = %d, want %d", total, len(full))
			}
			ranged++
		}})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if ranged == 0 {
		t.Error("progress never reported")
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(got) != full {
		t.Errorf("resumed content = %q, want %q", got, full)
	}
}

// A 206 whose range does not start where the .part ends must not be appended:
// the bytes would land at the wrong offset. The downloader drops the .part and
// fetches the whole file instead.
func TestFetchResumeRejectsMisalignedRange(t *testing.T) {
	noRetryConfig(t)

	const full = "0123456789ABCDEF"
	var plain int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			plain++
			w.Write([]byte(full))
			return
		}
		// Claims to resume but starts four bytes early.
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 4-%d/%d", len(full)-1, len(full)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(full[4:]))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := os.WriteFile(dest+".part", []byte(full[:8]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Fetch(context.Background(), Options{URL: srv.URL, Dest: dest, Resume: true}); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != full {
		t.Errorf("content = %q, want %q", got, full)
	}
	if plain != 1 {
		t.Errorf("full-file requests = %d, want 1", plain)
	}
}

func TestFetchResumeRejectsMissingContentRange(t *testing.T) {
	noRetryConfig(t)

	const full = "0123456789ABCDEF"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			w.Write([]byte(full))
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(full[8:]))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := os.WriteFile(dest+".part", []byte("XXXXXXXX"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Fetch(context.Background(), Options{URL: srv.URL, Dest: dest, Resume: true}); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != full {
		t.Errorf("content = %q, want %q", got, full)
	}
}

func TestFetchMaxBytesRefusesDeclaredLength(t *testing.T) {
	Configure(Config{Retries: 3})
	t.Cleanup(func() { Configure(Config{}) })

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := Fetch(context.Background(), Options{URL: srv.URL, Dest: dest, MaxBytes: 5})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Fetch() error = %v, want ErrTooLarge", err)
	}
	if hits != 1 {
		t.Errorf("requests = %d, want 1 (ErrTooLarge must not be retried)", hits)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error(".part left behind")
	}
}

// A body that runs past what the server announced (here: no length at all,
// chunked) is cut off at the cap and its .part deleted.
func TestFetchMaxBytesCutsOffUndeclaredBody(t *testing.T) {
	noRetryConfig(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 64; i++ {
			w.Write([]byte(strings.Repeat("x", 1024)))
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := Fetch(context.Background(), Options{URL: srv.URL, Dest: dest, MaxBytes: 4096})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Fetch() error = %v, want ErrTooLarge", err)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error(".part left behind")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("final file created despite the cap")
	}
}

func TestFetchMaxBytesAllowsExactSize(t *testing.T) {
	noRetryConfig(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("01234"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := Fetch(context.Background(), Options{URL: srv.URL, Dest: dest, MaxBytes: 5}); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
}

func TestParseContentRange(t *testing.T) {
	for _, tc := range []struct {
		in          string
		start, size int64
		ok          bool
	}{
		{"bytes 8-15/16", 8, 16, true},
		{"bytes 0-0/1", 0, 1, true},
		{"bytes 8-15/*", 8, -1, true},
		{"bytes */16", 0, 0, false},
		{"bytes */*", 0, 0, false},
		{"bytes 8-15/15", 0, 0, false},
		{"bytes 9-8/16", 0, 0, false},
		{"items 8-15/16", 0, 0, false},
		{"", 0, 0, false},
	} {
		start, size, ok := parseContentRange(tc.in)
		if ok != tc.ok || (ok && (start != tc.start || size != tc.size)) {
			t.Errorf("parseContentRange(%q) = (%d, %d, %v), want (%d, %d, %v)", tc.in, start, size, ok, tc.start, tc.size, tc.ok)
		}
	}
}
