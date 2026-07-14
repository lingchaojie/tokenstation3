//go:build unit

package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type paymentHandlerSeatSettingRepo struct{}

func (paymentHandlerSeatSettingRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}
func (paymentHandlerSeatSettingRepo) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}
func (paymentHandlerSeatSettingRepo) Set(context.Context, string, string) error { return nil }
func (paymentHandlerSeatSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (paymentHandlerSeatSettingRepo) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (paymentHandlerSeatSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (paymentHandlerSeatSettingRepo) Delete(context.Context, string) error { return nil }

func newPaymentHandlerSeatClient(t *testing.T) *dbent.Client {
	t.Helper()
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	db, err := sql.Open("sqlite", dbName)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func seedPaymentHandlerSeatPlan(t *testing.T, ctx context.Context, client *dbent.Client, limit int, used int) *dbent.SubscriptionPlan {
	t.Helper()
	group := client.Group.Create().
		SetName("Seat Group").
		SetPlatform(domain.PlatformAnthropic).
		SetSubscriptionType(domain.SubscriptionTypeSubscription).
		SetRateMultiplier(1.25).
		SetDailyLimitUsd(10).
		SetWeeklyLimitUsd(50).
		SetMonthlyLimitUsd(100).
		SetSupportedModelScopes([]string{"claude"}).
		SaveX(ctx)
	plan := client.SubscriptionPlan.Create().
		SetGroupID(group.ID).
		SetName("Seat Plan").
		SetDescription("Seat desc").
		SetPrice(12.5).
		SetValidityDays(30).
		SetValidityUnit("days").
		SetFeatures("one\ntwo").
		SetProductName("Seat Product").
		SetForSale(true).
		SetSortOrder(7).
		SetSeatLimit(limit).
		SetVirtualSeatStart(4900).
		SetVirtualSeatTotal(4900 + limit).
		SaveX(ctx)

	for i := 0; i < used; i++ {
		user := client.User.Create().
			SetEmail(fmt.Sprintf("seat-user-%d@example.com", i)).
			SetPasswordHash("hash").
			SetRole(domain.RoleUser).
			SaveX(ctx)
		client.UserSubscription.Create().
			SetUserID(user.ID).
			SetGroupID(group.ID).
			SetPlanID(plan.ID).
			SetStartsAt(time.Now().Add(-time.Hour)).
			SetExpiresAt(time.Now().Add(time.Hour)).
			SetStatus(service.SubscriptionStatusActive).
			SaveX(ctx)
	}
	return plan
}

func TestPaymentGetPlansIncludesSeatSummaryFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newPaymentHandlerSeatClient(t)
	plan := seedPaymentHandlerSeatPlan(t, ctx, client, 2, 1)
	configSvc := service.NewPaymentConfigService(client, paymentHandlerSeatSettingRepo{}, nil)
	h := NewPaymentHandler(nil, configSvc)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/payment/plans", nil)

	h.GetPlans(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var resp struct {
		Code int `json:"code"`
		Data []struct {
			ID               int64 `json:"id"`
			SeatLimit        *int  `json:"seat_limit"`
			SeatUsed         int   `json:"seat_used"`
			SeatAvailable    *int  `json:"seat_available"`
			SeatFull         bool  `json:"seat_full"`
			SeatOverLimit    bool  `json:"seat_over_limit"`
			VirtualSeatStart *int  `json:"virtual_seat_start"`
			VirtualSeatTotal *int  `json:"virtual_seat_total"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data, 1)
	require.Equal(t, plan.ID, resp.Data[0].ID)
	require.NotNil(t, resp.Data[0].SeatLimit)
	require.Equal(t, 2, *resp.Data[0].SeatLimit)
	require.Equal(t, 1, resp.Data[0].SeatUsed)
	require.NotNil(t, resp.Data[0].SeatAvailable)
	require.Equal(t, 1, *resp.Data[0].SeatAvailable)
	require.False(t, resp.Data[0].SeatFull)
	require.False(t, resp.Data[0].SeatOverLimit)
	require.NotNil(t, resp.Data[0].VirtualSeatStart)
	require.Equal(t, 4900, *resp.Data[0].VirtualSeatStart)
	require.NotNil(t, resp.Data[0].VirtualSeatTotal)
	require.Equal(t, 4902, *resp.Data[0].VirtualSeatTotal)
}

func TestPaymentCheckoutInfoIncludesSeatSummaryFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newPaymentHandlerSeatClient(t)
	seedPaymentHandlerSeatPlan(t, ctx, client, 1, 1)
	configSvc := service.NewPaymentConfigService(client, paymentHandlerSeatSettingRepo{}, nil)
	h := NewPaymentHandler(nil, configSvc)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/payment/checkout-info", nil)

	h.GetCheckoutInfo(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Plans []struct {
				Features         []string `json:"features"`
				SeatLimit        *int     `json:"seat_limit"`
				SeatUsed         int      `json:"seat_used"`
				SeatAvailable    *int     `json:"seat_available"`
				SeatFull         bool     `json:"seat_full"`
				SeatOverLimit    bool     `json:"seat_over_limit"`
				VirtualSeatStart *int     `json:"virtual_seat_start"`
				VirtualSeatTotal *int     `json:"virtual_seat_total"`
			} `json:"plans"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Plans, 1)
	require.Equal(t, []string{"one", "two"}, resp.Data.Plans[0].Features)
	require.NotNil(t, resp.Data.Plans[0].SeatLimit)
	require.Equal(t, 1, *resp.Data.Plans[0].SeatLimit)
	require.Equal(t, 1, resp.Data.Plans[0].SeatUsed)
	require.NotNil(t, resp.Data.Plans[0].SeatAvailable)
	require.Equal(t, 0, *resp.Data.Plans[0].SeatAvailable)
	require.True(t, resp.Data.Plans[0].SeatFull)
	require.False(t, resp.Data.Plans[0].SeatOverLimit)
	require.NotNil(t, resp.Data.Plans[0].VirtualSeatStart)
	require.Equal(t, 4900, *resp.Data.Plans[0].VirtualSeatStart)
	require.NotNil(t, resp.Data.Plans[0].VirtualSeatTotal)
	require.Equal(t, 4901, *resp.Data.Plans[0].VirtualSeatTotal)
}

func TestPaymentGetPublicPlansReturnsSaleableDisplayFieldsAndSeatSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newPaymentHandlerSeatClient(t)
	plan := seedPaymentHandlerSeatPlan(t, ctx, client, 1, 1)
	configSvc := service.NewPaymentConfigService(client, paymentHandlerSeatSettingRepo{}, nil)
	h := NewPaymentHandler(nil, configSvc)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/payment/public/plans", nil)

	h.GetPublicPlans(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var resp struct {
		Code int `json:"code"`
		Data []struct {
			ID               int64    `json:"id"`
			Name             string   `json:"name"`
			Description      string   `json:"description"`
			Price            float64  `json:"price"`
			ValidityDays     int      `json:"validity_days"`
			ValidityUnit     string   `json:"validity_unit"`
			Features         []string `json:"features"`
			SortOrder        int      `json:"sort_order"`
			SeatLimit        *int     `json:"seat_limit"`
			SeatUsed         int      `json:"seat_used"`
			SeatAvailable    *int     `json:"seat_available"`
			SeatFull         bool     `json:"seat_full"`
			SeatOverLimit    bool     `json:"seat_over_limit"`
			VirtualSeatStart *int     `json:"virtual_seat_start"`
			VirtualSeatTotal *int     `json:"virtual_seat_total"`
			ProductName      string   `json:"product_name"`
			ForSale          bool     `json:"for_sale"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data, 1)
	got := resp.Data[0]
	require.Equal(t, plan.ID, got.ID)
	require.Equal(t, "Seat Plan", got.Name)
	require.Equal(t, "Seat desc", got.Description)
	require.Equal(t, 12.5, got.Price)
	require.Equal(t, 30, got.ValidityDays)
	require.Equal(t, "days", got.ValidityUnit)
	require.Equal(t, []string{"one", "two"}, got.Features)
	require.Equal(t, 7, got.SortOrder)
	require.NotNil(t, got.SeatLimit)
	require.Equal(t, 1, *got.SeatLimit)
	require.Equal(t, 1, got.SeatUsed)
	require.NotNil(t, got.SeatAvailable)
	require.Equal(t, 0, *got.SeatAvailable)
	require.True(t, got.SeatFull)
	require.False(t, got.SeatOverLimit)
	require.NotNil(t, got.VirtualSeatStart)
	require.Equal(t, 4900, *got.VirtualSeatStart)
	require.NotNil(t, got.VirtualSeatTotal)
	require.Equal(t, 4901, *got.VirtualSeatTotal)
	require.Empty(t, got.ProductName, "public plans must not expose provider/admin payment config")
	require.False(t, got.ForSale, "public plans must not expose provider/admin payment config")
}
