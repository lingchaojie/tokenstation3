//go:build linux

package spool

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func renameDirectoryEntryNoReplace(sourceDirectory *os.File, sourceName string, destinationDirectory *os.File, destinationName string) error {
	if !validReadyEntryName(sourceName) || !validCorruptionID(CorruptionID(destinationName)) {
		return ErrSpoolCorrupt
	}
	if err := unix.Renameat2(int(sourceDirectory.Fd()), sourceName, int(destinationDirectory.Fd()), destinationName, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("renameat2 ready entry: %w", err)
	}
	return nil
}
