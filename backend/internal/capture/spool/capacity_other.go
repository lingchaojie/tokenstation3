//go:build !linux && !darwin && !windows

package spool

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func filesystemFreeBytes(string) (int64, error) {
	return 0, errors.New("filesystem capacity is unsupported on this platform")
}

func scanUsage(root string) (usage, error) {
	allocated, err := scanCapacityTreeFallback(root)
	if err != nil {
		return usage{}, err
	}
	operationalAllocated, err := scanCapacityTreeFallback(filepath.Join(root, "sending"))
	if errors.Is(err, os.ErrNotExist) {
		operationalAllocated = 0
	} else if err != nil {
		return usage{}, err
	}
	free, err := filesystemFreeBytes(root)
	if err != nil {
		return usage{}, err
	}
	return usage{Allocated: allocated, OperationalAllocated: operationalAllocated, Free: free, BlockSize: filesystemBlockSize}, nil
}

func scanCapacityTreeFallback(root string) (int64, error) {
	remaining := maxCapacityScanEntries
	var allocated int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
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
