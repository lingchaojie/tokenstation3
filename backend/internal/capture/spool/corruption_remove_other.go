//go:build !linux && !darwin

package spool

import "os"

func removeDirectoryEntryNoFollowAllocated(_ *os.File, _ string) (int64, error) {
	return 0, errAtomicCorruptionRenameUnsupported
}
