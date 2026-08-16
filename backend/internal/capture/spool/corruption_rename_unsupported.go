//go:build !linux && !darwin

package spool

import "errors"

var errAtomicCorruptionRenameUnsupported = errors.New("atomic corruption quarantine unsupported")

func renameDirectoryEntryNoReplace(_, _, _, _ string) error {
	// Fail closed on platforms without an atomic no-replace rename primitive.
	// Production sidecars run on Linux; silently falling back to a check-then-
	// rename sequence would reintroduce the destination TOCTOU this transaction
	// exists to eliminate.
	return errAtomicCorruptionRenameUnsupported
}
