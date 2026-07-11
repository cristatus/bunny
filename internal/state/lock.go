package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Lock is an advisory, process-wide mutation lock. Bunny is Linux-only, so
// flock gives us crash-safe release semantics without lock-file cleanup races.
type Lock struct {
	file *os.File
}

// AcquireLock waits until path can be exclusively locked or ctx is cancelled.
func AcquireLock(ctx context.Context, path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open mutation lock: %w", err)
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			file.Close()
			return nil, fmt.Errorf("acquire mutation lock: %w", err)
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, fmt.Errorf("wait for mutation lock: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Close releases the mutation lock.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	return errors.Join(unlockErr, closeErr)
}
