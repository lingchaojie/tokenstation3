package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// AccountUpstreamUserAgent is one deduped User-Agent observed on real upstream
// requests for an account.
type AccountUpstreamUserAgent struct {
	AccountID   int64     `json:"account_id"`
	UserAgent   string    `json:"user_agent"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	SeenCount   int64     `json:"seen_count"`
}

type AccountUpstreamUserAgentRepository interface {
	Record(ctx context.Context, accountID int64, userAgent string) error
	ListByAccountID(ctx context.Context, accountID int64, limit int) ([]AccountUpstreamUserAgent, error)
}

func (s *GatewayService) recordUpstreamUserAgent(ctx context.Context, account *Account, req *http.Request) {
	recordAccountUpstreamUserAgent(ctx, s.upstreamUARepo, account, req, "anthropic")
}

func (s *OpenAIGatewayService) recordUpstreamUserAgent(ctx context.Context, account *Account, req *http.Request) {
	recordAccountUpstreamUserAgent(ctx, s.upstreamUARepo, account, req, "openai")
}

func recordAccountUpstreamUserAgent(ctx context.Context, repo AccountUpstreamUserAgentRepository, account *Account, req *http.Request, component string) {
	if repo == nil || account == nil || req == nil {
		return
	}
	ua := strings.TrimSpace(getHeaderRaw(req.Header, "User-Agent"))
	if ua == "" {
		return
	}

	slog.Info("account_upstream_user_agent.observed",
		"component", component,
		"account_id", account.ID,
		"platform", account.Platform,
		"account_type", account.Type,
		"user_agent", ua,
	)

	if err := repo.Record(ctx, account.ID, ua); err != nil {
		slog.Warn("failed to record account upstream user-agent",
			"component", component,
			"account_id", account.ID,
			"platform", account.Platform,
			"account_type", account.Type,
			"error", err,
		)
	}
}
