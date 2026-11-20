//go:build unit

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func scopedAdminKeyMockRows(id, hash string, updatedAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(strings.Split(scopedAdminKeyColumns, ",")).AddRow(
		id, "scope", "", "adm_sc_", hash, true, nil, []byte("[]"), []byte("{}"), int64(9), updatedAt, updatedAt, nil, "", nil,
	)
}

func TestScopedAdminKeyRepositoryRotationAndRevocation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewScopedAdminKeyRepository(db)
	now := time.Now().UTC()
	ctx := context.Background()
	mock.ExpectQuery(`UPDATE scoped_admin_api_keys SET key_hash=\$2, updated_at=GREATEST\(\$3::timestamptz, updated_at \+ interval '1 microsecond'\) WHERE id=\$1 AND key_hash=\$4 AND revoked_at IS NULL RETURNING`).WithArgs("id", "hash", now, "old-hash").WillReturnRows(scopedAdminKeyMockRows("id", "hash", now.Truncate(time.Microsecond)))
	rotated, err := repo.RotateScopedAdminKeyHash(ctx, "id", "old-hash", "hash", now)
	require.NoError(t, err)
	require.Equal(t, now.Truncate(time.Microsecond), rotated.UpdatedAt)
	mock.ExpectExec(`UPDATE scoped_admin_api_keys SET revoked_at=\$2,enabled=false,updated_at=\$2 WHERE id=\$1 AND revoked_at IS NULL`).WithArgs("id", now).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, repo.RevokeScopedAdminKey(ctx, "id", now), service.ErrScopedAdminKeyNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScopedAdminKeyTouchUsesCASAgainstRotationAndRevocation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewScopedAdminKeyRepository(db)
	now := time.Now().UTC()
	mock.ExpectExec(`(?s)WITH creator AS MATERIALIZED .*FOR SHARE OF u.*UPDATE scoped_admin_api_keys SET last_used_at=\$5,last_used_ip=\$4 WHERE id=\$1 AND key_hash=\$2 AND updated_at=\$3 AND revoked_at IS NULL AND enabled=true AND \(expires_at IS NULL OR expires_at>\$5\) AND created_by=\(SELECT id FROM creator\)`).
		WithArgs("id", "observed-hash", now.Add(-time.Minute), "192.0.2.1", now, service.RoleAdmin, service.StatusActive).
		WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, repo.TouchScopedAdminKey(context.Background(), "id", "observed-hash", now.Add(-time.Minute), "192.0.2.1", now), service.ErrScopedAdminKeyNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestScopedAdminKeyRepositoryEditDoesNotRestoreOldHash(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := NewScopedAdminKeyRepository(db)
	k := &service.ScopedAdminKey{ID: "id", Hash: "旧摘要", ScopedAdminKeyInput: service.ScopedAdminKeyInput{Name: "更新", AllowedIPs: []string{}}, UpdatedAt: time.Now().UTC()}
	storedVersion := k.UpdatedAt.Truncate(time.Microsecond)
	mock.ExpectQuery(`UPDATE scoped_admin_api_keys SET name=\$2,description=\$3,enabled=\$4,expires_at=\$5,allowed_ips=\$6,policy=\$7, updated_at=GREATEST\(\$8::timestamptz, updated_at \+ interval '1 microsecond'\) WHERE id=\$1 AND revoked_at IS NULL AND updated_at=\$9 RETURNING`).WithArgs(k.ID, k.Name, k.Description, k.Enabled, k.ExpiresAt, "[]", sqlmock.AnyArg(), k.UpdatedAt, k.ExpectedUpdatedAt).WillReturnRows(scopedAdminKeyMockRows(k.ID, "stored-hash", storedVersion))
	require.NoError(t, repo.UpdateScopedAdminKey(context.Background(), k))
	require.Equal(t, storedVersion, k.UpdatedAt)
	require.Equal(t, "stored-hash", k.Hash)
	require.NoError(t, mock.ExpectationsWereMet())
}
