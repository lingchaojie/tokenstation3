//go:build windows

package spool

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func filesystemFreeBytes(root string) (int64, error) {
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(path, &available, &total, &free); err != nil {
		return 0, err
	}
	if available > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1), nil
	}
	return int64(available), nil
}

func scanUsage(root string) (usage, error) {
	allocated, err := scanCapacityTree(root)
	if err != nil {
		return usage{}, err
	}
	operationalAllocated, err := scanCapacityTree(filepath.Join(root, "sending"))
	if errors.Is(err, os.ErrNotExist) {
		operationalAllocated = 0
	} else if err != nil {
		return usage{}, err
	}
	free, err := filesystemFreeBytes(root)
	if err != nil {
		return usage{}, err
	}
	return usage{
		Allocated:            allocated,
		OperationalAllocated: operationalAllocated,
		Free:                 free,
		BlockSize:            filesystemBlockSize,
	}, nil
}

func scanCapacityTree(root string) (int64, error) {
	remaining := maxCapacityScanEntries
	var allocated int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if remaining <= 0 {
			return ErrSpoolCorrupt
		}
		remaining--
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrSpoolCorrupt
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		entryAllocated := allocatedFileInfo(info)
		if entryAllocated > int64(^uint64(0)>>1)-allocated {
			return ErrSpoolCorrupt
		}
		allocated += entryAllocated
		return nil
	})
	return allocated, err
}

func allocatedFileInfo(info os.FileInfo) int64 {
	if info.Mode().IsRegular() {
		return roundUp(info.Size(), filesystemBlockSize)
	}
	return 0
}
