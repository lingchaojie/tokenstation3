//go:build darwin

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
	if err := unix.RenameatxNp(int(source.Fd()), sourceName, int(destination.Fd()), destinationName, unix.RENAME_EXCL); err != nil {
		return fmt.Errorf("renameatx_np ready entry: %w", err)
	}
	return nil
}
