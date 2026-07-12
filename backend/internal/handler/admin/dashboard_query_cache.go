package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

var (
	dashboardTrendCache        = newSnapshotCache(30 * time.Second)
	dashboardModelStatsCache   = newSnapshotCache(30 * time.Second)
	dashboardGroupStatsCache   = newSnapshotCache(30 * time.Second)
	dashboardUsersTrendCache   = newSnapshotCache(30 * time.Second)
	dashboardAPIKeysTrendCache = newSnapshotCache(30 * time.Second)
)

type dashboardTrendCacheKey struct {
	StartTime         string  `json:"start_time"`
	EndTime           string  `json:"end_time"`
	Granularity       string  `json:"granularity"`
	UserID            int64   `json:"user_id"`
	APIKeyID          int64   `json:"api_key_id"`
	AccountID         int64   `json:"account_id"`
	GroupID           int64   `json:"group_id"`
	Model             string  `json:"model"`
	ModelFilterSource string  `json:"model_filter_source,omitempty"`
	BillingMode       string  `json:"billing_mode,omitempty"`
	RequestType       *int16  `json:"request_type"`
	Stream            *bool   `json:"stream"`
	BillingType       *int8   `json:"billing_type"`
	ExcludedUserIDs   []int64 `json:"excluded_user_ids,omitempty"`
}

type dashboardModelGroupCacheKey struct {
	StartTime         string  `json:"start_time"`
	EndTime           string  `json:"end_time"`
	UserID            int64   `json:"user_id"`
	APIKeyID          int64   `json:"api_key_id"`
	AccountID         int64   `json:"account_id"`
	GroupID           int64   `json:"group_id"`
	Model             string  `json:"model,omitempty"`
	ModelSource       string  `json:"model_source,omitempty"`
	ModelFilterSource string  `json:"model_filter_source,omitempty"`
	BillingMode       string  `json:"billing_mode,omitempty"`
	RequestType       *int16  `json:"request_type"`
	Stream            *bool   `json:"stream"`
	BillingType       *int8   `json:"billing_type"`
	ExcludedUserIDs   []int64 `json:"excluded_user_ids,omitempty"`
}

type dashboardEntityTrendCacheKey struct {
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Granularity string `json:"granularity"`
	Limit       int    `json:"limit"`
}

func cacheStatusValue(hit bool) string {
	if hit {
		return "hit"
	}
	return "miss"
}

func mustMarshalDashboardCacheKey(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func snapshotPayloadAs[T any](payload any) (T, error) {
	typed, ok := payload.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("unexpected cache payload type %T", payload)
	}
	return typed, nil
}

func (h *DashboardHandler) getUsageTrendCached(
	ctx context.Context,
	startTime, endTime time.Time,
	granularity string,
	filters usagestats.UsageLogFilters,
) ([]usagestats.TrendDataPoint, bool, error) {
	key := mustMarshalDashboardCacheKey(dashboardTrendCacheKey{
		StartTime:         startTime.UTC().Format(time.RFC3339),
		EndTime:           endTime.UTC().Format(time.RFC3339),
		Granularity:       granularity,
		UserID:            filters.UserID,
		APIKeyID:          filters.APIKeyID,
		AccountID:         filters.AccountID,
		GroupID:           filters.GroupID,
		Model:             filters.Model,
		ModelFilterSource: filters.ModelFilterSource,
		BillingMode:       filters.BillingMode,
		RequestType:       filters.RequestType,
		Stream:            filters.Stream,
		BillingType:       filters.BillingType,
		ExcludedUserIDs:   usagestats.NormalizeExcludedUserIDs(filters.ExcludedUserIDs),
	})
	entry, hit, err := dashboardTrendCache.GetOrLoad(key, func() (any, error) {
		return h.dashboardService.GetUsageTrendWithUsageFilters(ctx, startTime, endTime, granularity, filters)
	})
	if err != nil {
		return nil, hit, err
	}
	trend, err := snapshotPayloadAs[[]usagestats.TrendDataPoint](entry.Payload)
	return trend, hit, err
}

