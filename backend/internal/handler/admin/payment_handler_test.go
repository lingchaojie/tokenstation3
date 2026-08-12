package admin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestSanitizeAdminPaymentOrderForResponseAddsCurrency(t *testing.T) {
	now := time.Now()
	order := &dbent.PaymentOrder{
		ID:          1,
		UserID:      2,
		Amount:      100,
		PayAmount:   108,
		FeeRate:     8,
		OutTradeNo:  "sub2_202606250001",
		PaymentType: "stripe",
		OrderType:   "subscription",
		Status:      "COMPLETED",
		ExpiresAt:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		ProviderSnapshot: map[string]any{
			"schema_version": 2,
			"currency":       "USD",
		},
	}

	got := sanitizeAdminPaymentOrderForResponse(order)
	if got == nil {
		t.Fatal("expected sanitized order")
	}
	if got.Currency != "USD" {
		t.Fatalf("expected currency USD, got %q", got.Currency)
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal sanitized order: %v", err)
	}
	if strings.Contains(string(body), "provider_snapshot") {
		t.Fatalf("expected provider_snapshot to be omitted, got %s", string(body))
	}
}

func TestNewAdminPlanResponsePreservesLocalPlanFieldsWithoutGroupID(t *testing.T) {
	now := time.Now()
	seatLimit := 20
	plan := &dbent.SubscriptionPlan{
		ID:           11,
		GroupID:      7,
		Name:         "All models",
		Description:  "Grok access",
		Price:        19.99,
		Currency:     "CNY",
		ValidityDays: 30,
		ValidityUnit: "days",
		Features:     "OpenAI\nClaude\nGemini\nGrok",
		ProductName:  "Sub2API",
		ForSale:      true,
		SortOrder:    1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	got := newAdminPlanResponse(plan, service.PlanSeatSummary{SeatLimit: &seatLimit, SeatUsed: 3})
	// 投影必须保留 ent 原始响应的全部套餐字段：currency 丢失曾导致编辑保存时
	// 静默清空套餐货币（PlanEditDialog 回传空串 → SetCurrency("")）。
	if got.Currency != "CNY" {
		t.Fatalf("expected currency to be preserved, got %q", got.Currency)
	}
	if !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("expected created_at/updated_at to be preserved, got %v / %v", got.CreatedAt, got.UpdatedAt)
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "group_id") {
		t.Fatalf("admin plan response must not expose legacy group_id: %s", body)
	}
	if got.SeatLimit == nil || *got.SeatLimit != seatLimit || got.SeatUsed != 3 {
		t.Fatalf("expected local virtual seat summary to be preserved: %#v", got)
	}
}
