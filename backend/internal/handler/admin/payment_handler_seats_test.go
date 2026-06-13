//go:build unit

package admin

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

func newAdminPaymentSeatClient(t *testing.T) *dbent.Client {
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

func seedAdminPaymentSeatPlan(t *testing.T, ctx context.Context, client *dbent.Client, limit int, used int) *dbent.SubscriptionPlan {
	t.Helper()
	group := client.Group.Create().
		SetName("Admin Seat Group").
		SetPlatform(domain.PlatformAnthropic).
		SetSubscriptionType(domain.SubscriptionTypeSubscription).
		SaveX(ctx)
	plan := client.SubscriptionPlan.Create().
		SetGroupID(group.ID).
		SetName("Admin Seat Plan").
		SetDescription("Admin desc").
		SetPrice(25).
		SetOriginalPrice(30).
		SetValidityDays(90).
		SetValidityUnit("days").
		SetFeatures("alpha\nbeta").
		SetProductName("Admin Product").
		SetForSale(true).
		SetSortOrder(3).
		SetSeatLimit(limit).
		SaveX(ctx)

	for i := 0; i < used; i++ {
		user := client.User.Create().
			SetEmail(fmt.Sprintf("admin-seat-user-%d@example.com", i)).
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

func TestAdminListPlansReturnsMappedSeatSummaryResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newAdminPaymentSeatClient(t)
	plan := seedAdminPaymentSeatPlan(t, ctx, client, 2, 2)
	configSvc := service.NewPaymentConfigService(client, nil, nil)
	h := NewPaymentHandler(nil, configSvc)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/payment/plans", nil)

	h.ListPlans(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var resp struct {
		Code int `json:"code"`
		Data []struct {
			ID            int64     `json:"id"`
			GroupID       int64     `json:"group_id"`
			Name          string    `json:"name"`
			Description   string    `json:"description"`
			Price         float64   `json:"price"`
			OriginalPrice *float64  `json:"original_price"`
			ValidityDays  int       `json:"validity_days"`
			ValidityUnit  string    `json:"validity_unit"`
			Features      string    `json:"features"`
			ProductName   string    `json:"product_name"`
			ForSale       bool      `json:"for_sale"`
			SortOrder     int       `json:"sort_order"`
			CreatedAt     time.Time `json:"created_at"`
			UpdatedAt     time.Time `json:"updated_at"`
			SeatLimit     *int      `json:"seat_limit"`
			SeatUsed      int       `json:"seat_used"`
			SeatAvailable *int      `json:"seat_available"`
			SeatFull      bool      `json:"seat_full"`
			SeatOverLimit bool      `json:"seat_over_limit"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data, 1)
	got := resp.Data[0]
	require.Equal(t, plan.ID, got.ID)
	require.Equal(t, plan.GroupID, got.GroupID)
	require.Equal(t, "Admin Seat Plan", got.Name)
	require.Equal(t, "Admin desc", got.Description)
	require.Equal(t, 25.0, got.Price)
	require.NotNil(t, got.OriginalPrice)
	require.Equal(t, 30.0, *got.OriginalPrice)
	require.Equal(t, 90, got.ValidityDays)
	require.Equal(t, "days", got.ValidityUnit)
	require.Equal(t, "alpha\nbeta", got.Features)
	require.Equal(t, "Admin Product", got.ProductName)
	require.True(t, got.ForSale)
	require.Equal(t, 3, got.SortOrder)
	require.False(t, got.CreatedAt.IsZero())
	require.False(t, got.UpdatedAt.IsZero())
	require.NotNil(t, got.SeatLimit)
	require.Equal(t, 2, *got.SeatLimit)
	require.Equal(t, 2, got.SeatUsed)
	require.NotNil(t, got.SeatAvailable)
	require.Equal(t, 0, *got.SeatAvailable)
	require.True(t, got.SeatFull)
	require.False(t, got.SeatOverLimit)
}
