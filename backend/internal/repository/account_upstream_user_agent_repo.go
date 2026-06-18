package repository

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const defaultAccountUpstreamUserAgentLimit = 50
const maxAccountUpstreamUserAgentLimit = 200

type accountUpstreamUserAgentRepository struct {
	db *sql.DB
}

func NewAccountUpstreamUserAgentRepository(db *sql.DB) service.AccountUpstreamUserAgentRepository {
	return &accountUpstreamUserAgentRepository{db: db}
}

func (r *accountUpstreamUserAgentRepository) Record(ctx context.Context, accountID int64, userAgent string) error {
	if r == nil || r.db == nil || accountID <= 0 {
		return nil
	}
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		return nil
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO account_upstream_user_agents (account_id, user_agent, first_seen_at, last_seen_at, seen_count)
		VALUES ($1, $2, NOW(), NOW(), 1)
		ON CONFLICT (account_id, user_agent)
		DO UPDATE SET
			last_seen_at = NOW(),
			seen_count = account_upstream_user_agents.seen_count + 1
	`, accountID, userAgent)
	return err
}

func (r *accountUpstreamUserAgentRepository) ListByAccountID(ctx context.Context, accountID int64, limit int) ([]service.AccountUpstreamUserAgent, error) {
	if r == nil || r.db == nil || accountID <= 0 {
		return []service.AccountUpstreamUserAgent{}, nil
	}
	if limit <= 0 {
		limit = defaultAccountUpstreamUserAgentLimit
	}
	if limit > maxAccountUpstreamUserAgentLimit {
		limit = maxAccountUpstreamUserAgentLimit
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT account_id, user_agent, first_seen_at, last_seen_at, seen_count
		FROM account_upstream_user_agents
		WHERE account_id = $1
		ORDER BY last_seen_at DESC
		LIMIT $2
	`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.AccountUpstreamUserAgent, 0)
	for rows.Next() {
		var item service.AccountUpstreamUserAgent
		if err := rows.Scan(&item.AccountID, &item.UserAgent, &item.FirstSeenAt, &item.LastSeenAt, &item.SeenCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
