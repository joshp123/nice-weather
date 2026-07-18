package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateLockIsExclusiveAndCleansAbandonedStaging(t *testing.T) {
	cache := t.TempDir()
	stale := filepath.Join(cache, "staging", "abandoned", "partial")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := acquireUpdateLock(cache)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := os.Stat(filepath.Join(cache, "staging")); !os.IsNotExist(err) {
		t.Fatalf("stale staging remains: %v", err)
	}
	if second, err := acquireUpdateLock(cache); err == nil {
		second.Close()
		t.Fatal("second updater acquired lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireUpdateLock(cache)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryLockSerializesSharedOutput(t *testing.T) {
	output := t.TempDir()
	first, err := acquireDirectoryLock(output)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := acquireDirectoryLock(output); err == nil {
		second.Close()
		t.Fatal("second updater acquired shared output lock")
	}
}
