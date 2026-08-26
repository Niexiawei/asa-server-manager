package procx

import (
	"os"
	"testing"
)

// TestProcessCmdline_OwnProcess is deliberately loose about the exact
// content (the test binary's own invocation varies by how `go test` was
// run) but asserts the plumbing actually works end-to-end: no error, and a
// non-empty result for a process we know is alive (ourselves).
func TestProcessCmdline_OwnProcess(t *testing.T) {
	cmdline, err := ProcessCmdline(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("ProcessCmdline(%d): %v", os.Getpid(), err)
	}
	if cmdline == "" {
		t.Error("expected a non-empty command line for the current process")
	}
}
