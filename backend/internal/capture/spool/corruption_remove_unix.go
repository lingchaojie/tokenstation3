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

type quarantineDeleteEntry struct {
	name      string
	stat      unix.Stat_t
	directory bool
	children  []*quarantineDeleteEntry
}

func removeDirectoryEntryNoFollow(directory *os.File, name string) error {
	remaining := maxQuarantineDeleteEntries
	return removeDirectoryEntryNoFollowBounded(directory, name, 0, &remaining)
}

func removeDirectoryEntryNoFollowBounded(directory *os.File, name string, depth int, remaining *int) error {
	entry, err := preflightDirectoryEntryNoFollowBounded(directory, name, depth, remaining)
	if err != nil {
		return err
	}
	if err := validateDirectoryEntryNoFollow(directory, entry); err != nil {
		return err
	}
	return deleteDirectoryEntryNoFollow(directory, entry)
}

func preflightDirectoryEntryNoFollowBounded(directory *os.File, name string, depth int, remaining *int) (*quarantineDeleteEntry, error) {
	if !validBatchFileName(name) || depth > maxQuarantineDeleteDepth || *remaining <= 0 {
		return nil, ErrSpoolCorrupt
	}
	(*remaining)--
	stat, err := statDirectoryEntryNoFollow(directory, name)
	if err != nil {
		return nil, err
	}
	entry := &quarantineDeleteEntry{
		name:      name,
		stat:      stat,
		directory: stat.Mode&unix.S_IFMT == unix.S_IFDIR,
	}
	if !entry.directory {
		return entry, nil
	}

	child, err := openBatchDirectoryAt(directory, name)
	if err != nil {
		return nil, err
	}
	openedStat, statErr := statOpenedDirectory(child)
	if statErr != nil || !sameDirectoryEntry(stat, openedStat) {
		_ = child.Close()
		if statErr != nil {
			return nil, statErr
		}
		return nil, ErrSpoolCorrupt
	}
	names, readErr := readDirectoryNamesBounded(child, *remaining)
	if readErr == nil {
		entry.children = make([]*quarantineDeleteEntry, 0, len(names))
		for _, childName := range names {
			childEntry, err := preflightDirectoryEntryNoFollowBounded(child, childName, depth+1, remaining)
			if err != nil {
				readErr = err
				break
			}
			entry.children = append(entry.children, childEntry)
		}
	}
	closeErr := child.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return entry, nil
}

func validateDirectoryEntryNoFollow(directory *os.File, entry *quarantineDeleteEntry) error {
	stat, err := statDirectoryEntryNoFollow(directory, entry.name)
	if err != nil {
		return err
	}
	if !sameDirectoryEntry(entry.stat, stat) {
		return ErrSpoolCorrupt
	}
	if !entry.directory {
		return nil
	}
	child, err := openBatchDirectoryAt(directory, entry.name)
	if err != nil {
		return err
	}
	openedStat, validateErr := statOpenedDirectory(child)
	if validateErr == nil && !sameDirectoryEntry(entry.stat, openedStat) {
		validateErr = ErrSpoolCorrupt
	}
	var names []string
	if validateErr == nil {
		names, validateErr = readDirectoryNamesBounded(child, len(entry.children))
	}
	if validateErr == nil && !sameDeleteEntryNames(entry.children, names) {
		validateErr = ErrSpoolCorrupt
	}
	if validateErr == nil {
		childrenByName := make(map[string]*quarantineDeleteEntry, len(entry.children))
		for _, childEntry := range entry.children {
			childrenByName[childEntry.name] = childEntry
		}
		for _, name := range names {
			if err := validateDirectoryEntryNoFollow(child, childrenByName[name]); err != nil {
				validateErr = err
				break
			}
		}
	}
	closeErr := child.Close()
	if validateErr != nil {
		return validateErr
	}
	return closeErr
}

func deleteDirectoryEntryNoFollow(directory *os.File, entry *quarantineDeleteEntry) error {
	stat, err := statDirectoryEntryNoFollow(directory, entry.name)
	if err != nil {
		return err
	}
	if !sameDirectoryEntry(entry.stat, stat) {
		return ErrSpoolCorrupt
	}
	if !entry.directory {
		if err := unix.Unlinkat(int(directory.Fd()), entry.name, 0); err != nil {
			if errors.Is(err, unix.ENOENT) {
				return os.ErrNotExist
			}
			return &os.PathError{Op: "unlinkat quarantined entry", Path: entry.name, Err: err}
		}
		return nil
	}
	child, err := openBatchDirectoryAt(directory, entry.name)
	if err != nil {
		return err
	}
	openedStat, deleteErr := statOpenedDirectory(child)
	if deleteErr == nil && !sameDirectoryEntry(entry.stat, openedStat) {
		deleteErr = ErrSpoolCorrupt
	}
	if deleteErr == nil {
		for _, childEntry := range entry.children {
			if err := deleteDirectoryEntryNoFollow(child, childEntry); err != nil {
				deleteErr = err
				break
			}
		}
	}
	closeErr := child.Close()
	if deleteErr != nil {
		return deleteErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := unix.Unlinkat(int(directory.Fd()), entry.name, unix.AT_REMOVEDIR); err != nil {
		return fmt.Errorf("unlinkat quarantined directory: %w", err)
	}
	return nil
}

func statDirectoryEntryNoFollow(directory *os.File, name string) (unix.Stat_t, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return unix.Stat_t{}, os.ErrNotExist
		}
		return unix.Stat_t{}, &os.PathError{Op: "fstatat quarantined entry", Path: name, Err: err}
	}
	return stat, nil
}

func statOpenedDirectory(directory *os.File) (unix.Stat_t, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(directory.Fd()), &stat); err != nil {
		return unix.Stat_t{}, err
	}
	return stat, nil
}

func sameDirectoryEntry(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Mode == right.Mode
}

func sameDeleteEntryNames(entries []*quarantineDeleteEntry, names []string) bool {
	if len(entries) != len(names) {
		return false
	}
	want := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		want[entry.name] = struct{}{}
	}
	for _, name := range names {
		if _, exists := want[name]; !exists {
			return false
		}
	}
	return true
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
