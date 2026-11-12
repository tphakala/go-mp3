//go:build unix

package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// A termination signal cancels the run's context instead of ending the
// process, so the run can return and its deferred cleanup executes. Without
// the handler the signal ends the test binary, which also fails the test.
func TestSignalContextCancelsOnSIGTERM(t *testing.T) {
	ctx, stop := signalContext()
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM did not cancel the context")
	}
}
