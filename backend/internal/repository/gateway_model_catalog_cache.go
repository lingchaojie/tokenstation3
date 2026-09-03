package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const modelCatalogPrefix = "gateway:model_catalog:v1:"

func buildModelCatalogKey(groupID int64, platform string) string {
	return fmt.Sprintf("%sgroup:%d:platform:%s", modelCatalogPrefix, groupID, strings.TrimSpace(platform))
}

func (c *gatewayCache) GetModelCatalog(ctx context.Context, groupID int64, platform string) ([]string, error) {
	payload, err := c.rdb.Get(ctx, buildModelCatalogKey(groupID, platform)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, service.ErrModelCatalogCacheMiss
	}
	if err != nil {
		return nil, err
	}
	var models []string
	if err := json.Unmarshal(payload, &models); err != nil {
		return nil, fmt.Errorf("decode model catalog: %w", err)
	}
	if models == nil {
		models = []string{}
	}
	return models, nil
}

func (c *gatewayCache) SetModelCatalog(
	ctx context.Context,
	groupID int64,
	platform string,
	models []string,
	ttl time.Duration,
) error {
	if models == nil {
		models = []string{}
	}
	payload, err := json.Marshal(models)
	if err != nil {
		return fmt.Errorf("encode model catalog: %w", err)
	}
	return c.rdb.Set(ctx, buildModelCatalogKey(groupID, platform), payload, ttl).Err()
}

var _ service.ModelCatalogCache = (*gatewayCache)(nil)
