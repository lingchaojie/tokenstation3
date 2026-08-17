package repository

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAuditLogRepositoryTruncateStringPreservesUTF8AtByteBoundary(t *testing.T) {
	input := strings.Repeat("a", 63) + "中"
	got := truncateString(input, 64)
	require.True(t, utf8.ValidString(got))
	require.LessOrEqual(t, len(got), 64)
	require.Equal(t, strings.Repeat("a", 63), got)
}

func TestAuditLogRepositoryTruncateStringHandlesLargeUntrustedFieldInLinearPrefix(t *testing.T) {
	input := strings.Repeat("a", 1<<20) + "中"
	got := truncateString(input, 512)
	require.Equal(t, strings.Repeat("a", 512), got)
	require.True(t, utf8.ValidString(got))
}

func auditLogInsertAnyArgs() []driver.Value {
	args := make([]driver.Value, 16)
	for i := range args {
		args[i] = sqlmock.AnyArg()
	}
	return args
}

func TestAuditLogRepositoryClearAllWithTraceIsAtomic(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectExec("LOCK TABLE audit_logs IN ACCESS EXCLUSIVE MODE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM audit_logs").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectExec("TRUNCATE TABLE audit_logs").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO audit_logs").WithArgs(auditLogInsertAnyArgs()...).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectClose()

	repo := &auditLogRepository{db: db}
	trace := &service.AuditLog{Action: service.AuditActionAuditLogClear}
	deleted, err := repo.ClearAllWithTrace(context.Background(), trace)
	require.NoError(t, err)
	require.EqualValues(t, 3, deleted)
	require.EqualValues(t, 3, trace.Extra["deleted_rows"])
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditLogRepositoryClearAllWithTraceRollsBackWhenTraceInsertFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	insertErr := errors.New("trace insert failed")
	mock.ExpectBegin()
	mock.ExpectExec("LOCK TABLE audit_logs IN ACCESS EXCLUSIVE MODE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM audit_logs").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectExec("TRUNCATE TABLE audit_logs").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO audit_logs").WithArgs(auditLogInsertAnyArgs()...).WillReturnError(insertErr)
	mock.ExpectRollback()
	mock.ExpectClose()

	repo := &auditLogRepository{db: db}
	deleted, err := repo.ClearAllWithTrace(context.Background(), &service.AuditLog{})
	require.ErrorIs(t, err, insertErr)
	require.Zero(t, deleted)
	require.NoError(t, db.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}
