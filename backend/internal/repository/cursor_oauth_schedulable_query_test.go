package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func captureCursorSchedulableEntQuery(t *testing.T, run func(context.Context, *accountRepository) error) string {
	t.Helper()
	var capturedSQL string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcher{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)

	mock.ExpectQuery("cursor oauth schedulable query").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	require.NoError(t, run(context.Background(), repo))
	require.NoError(t, mock.ExpectationsWereMet())
	return normalizeSQLWhitespace(capturedSQL)
}

func requireCursorOAuthOnlyEntPredicate(t *testing.T, query string) {
	t.Helper()
	platformIndex := strings.Index(query, `"platform" <>`)
	require.NotEqual(t, -1, platformIndex, "query must reject legacy Cursor account types: %s", query)
	remaining := query[platformIndex:]
	orIndex := strings.Index(remaining, " OR ")
	typeIndex := strings.Index(remaining, `"type" =`)
	require.NotEqual(t, -1, orIndex, "Cursor predicate must use platform/type disjunction: %s", query)
	require.Greater(t, typeIndex, orIndex, "Cursor predicate must require OAuth after the disjunction: %s", query)
}

func TestSchedulableEntQueriesExcludeLegacyCursorNonOAuthAccounts(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *accountRepository) error
	}{
		{name: "all", run: func(ctx context.Context, repo *accountRepository) error {
			_, err := repo.ListSchedulable(ctx)
			return err
		}},
		{name: "load projection", run: func(ctx context.Context, repo *accountRepository) error {
			_, err := repo.ListSchedulableAccountLoads(ctx)
			return err
		}},
		{name: "platform", run: func(ctx context.Context, repo *accountRepository) error {
			_, err := repo.ListSchedulableByPlatform(ctx, service.PlatformCursor)
			return err
		}},
		{name: "platforms", run: func(ctx context.Context, repo *accountRepository) error {
			_, err := repo.ListSchedulableByPlatforms(ctx, []string{service.PlatformCursor, service.PlatformOpenAI})
			return err
		}},
		{name: "ungrouped platform", run: func(ctx context.Context, repo *accountRepository) error {
			_, err := repo.ListSchedulableUngroupedByPlatform(ctx, service.PlatformCursor)
			return err
		}},
		{name: "ungrouped platforms", run: func(ctx context.Context, repo *accountRepository) error {
			_, err := repo.ListSchedulableUngroupedByPlatforms(ctx, []string{service.PlatformCursor, service.PlatformOpenAI})
			return err
		}},
		{name: "group", run: func(ctx context.Context, repo *accountRepository) error {
			_, err := repo.ListSchedulableByGroupID(ctx, 41)
			return err
		}},
		{name: "group platform", run: func(ctx context.Context, repo *accountRepository) error {
			_, err := repo.ListSchedulableByGroupIDAndPlatform(ctx, 41, service.PlatformCursor)
			return err
		}},
		{name: "group platforms", run: func(ctx context.Context, repo *accountRepository) error {
			_, err := repo.ListSchedulableByGroupIDAndPlatforms(ctx, 41, []string{service.PlatformCursor, service.PlatformOpenAI})
			return err
		}},
		{name: "model availability", run: func(ctx context.Context, repo *accountRepository) error {
			_, err := repo.ListModelAvailabilityCandidates(ctx, nil, []string{service.PlatformCursor}, true)
			return err
		}},
		{name: "group model availability", run: func(ctx context.Context, repo *accountRepository) error {
			groupID := int64(41)
			_, err := repo.ListModelAvailabilityCandidates(ctx, &groupID, []string{service.PlatformCursor}, false)
			return err
		}},
		{name: "admin active filter", run: func(ctx context.Context, repo *accountRepository) error {
			_, err := repo.accountListFilteredQuery("", "", service.StatusActive, "", 0, "").All(ctx)
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireCursorOAuthOnlyEntPredicate(t, captureCursorSchedulableEntQuery(t, tt.run))
		})
	}
}

func TestSchedulableCapacityQueryExcludesLegacyCursorNonOAuthAccounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var capturedSQL string
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
		"group_id", "account_id", "platform", "type", "credentials", "concurrency", "extra",
		"session_window_start", "session_window_end", "session_window_status",
	}))
	repo := newAccountRepositoryWithSQL(nil, captureQuerySQL{db: db, captured: &capturedSQL}, nil)

	rows, err := repo.ListSchedulableCapacityByGroupIDs(context.Background(), []int64{41})

	require.NoError(t, err)
	require.Empty(t, rows)
	require.Contains(t, normalizeSQLWhitespace(capturedSQL), "(a.platform <> 'cursor' OR a.type = 'oauth')")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGroupAvailabilityQueriesExcludeLegacyCursorNonOAuthAccounts(t *testing.T) {
	t.Run("single group", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		var capturedSQL string
		mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"total", "active", "limited"}).AddRow(3, 1, 0))
		repo := newGroupRepositoryWithSQL(nil, captureQuerySQL{db: db, captured: &capturedSQL})

		total, active, err := repo.GetAccountCount(context.Background(), 41)

		require.NoError(t, err)
		require.Equal(t, int64(3), total)
		require.Equal(t, int64(1), active)
		require.Equal(t, 2, strings.Count(normalizeSQLWhitespace(capturedSQL), "(a.platform <> 'cursor' OR a.type = 'oauth')"),
			"available and temporarily limited counts must both exclude invalid Cursor rows")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("group list counts", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		var capturedSQL string
		mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"group_id", "total", "active", "limited"}))
		repo := newGroupRepositoryWithSQL(nil, captureQuerySQL{db: db, captured: &capturedSQL})

		counts, err := repo.loadAccountCounts(context.Background(), []int64{41})

		require.NoError(t, err)
		require.Empty(t, counts)
		require.Equal(t, 2, strings.Count(normalizeSQLWhitespace(capturedSQL), "(a.platform <> 'cursor' OR a.type = 'oauth')"))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDashboardNormalAccountCountExcludesLegacyCursorNonOAuthAccounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var capturedSQL string
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"total_users", "today_users"}).AddRow(0, 0))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"total_keys", "active_keys"}).AddRow(0, 0))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
		"total_accounts", "normal_accounts", "error_accounts", "ratelimit_accounts", "overload_accounts",
	}).AddRow(0, 0, 0, 0, 0))
	repo := newUsageLogRepositoryWithSQL(nil, captureQuerySQL{db: db, captured: &capturedSQL})

	err = repo.fillDashboardEntityStats(context.Background(), &DashboardStats{}, time.Now(), time.Now())

	require.NoError(t, err)
	require.Contains(t, normalizeSQLWhitespace(capturedSQL), "(platform <> 'cursor' OR type = 'oauth')")
	require.NoError(t, mock.ExpectationsWereMet())
}
