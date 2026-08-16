//go:build linux

package spool

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func renameDirectoryEntryNoReplace(sourceDirectory, sourceName, destinationDirectory, destinationName string) error {
	if !validReadyEntryName(sourceName) || !validCorruptionID(CorruptionID(destinationName)) {
		return ErrSpoolCorrupt
	}
	source, err := openBatchDirectory(sourceDirectory)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := openBatchDirectory(destinationDirectory)
	if err != nil {
		return err
	}
	defer destination.Close()
	if err := unix.Renameat2(int(source.Fd()), sourceName, int(destination.Fd()), destinationName, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("renameat2 ready entry: %w", err)
	}
	return nil
}
