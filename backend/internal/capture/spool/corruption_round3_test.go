//go:build linux || darwin

package spool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRecoverAckedPreUpgradeAliasDoesNotReportDeliveredRecordLost(t *testing.T) {
	id := uuid.MustParse("abcdef01-2345-6789-abcd-ef0123456789")
	for _, test := range []struct {
		name  string
		alias string
	}{
		{name: "raw hex", alias: strings.ReplaceAll(id.String(), "-", "")},
		{name: "URN", alias: "urn:uuid:" + id.String()},
		{name: "Microsoft", alias: "{" + id.String() + "}"},
		{name: "uppercase", alias: strings.ToUpper(id.String())},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "spool")
			seed := openTestStoreAt(t, root, nil)
			commitSizedRecord(t, seed, id, 8, time.Unix(1_700_000_000, 0).UTC())
			recoverStore(t, seed)
			batch, err := seed.NextBatch(1, 64<<20)
			require.NoError(t, err)
			require.NoError(t, seed.MarkAcked(batch))

			aliasPath := filepath.Join(seed.readyDir, test.alias)
			require.NoError(t, os.Rename(filepath.Join(seed.readyDir, id.String()), aliasPath))
			require.NoError(t, syncDirectory(seed.readyDir))

			reopened := openTestStoreAt(t, root, nil)
			report, err := reopened.Recover(context.Background())
			require.NoError(t, err)
			require.Empty(t, report.AppliedCorruptions)
			require.Zero(t, reopened.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])
			require.NoFileExists(t, aliasPath)
			require.Empty(t, readDirectoryNames(t, reopened.sendingDir))
		})
	}
}

func TestRecoverPreUpgradePendingAliasRetiresDerivativeBatchExactlyOnce(t *testing.T) {
	id := uuid.MustParse("abcdef01-2345-6789-abcd-ef0123456789")
	for _, test := range []struct {
		name  string
		alias string
	}{
		{name: "raw hex", alias: strings.ReplaceAll(id.String(), "-", "")},
		{name: "URN", alias: "urn:uuid:" + id.String()},
		{name: "Microsoft", alias: "{" + id.String() + "}"},
		{name: "uppercase", alias: strings.ToUpper(id.String())},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "spool")
			seed := openTestStoreAt(t, root, nil)
			commitSizedRecord(t, seed, id, 8, time.Unix(1_700_000_000, 0).UTC())
			recoverStore(t, seed)
			batch, err := seed.NextBatch(1, 64<<20)
			require.NoError(t, err)
			aliasPath := filepath.Join(seed.readyDir, test.alias)
			require.NoError(t, os.Rename(filepath.Join(seed.readyDir, id.String()), aliasPath))
			require.NoError(t, syncDirectory(seed.readyDir))

			reopened := openTestStoreAt(t, root, nil)
			report, err := reopened.Recover(context.Background())
			require.NoError(t, err)
			require.Len(t, report.AppliedCorruptions, 1)
			require.NoFileExists(t, batchManifestPath(reopened, batch.ID))
			require.EqualValues(t, 1, reopened.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])
			next, err := reopened.NextBatch(1, 64<<20)
			require.NoError(t, err)
			require.Nil(t, next)
		})
	}
}

