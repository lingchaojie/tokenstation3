//go:build !aix && !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd && !solaris

package spool

import (
	"os"
	"path/filepath"
)

// Repository production images are Linux. This fallback keeps other targets
// buildable and rejects links by comparing the opened descriptor with a final
// lstat before any bytes are consumed.
func openBatchDirectory(path string) (*os.File, error) {
	return openBatchFallback(path, true)
}

func openBatchDirectoryAt(directory *os.File, name string) (*os.File, error) {
	return openBatchFallback(filepath.Join(directory.Name(), name), true)
}

func openBatchRegularAt(directory *os.File, name string) (*os.File, error) {
	return openBatchFallback(filepath.Join(directory.Name(), name), false)
}

func openBatchFallback(path string, wantDirectory bool) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, statErr := f.Stat()
	named, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil || named.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, named) || opened.IsDir() != wantDirectory {
		_ = f.Close()
		if statErr != nil {
			return nil, statErr
		}
		if lstatErr != nil {
			return nil, lstatErr
		}
		return nil, ErrSpoolCorrupt
	}
	return f, nil
}
