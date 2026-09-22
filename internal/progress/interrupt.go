package progress

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Interruptible returns a context the first SIGINT or SIGTERM cancels, so an
// install in flight stops and rolls back instead of dying between moving its
// tree and recording it. A second signal exits at once, as an impatient
// Ctrl+C expects, restoring the cursor the live reporter hides.
func Interruptible(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-sig:
			cancel()
		case <-done:
			return
		}
		select {
		case <-sig:
			fmt.Fprint(os.Stderr, showCursor)
			os.Exit(130)
		case <-done:
		}
	}()
	return ctx, func() {
		signal.Stop(sig)
		close(done)
		cancel()
	}
}
