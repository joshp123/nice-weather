package update

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type updateLock struct{ file *os.File }

func acquireUpdateLock(cache string) (*updateLock, error) {
	lock, err := acquireDirectoryLock(cache)
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(filepath.Join(cache, "staging")); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return lock, nil
}

func acquireDirectoryLock(directory string) (*updateLock, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, "update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("another weather update is already running")
	}
	return &updateLock{file: file}, nil
}

func (lock *updateLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
