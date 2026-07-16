package admin

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func newAdminPaymentPlanResponseClient(t *testing.T) *dbent.Client {
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

func TestAdminUpdatePlanDoesNotExposeLegacyGroupID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := newAdminPaymentPlanResponseClient(t)
	group := client.Group.Create().
		SetName("Legacy Subscription Group").
		SetPlatform(domain.PlatformAnthropic).
		SetSubscriptionType(domain.SubscriptionTypeSubscription).
		SaveX(ctx)
	plan := client.SubscriptionPlan.Create().
		SetGroupID(group.ID).
		SetName("Legacy Plan").
		SetDescription("legacy").
		SetPrice(25).
		SetCurrency("NZD").
		SetValidityDays(30).
		SetValidityUnit("day").
		SetFeatures("legacy").
		SetProductName("Legacy Product").
		SetForSale(true).
		SetSortOrder(1).
		SaveX(ctx)

	configSvc := service.NewPaymentConfigService(client, nil, nil)
	h := NewPaymentHandler(nil, configSvc)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Params = gin.Params{{Key: "id", Value: fmt.Sprintf("%d", plan.ID)}}
	ginCtx.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/payment/plans/1", strings.NewReader(`{"name":"Updated Legacy Plan"}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	h.UpdatePlan(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	require.Contains(t, body, "Updated Legacy Plan")
	require.Contains(t, body, `"currency":"NZD"`)
	require.NotContains(t, body, "group_id")
}
