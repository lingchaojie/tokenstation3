//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountStatsModelPricingImageInputPriceRoundTrip(t *testing.T) {
	t.Run("load", func(t *testing.T) {
		repo, mock := newChannelModelPricingTimePricingRepo(t)
		mock.ExpectQuery(`(?s)SELECT .*cache_read_price, image_input_price, image_output_price.*FROM channel_account_stats_model_pricing`).
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "rule_id", "platform", "models", "billing_mode", "input_price", "output_price",
				"cache_write_price", "cache_read_price", "image_input_price", "image_output_price", "per_request_price",
				"created_at", "updated_at",
			}).AddRow(
				int64(11), int64(7), "openai", `["gpt-image-2"]`, service.BillingModeToken,
				nil, nil, nil, nil, 5e-6, nil, nil, time.Time{}, time.Time{},
			))
		expectEmptyModelPricingIntervals(mock)

		pricing, err := repo.batchLoadAccountStatsModelPricing(context.Background(), []int64{7})
		require.NoError(t, err)
		require.Len(t, pricing[7], 1)
		require.NotNil(t, pricing[7][0].ImageInputPrice)
		require.InDelta(t, 5e-6, *pricing[7][0].ImageInputPrice, 1e-12)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("create", func(t *testing.T) {
		repo, mock := newChannelModelPricingTimePricingRepo(t)
		mock.ExpectBegin()
		tx, err := repo.db.Begin()
		require.NoError(t, err)

		imageInputPrice := 5e-6
		pricing := &service.ChannelModelPricing{
			Platform:        "openai",
			Models:          []string{"gpt-image-2"},
			BillingMode:     service.BillingModeToken,
			ImageInputPrice: &imageInputPrice,
		}
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, input_price, output_price, cache_write_price, cache_read_price, image_input_price, image_output_price, per_request_price)")).
			WithArgs(
				int64(7), "openai", []byte(`["gpt-image-2"]`), service.BillingModeToken,
				nil, nil, nil, nil, imageInputPrice, nil, nil,
			).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(11), time.Time{}, time.Time{}))

		require.NoError(t, createAccountStatsModelPricingTx(context.Background(), tx, 7, pricing))
		mock.ExpectRollback()
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
