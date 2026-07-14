//go:build unit

package routes

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
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type paymentRoutesSettingRepo struct{}

func (paymentRoutesSettingRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}
func (paymentRoutesSettingRepo) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}
func (paymentRoutesSettingRepo) Set(context.Context, string, string) error { return nil }
func (paymentRoutesSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (paymentRoutesSettingRepo) SetMultiple(context.Context, map[string]string) error { return nil }
func (paymentRoutesSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (paymentRoutesSettingRepo) Delete(context.Context, string) error { return nil }

func TestPaymentRoutesPublicPlansIsRegisteredWithoutAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	db, err := sql.Open("sqlite", dbName)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	configSvc := service.NewPaymentConfigService(client, paymentRoutesSettingRepo{}, nil)
	paymentHandler := handler.NewPaymentHandler(nil, configSvc)
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterPaymentRoutes(
		v1,
		paymentHandler,
		handler.NewPaymentWebhookHandler(nil, nil),
		adminhandler.NewPaymentHandler(nil, configSvc),
		func(c *gin.Context) { c.Next() },
		func(c *gin.Context) { c.Next() },
		nil,
	)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/payment/public/plans", nil)
	router.ServeHTTP(recorder, req)

	require.NotEqual(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestRegisterPaymentRoutesIncludesIkunPayWebhook(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterPaymentRoutes(
		v1,
		&handler.PaymentHandler{},
		handler.NewPaymentWebhookHandler(nil, nil),
		adminhandler.NewPaymentHandler(nil, nil),
		func(c *gin.Context) { c.Next() },
		func(c *gin.Context) { c.Next() },
		nil,
	)

	registered := map[string]bool{}
	for _, route := range router.Routes() {
		if route.Path == "/api/v1/payment/webhook/ikunpay" {
			registered[route.Method] = true
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if !registered[method] {
			t.Fatalf("%s /api/v1/payment/webhook/ikunpay was not registered", method)
		}
	}
}