func TestRecoverRetiresAliasDerivedBatchWhenCanonicalTwinHasDifferentContent(t *testing.T) {
	id := uuid.MustParse("abcdef01-2345-6789-abcd-ef0123456789")
	for _, test := range []struct {
		name  string
		alias string
	}{
		{name: "raw hex", alias: strings.ReplaceAll(id.String(), "-", "")},
		{name: "URN", alias: "urn:uuid:" + id.String()},
		{name: "Microsoft", alias: "{" + id.String() + "}"},
		{name: "uppercase", alias: strings.ToUpper(id.String())},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "spool")
			aliasSeed := openTestStoreAt(t, root, nil)
			commitSizedRecord(t, aliasSeed, id, 8, time.Unix(1_700_000_000, 0).UTC())
			recoverStore(t, aliasSeed)
			aliasBatch, err := aliasSeed.NextBatch(1, 64<<20)
			require.NoError(t, err)
			require.NoError(t, os.Rename(filepath.Join(aliasSeed.readyDir, id.String()), filepath.Join(aliasSeed.readyDir, test.alias)))
			require.NoError(t, syncDirectory(aliasSeed.readyDir))

			canonicalSeed := openTestStoreAt(t, root, nil)
			commitSizedRecord(t, canonicalSeed, id, 16, time.Unix(1_700_000_001, 0).UTC())

			reopened := openTestStoreAt(t, root, nil)
			report, err := reopened.Recover(context.Background())
			require.NoError(t, err)
			require.Len(t, report.AppliedCorruptions, 1)
			require.NoFileExists(t, batchManifestPath(reopened, aliasBatch.ID),
				"a batch whose manifest digest does not match the exact canonical twin is alias-derived")
			require.EqualValues(t, 1, reopened.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])

			canonicalBatch, err := reopened.NextBatch(1, 64<<20)
			require.NoError(t, err)
			require.NotNil(t, canonicalBatch)
			require.NotEqual(t, aliasBatch.ID, canonicalBatch.ID)
			require.Equal(t, []uuid.UUID{id}, batchCaptureIDs(canonicalBatch))
			require.EqualValues(t, 1, reopened.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])
		})
	}
}

func TestReadyEntryInspectionUsesExactEnumeratedCanonicalBasename(t *testing.T) {
	id := uuid.MustParse("abcdef01-2345-6789-abcd-ef0123456789")
	aliasName := strings.ToUpper(id.String())
	tombstone, err := newCorruptionTombstone(aliasName)
	require.NoError(t, err)

	aliasOnly, err := inspectReadyEntryNames(tombstone, []string{aliasName})
	require.NoError(t, err)
	require.Equal(t, aliasName, aliasOnly.sourceName)
	require.False(t, aliasOnly.canonicalTwin,
		"a case-insensitive filesystem lookup must not turn the enumerated alias into a canonical twin")

	withCanonical, err := inspectReadyEntryNames(tombstone, []string{aliasName, id.String()})
	require.NoError(t, err)
	require.Equal(t, aliasName, withCanonical.sourceName)
	require.True(t, withCanonical.canonicalTwin)
}

func TestRecoverAckedPreUpgradeAliasTwinDoesNotReportDuplicateAsLost(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	id := uuid.MustParse("abcdef01-2345-6789-abcd-ef0123456789")
	seed := openTestStoreAt(t, root, nil)
	commitSizedRecord(t, seed, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, seed)
	batch, err := seed.NextBatch(1, 64<<20)
	require.NoError(t, err)
	require.NoError(t, seed.MarkAcked(batch))

	aliasPath := filepath.Join(seed.readyDir, strings.ToUpper(id.String()))
	copyRecordDirectory(t, filepath.Join(seed.readyDir, id.String()), aliasPath)
	require.NoError(t, syncDirectory(seed.readyDir))

	reopened := openTestStoreAt(t, root, nil)
	report, err := reopened.Recover(context.Background())
	require.NoError(t, err)
	require.Empty(t, report.AppliedCorruptions)
	require.Zero(t, reopened.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])
	require.NoFileExists(t, aliasPath)
	require.NoFileExists(t, filepath.Join(reopened.readyDir, id.String()))
}

func TestRecoverAckedPreUpgradeAliasReplaysAfterReadyFsyncCrashWithoutLoss(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	id := uuid.MustParse("abcdef01-2345-6789-abcd-ef0123456789")
	seed := openTestStoreAt(t, root, nil)
	commitSizedRecord(t, seed, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, seed)
	batch, err := seed.NextBatch(1, 64<<20)
	require.NoError(t, err)
	require.NoError(t, seed.MarkAcked(batch))
	aliasPath := filepath.Join(seed.readyDir, "urn:uuid:"+id.String())
	require.NoError(t, os.Rename(filepath.Join(seed.readyDir, id.String()), aliasPath))
	require.NoError(t, syncDirectory(seed.readyDir))

	crashing := openTestStoreAt(t, root, nil)
	crash := os.ErrInvalid
	crashing.readySyncDirectory = func(directory *os.File) error {
		require.NoError(t, directory.Sync())
		return crash
	}
	_, err = crashing.Recover(context.Background())
	require.ErrorIs(t, err, crash)
	require.NoFileExists(t, aliasPath)
	require.FileExists(t, batchManifestPath(crashing, batch.ID))
	require.FileExists(t, batchAckPath(crashing, batch.ID))

	reopened := openTestStoreAt(t, root, nil)
	report, err := reopened.Recover(context.Background())
	require.NoError(t, err)
	require.Empty(t, report.AppliedCorruptions)
	require.Zero(t, reopened.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])
	require.Empty(t, readDirectoryNames(t, reopened.readyDir))
	require.Empty(t, readDirectoryNames(t, reopened.sendingDir))
}

