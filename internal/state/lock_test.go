package state

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestMutationLockSerializesAndHonorsContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mutation.lock")
	first, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := AcquireLock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock error = %v, want deadline exceeded", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}
