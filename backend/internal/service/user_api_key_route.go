package service

import (
	"context"
	"time"
)

type UserAPIKeyRoute struct {
	ID        int64
	UserID    int64
	KeyType   string
	GroupID   int64
	Group     *Group
	CreatedAt time.Time
	UpdatedAt time.Time
}

type UserAPIKeyRouteUpdate struct {
	AnthropicGroupID *int64 `json:"anthropic_group_id"`
	OpenAIGroupID    *int64 `json:"openai_group_id"`
}

type UserAPIKeyRoutes struct {
	Anthropic *UserAPIKeyRoute `json:"anthropic,omitempty"`
	OpenAI    *UserAPIKeyRoute `json:"openai,omitempty"`
}

type UserAPIKeyRouteRepository interface {
	GetByUserID(ctx context.Context, userID int64) ([]UserAPIKeyRoute, error)
	GetByUserIDAndKeyType(ctx context.Context, userID int64, keyType string) (*UserAPIKeyRoute, error)
	Upsert(ctx context.Context, route UserAPIKeyRoute) (*UserAPIKeyRoute, error)
	DeleteByUserIDAndKeyType(ctx context.Context, userID int64, keyType string) error
}