func TestQuarantinePinsDirectoryIdentityAcrossParentReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	seed := openTestStoreAt(t, root, nil)
	attempt := committedRequestAttempt(t, seed)
	aliasName := strings.ToUpper(attempt.ID().String())
	aliasPath := filepath.Join(seed.readyDir, aliasName)
	require.NoError(t, os.Rename(filepath.Join(seed.readyDir, attempt.ID().String()), aliasPath))
	require.NoError(t, os.WriteFile(filepath.Join(aliasPath, manifestName), []byte("corrupt"), 0o600))

	corruptionID := opaqueReadyNameID(aliasName)
	pinnedReady := filepath.Join(root, "ready-pinned")
	pinnedQuarantine := filepath.Join(root, "quarantine-pinned")
	externalReady := filepath.Join(root, "external-ready")
	require.NoError(t, os.Mkdir(externalReady, 0o700))
	externalReadySentinel := filepath.Join(externalReady, "must-survive")
	require.NoError(t, os.WriteFile(externalReadySentinel, []byte("external"), 0o600))

	reopened := openTestStoreAt(t, root, nil)
	replacementQuarantineEntry := filepath.Join(reopened.quarantineDir, string(corruptionID))
	reopened.config.eventHook = func(event string) {
		if event != "rename:ready-to-quarantine" {
			return
		}
		require.NoError(t, os.Rename(reopened.readyDir, pinnedReady))
		require.NoError(t, os.Symlink(externalReady, reopened.readyDir))
		require.NoError(t, os.Rename(reopened.quarantineDir, pinnedQuarantine))
		require.NoError(t, os.Mkdir(reopened.quarantineDir, 0o700))
		require.NoError(t, os.Mkdir(replacementQuarantineEntry, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(replacementQuarantineEntry, "must-survive"), []byte("replacement"), 0o600))
	}

	_, err := reopened.Recover(context.Background())
	require.NoError(t, err)
	require.FileExists(t, externalReadySentinel)
	require.FileExists(t, filepath.Join(replacementQuarantineEntry, "must-survive"))
	require.NoFileExists(t, filepath.Join(pinnedQuarantine, string(corruptionID)))
	readyInfo, err := os.Lstat(reopened.readyDir)
	require.NoError(t, err)
	require.NotZero(t, readyInfo.Mode()&os.ModeSymlink)
}

