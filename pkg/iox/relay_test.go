package iox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// fakeReader lets a test feed bytes in over time and signal EOF on demand,
// without needing a real file.
type fakeReader struct {
	ch  chan []byte
	eof bool
}

func (r *fakeReader) Read(p []byte) (int, error) {
	b, ok := <-r.ch
	if !ok {
		return 0, io.EOF
	}
	n := copy(p, b)
	return n, nil
}

func TestRelay_CopiesUntilEOFAfterDone(t *testing.T) {
	src := &fakeReader{ch: make(chan []byte, 4)}
	var dst bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		Relay(ctx, src, &dst, 5*time.Millisecond, nil)
		close(done)
	}()

	src.ch <- []byte("hello ")
	time.Sleep(20 * time.Millisecond)
	src.ch <- []byte("world")
	time.Sleep(20 * time.Millisecond)

	cancel()
	close(src.ch) // next Read returns EOF, relay should exit

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Relay did not return after ctx was cancelled and src hit EOF")
	}

	if got := dst.String(); got != "hello world" {
		t.Errorf("dst = %q, want %q", got, "hello world")
	}
}

func TestRelay_ReadErrorInvokesNote(t *testing.T) {
	var dst bytes.Buffer
	errReader := &erroringReader{err: errors.New("boom")}
	ctx := t.Context()

	var noted string
	Relay(ctx, errReader, &dst, time.Millisecond, func(msg string) { noted = msg })

	if noted == "" {
		t.Error("note callback was not invoked for a non-EOF read error")
	}
}

type erroringReader struct{ err error }

func (r *erroringReader) Read(p []byte) (int, error) { return 0, r.err }

func TestRelay_WriteErrorInvokesNote(t *testing.T) {
	src := &fakeReader{ch: make(chan []byte, 1)}
	src.ch <- []byte("x")
	ctx := t.Context()

	var noted string
	Relay(ctx, src, &erroringWriter{}, time.Millisecond, func(msg string) { noted = msg })

	if noted == "" {
		t.Error("note callback was not invoked for a write error")
	}
}

type erroringWriter struct{}

func (w *erroringWriter) Write(p []byte) (int, error) { return 0, errors.New("disk full") }