func (h *DashboardHandler) getModelStatsCached(
	ctx context.Context,
	startTime, endTime time.Time,
	filters usagestats.UsageLogFilters,
	modelSource string,
) ([]usagestats.ModelStat, bool, error) {
	key := mustMarshalDashboardCacheKey(dashboardModelGroupCacheKey{
		StartTime:         startTime.UTC().Format(time.RFC3339),
		EndTime:           endTime.UTC().Format(time.RFC3339),
		UserID:            filters.UserID,
		APIKeyID:          filters.APIKeyID,
		AccountID:         filters.AccountID,
		GroupID:           filters.GroupID,
		Model:             filters.Model,
		ModelSource:       usagestats.NormalizeModelSource(modelSource),
		ModelFilterSource: filters.ModelFilterSource,
		BillingMode:       filters.BillingMode,
		RequestType:       filters.RequestType,
		Stream:            filters.Stream,
		BillingType:       filters.BillingType,
		ExcludedUserIDs:   usagestats.NormalizeExcludedUserIDs(filters.ExcludedUserIDs),
	})
	entry, hit, err := dashboardModelStatsCache.GetOrLoad(key, func() (any, error) {
		return h.dashboardService.GetModelStatsWithUsageFiltersBySource(ctx, startTime, endTime, filters, modelSource)
	})
	if err != nil {
		return nil, hit, err
	}
	stats, err := snapshotPayloadAs[[]usagestats.ModelStat](entry.Payload)
	return stats, hit, err
}

func (h *DashboardHandler) getGroupStatsCached(
	ctx context.Context,
	startTime, endTime time.Time,
	filters usagestats.UsageLogFilters,
) ([]usagestats.GroupStat, bool, error) {
	key := mustMarshalDashboardCacheKey(dashboardModelGroupCacheKey{
		StartTime:         startTime.UTC().Format(time.RFC3339),
		EndTime:           endTime.UTC().Format(time.RFC3339),
		UserID:            filters.UserID,
		APIKeyID:          filters.APIKeyID,
		AccountID:         filters.AccountID,
		GroupID:           filters.GroupID,
		Model:             filters.Model,
		ModelFilterSource: filters.ModelFilterSource,
		BillingMode:       filters.BillingMode,
		RequestType:       filters.RequestType,
		Stream:            filters.Stream,
		BillingType:       filters.BillingType,
		ExcludedUserIDs:   usagestats.NormalizeExcludedUserIDs(filters.ExcludedUserIDs),
	})
	entry, hit, err := dashboardGroupStatsCache.GetOrLoad(key, func() (any, error) {
		return h.dashboardService.GetGroupStatsWithUsageFilters(ctx, startTime, endTime, filters)
	})
	if err != nil {
		return nil, hit, err
	}
	stats, err := snapshotPayloadAs[[]usagestats.GroupStat](entry.Payload)
	return stats, hit, err
}

func (h *DashboardHandler) getAPIKeyUsageTrendCached(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usagestats.APIKeyUsageTrendPoint, bool, error) {
	key := mustMarshalDashboardCacheKey(dashboardEntityTrendCacheKey{
		StartTime:   startTime.UTC().Format(time.RFC3339),
		EndTime:     endTime.UTC().Format(time.RFC3339),
		Granularity: granularity,
		Limit:       limit,
	})
	entry, hit, err := dashboardAPIKeysTrendCache.GetOrLoad(key, func() (any, error) {
		return h.dashboardService.GetAPIKeyUsageTrend(ctx, startTime, endTime, granularity, limit)
	})
	if err != nil {
		return nil, hit, err
	}
	trend, err := snapshotPayloadAs[[]usagestats.APIKeyUsageTrendPoint](entry.Payload)
	return trend, hit, err
}

func (h *DashboardHandler) getUserUsageTrendCached(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usagestats.UserUsageTrendPoint, bool, error) {
	key := mustMarshalDashboardCacheKey(dashboardEntityTrendCacheKey{
		StartTime:   startTime.UTC().Format(time.RFC3339),
		EndTime:     endTime.UTC().Format(time.RFC3339),
		Granularity: granularity,
		Limit:       limit,
	})
	entry, hit, err := dashboardUsersTrendCache.GetOrLoad(key, func() (any, error) {
		return h.dashboardService.GetUserUsageTrend(ctx, startTime, endTime, granularity, limit)
	})
	if err != nil {
		return nil, hit, err
	}
	trend, err := snapshotPayloadAs[[]usagestats.UserUsageTrendPoint](entry.Payload)
	return trend, hit, err
}