func TestQuarantineRecursiveDeleteDoesNotFollowNestedSymlink(t *testing.T) {
	root := t.TempDir()
	quarantinePath := filepath.Join(root, "quarantine")
	require.NoError(t, os.Mkdir(quarantinePath, 0o700))
	external := filepath.Join(root, "external")
	require.NoError(t, os.Mkdir(external, 0o700))
	externalSentinel := filepath.Join(external, "must-survive")
	require.NoError(t, os.WriteFile(externalSentinel, []byte("external"), 0o600))

	const entryName = "opaque-entry"
	entryPath := filepath.Join(quarantinePath, entryName)
	require.NoError(t, os.MkdirAll(filepath.Join(entryPath, "nested"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(entryPath, "nested", "payload"), []byte("payload"), 0o600))
	require.NoError(t, os.Symlink(external, filepath.Join(entryPath, "external-link")))
	directory, err := openBatchDirectory(quarantinePath)
	require.NoError(t, err)
	defer directory.Close()

	require.NoError(t, removeDirectoryEntryNoFollow(directory, entryName))
	require.NoFileExists(t, entryPath)
	require.FileExists(t, externalSentinel)
}

func TestQuarantineRecursiveDeleteFailsClosedAtEntryBound(t *testing.T) {
	root := t.TempDir()
	quarantinePath := filepath.Join(root, "quarantine")
	require.NoError(t, os.Mkdir(quarantinePath, 0o700))
	const entryName = "opaque-entry"
	entryPath := filepath.Join(quarantinePath, entryName)
	require.NoError(t, os.Mkdir(entryPath, 0o700))
	first := filepath.Join(entryPath, "first")
	second := filepath.Join(entryPath, "second")
	require.NoError(t, os.WriteFile(first, []byte("first"), 0o600))
	require.NoError(t, os.WriteFile(second, []byte("second"), 0o600))
	directory, err := openBatchDirectory(quarantinePath)
	require.NoError(t, err)
	defer directory.Close()
	remaining := 2

	err = removeDirectoryEntryNoFollowBounded(directory, entryName, 0, &remaining)
	require.ErrorIs(t, err, ErrSpoolCorrupt)
	require.DirExists(t, entryPath)
	require.FileExists(t, first)
	require.FileExists(t, second)
}

func TestQuarantineRecursiveDeletePreflightsWholeSubtreeBeforeUnlink(t *testing.T) {
	root := t.TempDir()
	quarantinePath := filepath.Join(root, "quarantine")
	require.NoError(t, os.Mkdir(quarantinePath, 0o700))
	const entryName = "opaque-entry"
	entryPath := filepath.Join(quarantinePath, entryName)
	require.NoError(t, os.Mkdir(entryPath, 0o700))
	earlyFile := filepath.Join(entryPath, "a-early")
	require.NoError(t, os.WriteFile(earlyFile, []byte("early"), 0o600))
	lateDirectory := filepath.Join(entryPath, "z-late")
	deepDirectory := filepath.Join(lateDirectory, "deep")
	require.NoError(t, os.MkdirAll(deepDirectory, 0o700))
	deepFile := filepath.Join(deepDirectory, "payload")
	require.NoError(t, os.WriteFile(deepFile, []byte("payload"), 0o600))
	directory, err := openBatchDirectory(quarantinePath)
	require.NoError(t, err)
	defer directory.Close()
	remaining := 4

	err = removeDirectoryEntryNoFollowBounded(directory, entryName, 0, &remaining)

	require.ErrorIs(t, err, ErrSpoolCorrupt)
	require.DirExists(t, entryPath)
	require.FileExists(t, earlyFile)
	require.DirExists(t, lateDirectory)
	require.DirExists(t, deepDirectory)
	require.FileExists(t, deepFile)
}

func TestRecoverRejectsTombstoneAliasIDNotBoundToDigestMatchedBasename(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	seed := openTestStoreAt(t, root, nil)
	deliveredID := uuid.MustParse("abcdef01-2345-6789-abcd-ef0123456789")
	commitSizedRecord(t, seed, deliveredID, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, seed)
	batch, err := seed.NextBatch(1, 64<<20)
	require.NoError(t, err)

	deliveredAlias := strings.ReplaceAll(deliveredID.String(), "-", "")
	require.NoError(t, os.Rename(filepath.Join(seed.readyDir, deliveredID.String()), filepath.Join(seed.readyDir, deliveredAlias)))

	actualID := uuid.MustParse("12345678-90ab-cdef-1234-567890abcdef")
	commitSizedRecord(t, seed, actualID, 8, time.Unix(1_700_000_001, 0).UTC())
	actualAlias := "urn:uuid:" + actualID.String()
	actualAliasPath := filepath.Join(seed.readyDir, actualAlias)
	require.NoError(t, os.Rename(filepath.Join(seed.readyDir, actualID.String()), actualAliasPath))
	require.NoError(t, os.WriteFile(filepath.Join(actualAliasPath, manifestName), []byte("corrupt"), 0o600))

	corruptionID := opaqueReadyNameID(actualAlias)
	require.NoError(t, seed.writeCorruptionTombstoneLocked(corruptionTombstone{
		Version:        corruptionTombstoneVersion,
		ID:             corruptionID,
		AliasCaptureID: deliveredID,
		NameSHA256:     string(corruptionID),
	}))

	reopened := openTestStoreAt(t, root, nil)
	_, err = reopened.Recover(context.Background())
	require.ErrorIs(t, err, ErrSpoolCorrupt)
	require.FileExists(t, batchManifestPath(reopened, batch.ID))
	require.DirExists(t, actualAliasPath)
}
