//go:build linux || darwin

package spool

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

const (
	maxQuarantineDeleteDepth   = 64
	maxQuarantineDeleteEntries = 4096
)

func removeDirectoryEntryNoFollow(directory *os.File, name string) error {
	remaining := maxQuarantineDeleteEntries
	return removeDirectoryEntryNoFollowBounded(directory, name, 0, &remaining)
}

func removeDirectoryEntryNoFollowBounded(directory *os.File, name string, depth int, remaining *int) error {
	if !validBatchFileName(name) || depth > maxQuarantineDeleteDepth || *remaining <= 0 {
		return ErrSpoolCorrupt
	}
	(*remaining)--
	if err := unix.Unlinkat(int(directory.Fd()), name, 0); err == nil {
		return nil
	} else if errors.Is(err, unix.ENOENT) {
		return os.ErrNotExist
	} else if !errors.Is(err, unix.EISDIR) && !errors.Is(err, unix.EPERM) {
		return &os.PathError{Op: "unlinkat quarantined entry", Path: name, Err: err}
	}

	child, err := openBatchDirectoryAt(directory, name)
	if err != nil {
		return err
	}
	names, readErr := readDirectoryNamesBounded(child, *remaining)
	if readErr == nil {
		for _, childName := range names {
			if err := removeDirectoryEntryNoFollowBounded(child, childName, depth+1, remaining); err != nil {
				readErr = err
				break
			}
		}
	}
	closeErr := child.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := unix.Unlinkat(int(directory.Fd()), name, unix.AT_REMOVEDIR); err != nil {
		return fmt.Errorf("unlinkat quarantined directory: %w", err)
	}
	return nil
}

func readDirectoryNamesBounded(directory *os.File, limit int) ([]string, error) {
	if limit < 0 {
		return nil, ErrSpoolCorrupt
	}
	entries, err := directory.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > limit {
		return nil, ErrSpoolCorrupt
	}
	names := make([]string, len(entries))
	for index := range entries {
		names[index] = entries[index].Name()
	}
	return names, nil
}
