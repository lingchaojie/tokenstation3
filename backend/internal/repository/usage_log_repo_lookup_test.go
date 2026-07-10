package repository

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogRepository_GetByRequestIDAndAPIKeyID(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := newUsageLogRepositoryWithSQL(nil, db)
	createdAt := time.Now().UTC()
	rows := usageLogSelectRows().
		AddRow(
			int64(123),
			int64(42),
			int64(55),
			int64(77),
			"client:webchat-message-101",
			"claude-sonnet-4",
			sql.NullString{String: "claude-sonnet-4", Valid: true},
			sql.NullString{String: "claude-sonnet-4-upstream", Valid: true},
			sql.NullInt64{Int64: 11, Valid: true},
			sql.NullInt64{Int64: 66, Valid: true},
			10,
			20,
			0,
			0,
			0,
			0,
			0,
			float64(0),
			float64(0.01),
			float64(0.02),
			float64(0),
			float64(0),
			float64(0.03),
			float64(0.03),
			float64(1),
			sql.NullFloat64{Float64: 1, Valid: true},
			int16(0),
			int16(0),
			true,
			false,
			sql.NullInt64{Int64: 100, Valid: true},
			sql.NullInt64{},
			sql.NullString{String: "agent", Valid: true},
			sql.NullString{String: "127.0.0.1", Valid: true},
			0,
			sql.NullString{},
			sql.NullString{},
			sql.NullString{},
			sql.NullString{},
			sql.NullString{},
			0,
			sql.NullString{},
			sql.NullInt64{},
			sql.NullString{},
			sql.NullString{},
			sql.NullString{String: "/api/v1/chat/conversations/7/messages", Valid: true},
			sql.NullString{},
			false,
			sql.NullInt64{},
			sql.NullString{},
			sql.NullString{},
			sql.NullString{},
			sql.NullFloat64{},
			sql.NullFloat64{},
			createdAt,
		)

	query := "SELECT " + usageLogSelectColumns + " FROM usage_logs WHERE request_id = $1 AND api_key_id = $2 LIMIT 1"
	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs("client:webchat-message-101", int64(55)).
		WillReturnRows(rows)

	got, err := repo.GetByRequestIDAndAPIKeyID(context.Background(), " client:webchat-message-101 ", 55)

	require.NoError(t, err)
	require.Equal(t, int64(123), got.ID)
	require.Equal(t, "client:webchat-message-101", got.RequestID)
	require.Equal(t, int64(55), got.APIKeyID)
	require.Equal(t, "claude-sonnet-4-upstream", *got.UpstreamModel)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogRepository_GetByRequestIDAndAPIKeyIDRejectsInvalidInput(t *testing.T) {
	repo := newUsageLogRepositoryWithSQL(nil, nil)

	got, err := repo.GetByRequestIDAndAPIKeyID(context.Background(), " ", 55)

	require.Nil(t, got)
	require.ErrorIs(t, err, service.ErrUsageLogNotFound)
}

func usageLogSelectRows() *sqlmock.Rows {
	return sqlmock.NewRows(strings.Split(usageLogSelectColumns, ", "))
}
