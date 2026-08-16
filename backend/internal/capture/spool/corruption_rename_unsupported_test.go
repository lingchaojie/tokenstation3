//go:build !linux && !darwin

package spool

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCorruptionRenameFailsClosedWhenAtomicNoReplaceIsUnavailable(t *testing.T) {
	err := renameDirectoryEntryNoReplace("ready", "malformed", "quarantine", "opaque")
	require.Error(t, err)
	require.True(t, errors.Is(err, errAtomicCorruptionRenameUnsupported))
}
