//go:build unit

package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedOrderReadsExposeEasyPayConfirmationHold(t *testing.T) {
	db, err := sql.Open("sqlite", "file:payment_handler_confirmation_hold?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	user, err := client.User.Create().SetEmail("hold@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	createOrder := func(key, status string) *dbent.PaymentOrder {
		order, err := client.PaymentOrder.Create().SetUserID(user.ID).SetUserEmail(user.Email).SetUserName("test").
			SetAmount(10).SetPayAmount(10).SetFeeRate(0).SetRechargeCode(key).SetOutTradeNo(key).
			SetPaymentType(payment.TypeAlipay).SetPaymentTradeNo(key).SetOrderType(payment.OrderTypeBalance).
			SetStatus(status).SetExpiresAt(time.Now().Add(time.Hour)).SetClientIP("127.0.0.1").SetSrcHost("example.com").
			SetProviderSnapshot(map[string]any{"provider_key": "easypay", "private_field": "private"}).
			SetPayURL("https://example.com/pay").SetQrCode("qr-placeholder").Save(ctx)
		require.NoError(t, err)
		return order
	}
	held := createOrder("held", service.OrderStatusPending)
	unpaid := createOrder("unpaid", service.OrderStatusPending)
	completed := createOrder("completed", service.OrderStatusCompleted)
	for _, order := range []*dbent.PaymentOrder{held, completed} {
		_, err := client.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(order.ID, 10)).
			SetAction("PAYMENT_AWAITING_TRADE_NO").SetOperator("easypay").Save(ctx)
		require.NoError(t, err)
	}
	h := NewPaymentHandler(service.NewPaymentService(client, payment.NewRegistry(), nil, nil, nil, nil, nil, nil, nil), nil)
	request := func(handler gin.HandlerFunc, userID int64, order *dbent.PaymentOrder, post bool) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
		if post {
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/payment/orders/verify", bytes.NewBufferString(`{"out_trade_no":"held"}`))
			c.Request.Header.Set("Content-Type", "application/json")
		} else {
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/payment/orders", nil)
		}
		if order != nil {
			c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(order.ID, 10)}}
		}
		handler(c)
		return recorder
	}
	checkHeld := func(data map[string]any) {
		require.Equal(t, true, data["confirmation_pending"])
		require.Equal(t, "easypay", data["provider_key"])
		require.NotContains(t, data, "qr_code")
		require.NotContains(t, data, "pay_url")
		require.NotContains(t, data, "provider_snapshot")
	}
	for _, result := range []*httptest.ResponseRecorder{
		request(h.GetOrder, user.ID, held, false),
		request(h.VerifyOrder, user.ID, nil, true),
	} {
		require.Equal(t, http.StatusOK, result.Code, result.Body.String())
		var payload struct{ Data map[string]any }
		require.NoError(t, json.Unmarshal(result.Body.Bytes(), &payload))
		checkHeld(payload.Data)
	}
	listed := request(h.GetMyOrders, user.ID, nil, false)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var payload struct {
		Data struct{ Items []map[string]any }
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 3)
	for _, item := range payload.Data.Items {
		switch int64(item["id"].(float64)) {
		case held.ID:
			checkHeld(item)
		case unpaid.ID:
			require.Equal(t, "https://example.com/pay", item["pay_url"])
			require.NotContains(t, item, "confirmation_pending")
		case completed.ID:
			require.NotContains(t, item, "confirmation_pending")
		}
	}
	require.Equal(t, http.StatusForbidden, request(h.GetOrder, user.ID+1, held, false).Code)
	require.Equal(t, http.StatusForbidden, request(h.VerifyOrder, user.ID+1, nil, true).Code)
}
