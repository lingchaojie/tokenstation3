//go:build linux || darwin

package spool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCorruptionRenameNeverReplacesExistingDestination(t *testing.T) {
	root := t.TempDir()
	sourceDirectory := filepath.Join(root, "ready")
	destinationDirectory := filepath.Join(root, "quarantine")
	require.NoError(t, os.Mkdir(sourceDirectory, 0o700))
	require.NoError(t, os.Mkdir(destinationDirectory, 0o700))

	const sourceName = "malformed-ready-name"
	corruptionID := opaqueReadyNameID(sourceName)
	sourcePath := filepath.Join(sourceDirectory, sourceName)
	destinationPath := filepath.Join(destinationDirectory, string(corruptionID))
	require.NoError(t, os.Mkdir(sourcePath, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "source"), []byte("source"), 0o600))
	require.NoError(t, os.Mkdir(destinationPath, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(destinationPath, "winner"), []byte("winner"), 0o600))

	source, err := openBatchDirectory(sourceDirectory)
	require.NoError(t, err)
	defer source.Close()
	destination, err := openBatchDirectory(destinationDirectory)
	require.NoError(t, err)
	defer destination.Close()
	err = renameDirectoryEntryNoReplace(source, sourceName, destination, string(corruptionID))
	require.Error(t, err)
	require.FileExists(t, filepath.Join(sourcePath, "source"))
	require.FileExists(t, filepath.Join(destinationPath, "winner"))
}
