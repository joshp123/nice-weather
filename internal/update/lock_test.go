package update

import (
	"testing"
)

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
