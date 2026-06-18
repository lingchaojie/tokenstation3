package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestAccountUpstreamUserAgentRepositoryRecordUpsertsAndIncrementsSeenCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewAccountUpstreamUserAgentRepository(db)

	const expectedSQL = `
		INSERT INTO account_upstream_user_agents (account_id, user_agent, first_seen_at, last_seen_at, seen_count)
		VALUES ($1, $2, NOW(), NOW(), 1)
		ON CONFLICT (account_id, user_agent)
		DO UPDATE SET
			last_seen_at = NOW(),
			seen_count = account_upstream_user_agents.seen_count + 1
	`
	mock.ExpectExec(regexp.QuoteMeta(expectedSQL)).
		WithArgs(int64(42), "opencode/0.1.0").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.Record(context.Background(), 42, "opencode/0.1.0")

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountUpstreamUserAgentRepositoryListByAccountIDOrdersNewestFirst(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewAccountUpstreamUserAgentRepository(db)

	const expectedSQL = `
		SELECT account_id, user_agent, first_seen_at, last_seen_at, seen_count
		FROM account_upstream_user_agents
		WHERE account_id = $1
		ORDER BY last_seen_at DESC
		LIMIT $2
	`
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(expectedSQL)).
		WithArgs(int64(42), 50).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "user_agent", "first_seen_at", "last_seen_at", "seen_count"}).
			AddRow(int64(42), "codex_cli_rs/0.125.0", now.Add(-time.Hour), now, int64(3)).
			AddRow(int64(42), "opencode/0.1.0", now.Add(-2*time.Hour), now.Add(-time.Hour), int64(1)))

	items, err := repo.ListByAccountID(context.Background(), 42, 50)

	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "codex_cli_rs/0.125.0", items[0].UserAgent)
	require.Equal(t, int64(3), items[0].SeenCount)
	require.Equal(t, "opencode/0.1.0", items[1].UserAgent)
	require.NoError(t, mock.ExpectationsWereMet())
}
