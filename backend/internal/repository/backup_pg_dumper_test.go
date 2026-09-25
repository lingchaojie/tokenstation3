package repository

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func newTestPgDumper(t *testing.T, commandContext func(context.Context, string, ...string) *exec.Cmd) (*PgDumper, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &PgDumper{
		cfg: &config.DatabaseConfig{
			Host:     "db.example.test",
			Port:     5432,
			User:     "sub2api",
			Password: "secret",
			DBName:   "sub2api",
			SSLMode:  "require",
		},
		db:             db,
		commandContext: commandContext,
	}, mock
}

func expectBackupMigrationLock(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).
		WithArgs(migrationsAdvisoryLockID).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
}

func expectBackupMigrationUnlock(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestPgDumperHoldsMigrationLockThroughReaderClose(t *testing.T) {
	var mock sqlmock.Sqlmock
	commandCreated := false
	dumper, createdMock := newTestPgDumper(t, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		commandCreated = true
		require.Equal(t, "pg_dump", name)
		require.Contains(t, args, "--clean")
		require.NoError(t, mock.ExpectationsWereMet(), "migration lock must be acquired before pg_dump is created")
		return exec.CommandContext(ctx, "sh", "-c", "printf backup-data")
	})
	mock = createdMock
	expectBackupMigrationLock(mock)

	reader, err := dumper.Dump(context.Background())
	require.NoError(t, err)
	require.True(t, commandCreated)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "backup-data", string(data))

	expectBackupMigrationUnlock(mock)
	require.Error(t, mock.ExpectationsWereMet(), "migration lock was released before the reader closed")
	require.NoError(t, reader.Close())
	require.NoError(t, mock.ExpectationsWereMet())
	require.NoError(t, reader.Close(), "reader close must be idempotent")
}

func TestPgDumperReleasesMigrationLockWhenStdoutPipeSetupFails(t *testing.T) {
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "sh", "-c", "true")
		cmd.Stdout = io.Discard
		return cmd
	})
	expectBackupMigrationLock(mock)
	expectBackupMigrationUnlock(mock)

	reader, err := dumper.Dump(context.Background())
	require.Nil(t, reader)
	require.ErrorContains(t, err, "create stdout pipe")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperReleasesMigrationLockWhenProcessStartFails(t *testing.T) {
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "/path/that/does/not/exist/pg_dump")
	})
	expectBackupMigrationLock(mock)
	expectBackupMigrationUnlock(mock)

	reader, err := dumper.Dump(context.Background())
	require.Nil(t, reader)
	require.ErrorContains(t, err, "start pg_dump")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperReleasesMigrationLockWhenProcessFails(t *testing.T) {
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf partial-backup; exit 7")
	})
	expectBackupMigrationLock(mock)

	reader, err := dumper.Dump(context.Background())
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "partial-backup", string(data))
	expectBackupMigrationUnlock(mock)
	require.ErrorContains(t, reader.Close(), "pg_dump exited with error")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperReportsUnlockFailureAndDiscardsConnection(t *testing.T) {
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf backup-data")
	})
	expectBackupMigrationLock(mock)

	reader, err := dumper.Dump(context.Background())
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.NoError(t, err)
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
		WithArgs(migrationsAdvisoryLockID).
		WillReturnError(errors.New("unlock unavailable"))
	require.ErrorContains(t, reader.Close(), "release backup migration lock")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperDoesNotStartProcessWhenMigrationLockFails(t *testing.T) {
	commandCreated := false
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		commandCreated = true
		return exec.CommandContext(ctx, "sh", "-c", "true")
	})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).
		WithArgs(migrationsAdvisoryLockID).
		WillReturnError(errors.New("database unavailable"))

	reader, err := dumper.Dump(context.Background())
	require.Nil(t, reader)
	require.ErrorContains(t, err, "acquire backup migration lock")
	require.False(t, commandCreated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperRejectsNilDatabase(t *testing.T) {
	dumper := &PgDumper{cfg: &config.DatabaseConfig{}}
	reader, err := dumper.Dump(context.Background())
	require.Nil(t, reader)
	require.ErrorContains(t, err, "nil sql db")
}

func TestPgDumperRestoreHoldsMigrationLockThroughProcessCompletion(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	finish := filepath.Join(t.TempDir(), "finish")
	atCommandCreation := make(chan error, 1)
	var mock sqlmock.Sqlmock
	dumper, createdMock := newTestPgDumper(t, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name != "psql" {
			atCommandCreation <- errors.New("restore did not create psql")
		} else {
			atCommandCreation <- mock.ExpectationsWereMet()
		}
		return exec.CommandContext(ctx, "sh", "-c", `touch "$1"; while [ ! -e "$2" ]; do sleep 0.01; done`, "sh", started, finish)
	})
	mock = createdMock
	expectBackupMigrationLock(mock)
	expectBackupMigrationUnlock(mock)

	done := make(chan error, 1)
	go func() {
		done <- dumper.Restore(context.Background(), strings.NewReader("backup-data"))
	}()
	creationState := <-atCommandCreation
	require.Error(t, creationState, "unlock must still be pending when psql is created")
	require.ErrorContains(t, creationState, "pg_advisory_unlock", "migration lock must be acquired before psql is created")
	require.Eventually(t, func() bool {
		_, err := os.Stat(started)
		return err == nil
	}, time.Second, 10*time.Millisecond)
	require.Error(t, mock.ExpectationsWereMet(), "migration lock was released while psql was still running")
	require.NoError(t, os.WriteFile(finish, []byte("done"), 0o600))
	require.NoError(t, <-done)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperRestoreReleasesMigrationLockWhenProcessFails(t *testing.T) {
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf restore-failed >&2; exit 7")
	})
	expectBackupMigrationLock(mock)
	expectBackupMigrationUnlock(mock)

	err := dumper.Restore(context.Background(), strings.NewReader("backup-data"))
	require.ErrorContains(t, err, "restore-failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperRestoreReleasesMigrationLockWhenContextIsCanceled(t *testing.T) {
	created := make(chan struct{})
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		close(created)
		return exec.CommandContext(ctx, "sh", "-c", "exec sleep 30")
	})
	expectBackupMigrationLock(mock)
	expectBackupMigrationUnlock(mock)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- dumper.Restore(ctx, strings.NewReader("backup-data"))
	}()
	<-created
	cancel()
	require.Error(t, <-done)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperRestoreReportsUnlockFailureAndDiscardsConnection(t *testing.T) {
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "true")
	})
	expectBackupMigrationLock(mock)
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
		WithArgs(migrationsAdvisoryLockID).
		WillReturnError(errors.New("unlock unavailable"))

	err := dumper.Restore(context.Background(), strings.NewReader("backup-data"))
	require.ErrorContains(t, err, "release backup migration lock")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperRestoreDoesNotStartProcessWhenMigrationLockFails(t *testing.T) {
	commandCreated := false
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		commandCreated = true
		return exec.CommandContext(ctx, "sh", "-c", "true")
	})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).
		WithArgs(migrationsAdvisoryLockID).
		WillReturnError(errors.New("database unavailable"))

	err := dumper.Restore(context.Background(), strings.NewReader("backup-data"))
	require.ErrorContains(t, err, "acquire restore migration lock")
	require.False(t, commandCreated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperRestoreRejectsNilDatabase(t *testing.T) {
	dumper := &PgDumper{cfg: &config.DatabaseConfig{}}
	err := dumper.Restore(context.Background(), strings.NewReader("backup-data"))
	require.ErrorContains(t, err, "nil sql db")
}
