//go:build darwin

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
	if err := unix.RenameatxNp(int(sourceDirectory.Fd()), sourceName, int(destinationDirectory.Fd()), destinationName, unix.RENAME_EXCL); err != nil {
		return fmt.Errorf("renameatx_np ready entry: %w", err)
	}
	return nil
}
