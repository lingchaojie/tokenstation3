package repository

import (
	"io/fs"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const (
	userPlatformQuotasAddGrokMigration        = "157_user_platform_quotas_add_grok.sql"
	userPlatformQuotasAddGrokOriginalChecksum = "5cace8fa32c6174a72721cd9b01f28f4545de1fd7bcd9ca196a4225056ec4fb8"
)

func TestUserPlatformQuotasAddGrokMigration_AllowsExistingKiroRows(t *testing.T) {
	contentBytes, err := fs.ReadFile(migrations.FS, userPlatformQuotasAddGrokMigration)
	require.NoError(t, err)

	content := string(contentBytes)
	require.Contains(t, content, "user_platform_quotas_platform_check")
	require.Contains(t, content, "'grok'")
	require.Contains(t, content, "'kiro'")
}

func TestUserPlatformQuotasAddGrokMigration_ChecksumCompatibleWithOriginalPublishedFile(t *testing.T) {
	contentBytes, err := fs.ReadFile(migrations.FS, userPlatformQuotasAddGrokMigration)
	require.NoError(t, err)

	currentChecksum := migrationChecksum(string(contentBytes))
	require.NotEqual(t, userPlatformQuotasAddGrokOriginalChecksum, currentChecksum)
	require.True(t, isMigrationChecksumCompatible(
		userPlatformQuotasAddGrokMigration,
		userPlatformQuotasAddGrokOriginalChecksum,
		currentChecksum,
	))
}
