//go:build linux || darwin

package spool

import (
	"errors"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func filesystemFreeBytes(root string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

func scanUsage(root string) (usage, error) {
	rootDirectory, err := openBatchDirectory(root)
	if err != nil {
		return usage{}, err
	}
	remaining := maxCapacityScanEntries
	allocated, operationalAllocated, scanErr := scanCapacityRoot(rootDirectory, &remaining)
	closeErr := rootDirectory.Close()
	if scanErr != nil {
		return usage{}, scanErr
	}
	if closeErr != nil {
		return usage{}, closeErr
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root, &stat); err != nil {
		return usage{}, err
	}
	return usage{
		Allocated:            allocated,
		OperationalAllocated: operationalAllocated,
		Free:                 int64(stat.Bavail) * int64(stat.Bsize),
		BlockSize:            int64(stat.Bsize),
	}, nil
}

func scanCapacityRoot(directory *os.File, remaining *int) (int64, int64, error) {
	var allocated int64
	var operationalAllocated int64
	for {
		entries, err := directory.ReadDir(capacityScanChunkSize)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, 0, err
		}
		for _, entry := range entries {
			entryAllocated, scanErr := scanCapacityEntry(directory, entry.Name(), 0, remaining)
			if scanErr != nil {
				return 0, 0, scanErr
			}
			if entryAllocated > int64(^uint64(0)>>1)-allocated {
				return 0, 0, ErrSpoolCorrupt
			}
			allocated += entryAllocated
			if entry.Name() == "sending" {
				operationalAllocated = entryAllocated
			}
		}
		if errors.Is(err, io.EOF) {
			return allocated, operationalAllocated, nil
		}
	}
}

func scanCapacityEntry(directory *os.File, name string, depth int, remaining *int) (int64, error) {
	if depth > maxCapacityScanDepth || *remaining <= 0 {
		return 0, ErrSpoolCorrupt
	}
	(*remaining)--
	var stat unix.Stat_t
	if err := unix.Fstatat(int(directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return 0, &os.PathError{Op: "fstatat spool capacity entry", Path: name, Err: err}
	}
	allocated := stat.Blocks * 512
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return allocated, nil
	}
	child, err := openBatchDirectoryAt(directory, name)
	if err != nil {
		return 0, err
	}
	var openedStat unix.Stat_t
	scanErr := unix.Fstat(int(child.Fd()), &openedStat)
	if scanErr == nil && !sameCapacityEntry(stat, openedStat) {
		scanErr = ErrSpoolCorrupt
	}
	if scanErr == nil {
		var childrenAllocated int64
		childrenAllocated, scanErr = scanCapacityDirectory(child, depth+1, remaining)
		if scanErr == nil {
			if childrenAllocated > int64(^uint64(0)>>1)-allocated {
				scanErr = ErrSpoolCorrupt
			} else {
				allocated += childrenAllocated
			}
		}
	}
	closeErr := child.Close()
	if scanErr != nil {
		return 0, scanErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	return allocated, nil
}

func scanCapacityDirectory(directory *os.File, depth int, remaining *int) (int64, error) {
	var allocated int64
	for {
		entries, err := directory.ReadDir(capacityScanChunkSize)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		for _, entry := range entries {
			entryAllocated, scanErr := scanCapacityEntry(directory, entry.Name(), depth, remaining)
			if scanErr != nil {
				return 0, scanErr
			}
			if entryAllocated > int64(^uint64(0)>>1)-allocated {
				return 0, ErrSpoolCorrupt
			}
			allocated += entryAllocated
		}
		if errors.Is(err, io.EOF) {
			return allocated, nil
		}
	}
}

func sameCapacityEntry(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Mode == right.Mode
}

func allocatedFileInfo(info os.FileInfo) int64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Blocks * 512
	}
	if info.Mode().IsRegular() {
		return roundUp(info.Size(), filesystemBlockSize)
	}
	return 0
}
