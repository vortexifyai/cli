package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"vortexify-cli/internal/storage"
)

type BusyError struct{ Root string }

func (e *BusyError) Error() string {
	return fmt.Sprintf("workspace %s is busy; another commit, pull, or recovery is running", e.Root)
}

type Lock struct{ file *os.File }

func Acquire(root string) (*Lock, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, ".vortexify", "workspace.lock")
	if err = storage.CheckFilePath(path); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockFile(file); err != nil {
		file.Close()
		if isBusy(err) {
			return nil, &BusyError{Root: root}
		}
		return nil, err
	}
	// Keep this inode permanently: unlinking lock files can allow two independent locks.
	return &Lock{file: file}, nil
}
func (l *Lock) Close() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
