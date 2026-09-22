package progress

import (
	"context"
	"syscall"
	"testing"
	"time"
)

// Ctrl+C used to os.Exit(130) from the live reporter, skipping the install's
// rollback: a package moved into place but not yet recorded stayed that way.
// The first signal now only cancels, and the command unwinds.
func TestInterruptibleCancelsOnTheFirstSignal(t *testing.T) {
	ctx, stop := Interruptible(context.Background())
	defer stop()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the first SIGINT must cancel the context")
	}
}
