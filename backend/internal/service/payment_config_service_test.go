package service

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/payment"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func TestPcParseFloat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		defaultVal float64
		expected   float64
	}{
		{"empty string returns default", "", 1.0, 1.0},
		{"valid float", "3.14", 0, 3.14},
		{"valid integer as float", "42", 0, 42.0},
		{"invalid string returns default", "notanumber", 9.99, 9.99},
		{"zero value", "0", 5.0, 0},
		{"negative value", "-10.5", 0, -10.5},
		{"very large value", "99999999.99", 0, 99999999.99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pcParseFloat(tt.input, tt.defaultVal)
			if got != tt.expected {
				t.Fatalf("pcParseFloat(%q, %v) = %v, want %v", tt.input, tt.defaultVal, got, tt.expected)
			}
		})
	}
}

func TestPcParseInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		defaultVal int
		expected   int
	}{
		{"empty string returns default", "", 30, 30},
		{"valid int", "10", 0, 10},
		{"invalid string returns default", "abc", 5, 5},
		{"float string returns default", "3.14", 0, 0},
		{"zero value", "0", 99, 0},
		{"negative value", "-1", 0, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pcParseInt(tt.input, tt.defaultVal)
			if got != tt.expected {
				t.Fatalf("pcParseInt(%q, %v) = %v, want %v", tt.input, tt.defaultVal, got, tt.expected)
			}
		})
	}
}

func TestParsePaymentConfig(t *testing.T) {
	t.Parallel()

	svc := &PaymentConfigService{}

	t.Run("empty vals uses defaults", func(t *testing.T) {
		t.Parallel()
		cfg := svc.parsePaymentConfig(map[string]string{})
		if cfg.Enabled {
			t.Fatal("expected Enabled=false by default")
		}
		if cfg.MinAmount != 1 {
			t.Fatalf("expected MinAmount=1, got %v", cfg.MinAmount)
		}
		if cfg.MaxAmount != 0 {
			t.Fatalf("expected MaxAmount=0 (no limit), got %v", cfg.MaxAmount)
		}
		if cfg.OrderTimeoutMin != 30 {
			t.Fatalf("expected OrderTimeoutMin=30, got %v", cfg.OrderTimeoutMin)
		}
		if cfg.MaxPendingOrders != 3 {
			t.Fatalf("expected MaxPendingOrders=3, got %v", cfg.MaxPendingOrders)
		}
		if cfg.LoadBalanceStrategy != payment.DefaultLoadBalanceStrategy {
			t.Fatalf("expected LoadBalanceStrategy=%s, got %q", payment.DefaultLoadBalanceStrategy, cfg.LoadBalanceStrategy)
		}
		if len(cfg.EnabledTypes) != 0 {
			t.Fatalf("expected empty EnabledTypes, got %v", cfg.EnabledTypes)
		}
	})

	t.Run("all values populated", func(t *testing.T) {
		t.Parallel()
		vals := map[string]string{
			SettingPaymentEnabled:      "true",
			SettingMinRechargeAmount:   "5.00",
			SettingMaxRechargeAmount:   "1000.00",
			SettingDailyRechargeLimit:  "5000.00",
			SettingOrderTimeoutMinutes: "15",
			SettingMaxPendingOrders:    "5",
			SettingEnabledPaymentTypes: "alipay,wxpay,stripe",
			SettingBalancePayDisabled:  "true",
			SettingLoadBalanceStrategy: "least_amount",
			SettingProductNamePrefix:   "PRE",
			SettingProductNameSuffix:   "SUF",
		}
		cfg := svc.parsePaymentConfig(vals)

		if !cfg.Enabled {
			t.Fatal("expected Enabled=true")
		}
		if cfg.MinAmount != 5 {
			t.Fatalf("MinAmount = %v, want 5", cfg.MinAmount)
		}
		if cfg.MaxAmount != 1000 {
			t.Fatalf("MaxAmount = %v, want 1000", cfg.MaxAmount)
		}
		if cfg.DailyLimit != 5000 {
			t.Fatalf("DailyLimit = %v, want 5000", cfg.DailyLimit)
		}
		if cfg.OrderTimeoutMin != 15 {
			t.Fatalf("OrderTimeoutMin = %v, want 15", cfg.OrderTimeoutMin)
		}
		if cfg.MaxPendingOrders != 5 {
			t.Fatalf("MaxPendingOrders = %v, want 5", cfg.MaxPendingOrders)
		}
		if len(cfg.EnabledTypes) != 3 {
			t.Fatalf("EnabledTypes len = %d, want 3", len(cfg.EnabledTypes))
		}
		if cfg.EnabledTypes[0] != "alipay" || cfg.EnabledTypes[1] != "wxpay" || cfg.EnabledTypes[2] != "stripe" {
			t.Fatalf("EnabledTypes = %v, want [alipay wxpay stripe]", cfg.EnabledTypes)
		}
		if !cfg.BalanceDisabled {
			t.Fatal("expected BalanceDisabled=true")
		}
		if cfg.LoadBalanceStrategy != "least_amount" {
			t.Fatalf("LoadBalanceStrategy = %q, want %q", cfg.LoadBalanceStrategy, "least_amount")
		}
		if cfg.ProductNamePrefix != "PRE" {
			t.Fatalf("ProductNamePrefix = %q, want %q", cfg.ProductNamePrefix, "PRE")
		}
		if cfg.ProductNameSuffix != "SUF" {
			t.Fatalf("ProductNameSuffix = %q, want %q", cfg.ProductNameSuffix, "SUF")
		}
	})

	t.Run("enabled types with spaces are trimmed", func(t *testing.T) {
		t.Parallel()
		vals := map[string]string{
			SettingEnabledPaymentTypes: " alipay , wxpay ",
		}
		cfg := svc.parsePaymentConfig(vals)
		if len(cfg.EnabledTypes) != 2 {
			t.Fatalf("EnabledTypes len = %d, want 2", len(cfg.EnabledTypes))
		}
		if cfg.EnabledTypes[0] != "alipay" || cfg.EnabledTypes[1] != "wxpay" {
			t.Fatalf("EnabledTypes = %v, want [alipay wxpay]", cfg.EnabledTypes)
		}
	})

	t.Run("enabled types are normalized to visible methods and deduplicated", func(t *testing.T) {
		t.Parallel()
		vals := map[string]string{
			SettingEnabledPaymentTypes: "alipay_direct, alipay, wxpay_direct, wxpay",
		}
		cfg := svc.parsePaymentConfig(vals)
		if len(cfg.EnabledTypes) != 2 {
			t.Fatalf("EnabledTypes len = %d, want 2", len(cfg.EnabledTypes))
		}
		if cfg.EnabledTypes[0] != "alipay" || cfg.EnabledTypes[1] != "wxpay" {
			t.Fatalf("EnabledTypes = %v, want [alipay wxpay]", cfg.EnabledTypes)
		}
	})

	t.Run("empty enabled types string", func(t *testing.T) {
		t.Parallel()
		vals := map[string]string{
			SettingEnabledPaymentTypes: "",
		}
		cfg := svc.parsePaymentConfig(vals)
		if len(cfg.EnabledTypes) != 0 {
			t.Fatalf("expected empty EnabledTypes for empty string, got %v", cfg.EnabledTypes)
		}
	})
}

func TestGetBasePaymentType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{payment.TypeEasyPay, payment.TypeEasyPay},
		{payment.TypeStripe, payment.TypeStripe},
		{payment.TypeCard, payment.TypeStripe},
		{payment.TypeLink, payment.TypeStripe},
		{payment.TypeAlipay, payment.TypeAlipay},
		{payment.TypeAlipayDirect, payment.TypeAlipay},
		{payment.TypeWxpay, payment.TypeWxpay},
		{payment.TypeWxpayDirect, payment.TypeWxpay},
		{"unknown", "unknown"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := payment.GetBasePaymentType(tt.input)
			if got != tt.expected {
				t.Fatalf("GetBasePaymentType(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestApplyVisibleMethodRoutingToEnabledTypes(t *testing.T) {
	t.Parallel()

	base := []string{"alipay", "wxpay", "stripe"}
	vals := map[string]string{
		SettingPaymentVisibleMethodAlipayEnabled: "true",
		SettingPaymentVisibleMethodAlipaySource:  VisibleMethodSourceOfficialAlipay,
		SettingPaymentVisibleMethodWxpayEnabled:  "true",
		SettingPaymentVisibleMethodWxpaySource:   VisibleMethodSourceOfficialWechat,
	}
	available := map[string]bool{
		VisibleMethodSourceOfficialAlipay: true,
		VisibleMethodSourceOfficialWechat: false,
	}

	got := applyVisibleMethodRoutingToEnabledTypes(base, vals, available)
	want := []string{"alipay", "stripe"}
	if len(got) != len(want) {
		t.Fatalf("applyVisibleMethodRoutingToEnabledTypes len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("applyVisibleMethodRoutingToEnabledTypes[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestApplyVisibleMethodRoutingAddsConfiguredVisibleMethod(t *testing.T) {
	t.Parallel()

	base := []string{"stripe"}
	vals := map[string]string{
		SettingPaymentVisibleMethodAlipayEnabled: "true",
		SettingPaymentVisibleMethodAlipaySource:  VisibleMethodSourceEasyPayAlipay,
	}
	available := map[string]bool{
		VisibleMethodSourceEasyPayAlipay: true,
	}

	got := applyVisibleMethodRoutingToEnabledTypes(base, vals, available)
	want := []string{"stripe", "alipay"}
	if len(got) != len(want) {
		t.Fatalf("applyVisibleMethodRoutingToEnabledTypes len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("applyVisibleMethodRoutingToEnabledTypes[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestBuildVisibleMethodSourceAvailability(t *testing.T) {
	t.Parallel()

	instances := []*dbent.PaymentProviderInstance{
		{ProviderKey: payment.TypeAlipay, SupportedTypes: "alipay"},
		{ProviderKey: payment.TypeEasyPay, SupportedTypes: "wxpay_direct, alipay"},
		{ProviderKey: payment.TypeIkunPay, SupportedTypes: "alipay,wxpay"},
		{ProviderKey: payment.TypeWxpay, SupportedTypes: "wxpay_direct"},
	}

	got := buildVisibleMethodSourceAvailability(instances)
	if !got[VisibleMethodSourceOfficialAlipay] {
		t.Fatalf("expected %q to be available", VisibleMethodSourceOfficialAlipay)
	}
	if !got[VisibleMethodSourceEasyPayAlipay] {
		t.Fatalf("expected %q to be available", VisibleMethodSourceEasyPayAlipay)
	}
	if !got[VisibleMethodSourceOfficialWechat] {
		t.Fatalf("expected %q to be available", VisibleMethodSourceOfficialWechat)
	}
	if !got[VisibleMethodSourceEasyPayWechat] {
		t.Fatalf("expected %q to be available", VisibleMethodSourceEasyPayWechat)
	}
	if !got[VisibleMethodSourceIkunPayAlipay] {
		t.Fatalf("expected %q to be available", VisibleMethodSourceIkunPayAlipay)
	}
	if !got[VisibleMethodSourceIkunPayWechat] {
		t.Fatalf("expected %q to be available", VisibleMethodSourceIkunPayWechat)
	}
}

func TestGetPaymentConfigKeepsStoredEnabledTypes(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)

	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeEasyPay).
		SetName("EasyPay Alipay").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		Save(ctx)
	if err != nil {
		t.Fatalf("create easypay instance: %v", err)
	}

	svc := &PaymentConfigService{
		entClient: client,
		settingRepo: &paymentConfigSettingRepoStub{
			values: map[string]string{
				SettingEnabledPaymentTypes: "alipay,wxpay,stripe",
			},
		},
	}

	cfg, err := svc.GetPaymentConfig(ctx)
	if err != nil {
		t.Fatalf("GetPaymentConfig returned error: %v", err)
	}

	want := []string{payment.TypeAlipay, payment.TypeWxpay, payment.TypeStripe}
	if len(cfg.EnabledTypes) != len(want) {
		t.Fatalf("EnabledTypes len = %d, want %d (%v)", len(cfg.EnabledTypes), len(want), cfg.EnabledTypes)
	}
	for i := range want {
		if cfg.EnabledTypes[i] != want[i] {
			t.Fatalf("EnabledTypes[%d] = %q, want %q (full=%v)", i, cfg.EnabledTypes[i], want[i], cfg.EnabledTypes)
		}
	}
}

func newPaymentConfigServiceTestClient(t *testing.T) *dbent.Client {
	t.Helper()

	dbName := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()),
	)
	db, err := sql.Open("sqlite", dbName)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

type paymentConfigSettingRepoStub struct {
	values  map[string]string
	updates map[string]string
}

func (s *paymentConfigSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, nil
}
func (s *paymentConfigSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}
func (s *paymentConfigSettingRepoStub) Set(context.Context, string, string) error { return nil }
func (s *paymentConfigSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = s.values[key]
	}
	return out, nil
}
func (s *paymentConfigSettingRepoStub) SetMultiple(_ context.Context, values map[string]string) error {
	s.updates = make(map[string]string, len(values))
	for key, value := range values {
		s.updates[key] = value
		if s.values == nil {
			s.values = map[string]string{}
		}
		s.values[key] = value
	}
	return nil
}
func (s *paymentConfigSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return s.values, nil
}
func (s *paymentConfigSettingRepoStub) Delete(context.Context, string) error { return nil }

func TestUpdatePaymentConfig_PersistsPaymentEnabledPublicSetting(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{}}
	svc := &PaymentConfigService{settingRepo: repo}

	enabled := true
	err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}

	if repo.values[SettingPaymentEnabled] != "true" {
		t.Fatalf("payment enabled = %q, want true", repo.values[SettingPaymentEnabled])
	}
}

func TestUpdatePaymentConfig_PersistsVisibleMethodRouting(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{}}
	svc := &PaymentConfigService{settingRepo: repo}

	alipayEnabled := true
	wxpayEnabled := false
	err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{
		VisibleMethodAlipayEnabled: &alipayEnabled,
		VisibleMethodAlipaySource:  paymentConfigStrPtr(VisibleMethodSourceEasyPayAlipay),
		VisibleMethodWxpayEnabled:  &wxpayEnabled,
		VisibleMethodWxpaySource:   paymentConfigStrPtr(VisibleMethodSourceOfficialWechat),
	})
	if err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}

	if repo.values[SettingPaymentVisibleMethodAlipayEnabled] != "true" {
		t.Fatalf("alipay enabled = %q, want true", repo.values[SettingPaymentVisibleMethodAlipayEnabled])
	}
	if repo.values[SettingPaymentVisibleMethodAlipaySource] != VisibleMethodSourceEasyPayAlipay {
		t.Fatalf("alipay source = %q, want %q", repo.values[SettingPaymentVisibleMethodAlipaySource], VisibleMethodSourceEasyPayAlipay)
	}
	if repo.values[SettingPaymentVisibleMethodWxpayEnabled] != "false" {
		t.Fatalf("wxpay enabled = %q, want false", repo.values[SettingPaymentVisibleMethodWxpayEnabled])
	}
	if repo.values[SettingPaymentVisibleMethodWxpaySource] != VisibleMethodSourceOfficialWechat {
		t.Fatalf("wxpay source = %q, want %q", repo.values[SettingPaymentVisibleMethodWxpaySource], VisibleMethodSourceOfficialWechat)
	}
}

func paymentConfigStrPtr(value string) *string {
	return &value
}

func TestPaymentConfigServicePlanVirtualSeatRangeDerivesSeatLimit(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}

	start := 4900
	total := 5000
	created, err := svc.CreatePlan(ctx, CreatePlanRequest{
		Name:             "Pro monthly",
		Description:      "Primary development",
		Price:            799,
		ValidityDays:     30,
		ValidityUnit:     "day",
		Features:         "Seven-day quota",
		ProductName:      "LINX2 Pro monthly",
		ForSale:          true,
		SortOrder:        30,
		VirtualSeatStart: &start,
		VirtualSeatTotal: &total,
	})
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}
	if created.SeatLimit == nil || *created.SeatLimit != 100 {
		t.Fatalf("created.SeatLimit = %v, want 100", created.SeatLimit)
	}
	if created.VirtualSeatStart == nil || *created.VirtualSeatStart != 4900 {
		t.Fatalf("created.VirtualSeatStart = %v, want 4900", created.VirtualSeatStart)
	}
	if created.VirtualSeatTotal == nil || *created.VirtualSeatTotal != 5000 {
		t.Fatalf("created.VirtualSeatTotal = %v, want 5000", created.VirtualSeatTotal)
	}

	updatedStart := 120
	updatedTotal := 150
	var virtualStart OptionalInt
	if err := virtualStart.UnmarshalJSON([]byte(strconv.Itoa(updatedStart))); err != nil {
		t.Fatalf("decode virtual start: %v", err)
	}
	var virtualTotal OptionalInt
	if err := virtualTotal.UnmarshalJSON([]byte(strconv.Itoa(updatedTotal))); err != nil {
		t.Fatalf("decode virtual total: %v", err)
	}
	updated, err := svc.UpdatePlan(ctx, int64(created.ID), UpdatePlanRequest{
		VirtualSeatStart: virtualStart,
		VirtualSeatTotal: virtualTotal,
	})
	if err != nil {
		t.Fatalf("UpdatePlan returned error: %v", err)
	}
	if updated.SeatLimit == nil || *updated.SeatLimit != 30 {
		t.Fatalf("updated.SeatLimit = %v, want 30", updated.SeatLimit)
	}
	if updated.VirtualSeatStart == nil || *updated.VirtualSeatStart != 120 {
		t.Fatalf("updated.VirtualSeatStart = %v, want 120", updated.VirtualSeatStart)
	}
	if updated.VirtualSeatTotal == nil || *updated.VirtualSeatTotal != 150 {
		t.Fatalf("updated.VirtualSeatTotal = %v, want 150", updated.VirtualSeatTotal)
	}

	var clearStart OptionalInt
	if err := clearStart.UnmarshalJSON([]byte("null")); err != nil {
		t.Fatalf("decode clear start: %v", err)
	}
	var clearTotal OptionalInt
	if err := clearTotal.UnmarshalJSON([]byte("null")); err != nil {
		t.Fatalf("decode clear total: %v", err)
	}
	cleared, err := svc.UpdatePlan(ctx, int64(created.ID), UpdatePlanRequest{
		VirtualSeatStart: clearStart,
		VirtualSeatTotal: clearTotal,
	})
	if err != nil {
		t.Fatalf("UpdatePlan clear range returned error: %v", err)
	}
	if cleared.SeatLimit != nil {
		t.Fatalf("cleared.SeatLimit = %v, want nil", cleared.SeatLimit)
	}
	if cleared.VirtualSeatStart != nil {
		t.Fatalf("cleared.VirtualSeatStart = %v, want nil", cleared.VirtualSeatStart)
	}
	if cleared.VirtualSeatTotal != nil {
		t.Fatalf("cleared.VirtualSeatTotal = %v, want nil", cleared.VirtualSeatTotal)
	}
}

func TestPaymentConfigServicePlanVirtualSeatRangeRejectsInvalidCreate(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}

	start := 5000
	total := 4900
	_, err := svc.CreatePlan(ctx, CreatePlanRequest{
		Name:             "Invalid virtual range",
		Description:      "invalid",
		Price:            799,
		ValidityDays:     30,
		ValidityUnit:     "day",
		Features:         "Feature",
		ProductName:      "Invalid",
		ForSale:          true,
		SortOrder:        40,
		VirtualSeatStart: &start,
		VirtualSeatTotal: &total,
	})
	if err == nil {
		t.Fatal("CreatePlan with invalid virtual range returned nil error")
	}
	if !strings.Contains(err.Error(), ">= start") {
		t.Fatalf("CreatePlan error = %q, want message containing >= start", err.Error())
	}
}

func TestPaymentConfigServicePlanVirtualSeatRangeRejectsConflictingSeatLimit(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}

	start := 4900
	total := 5000
	seatLimit := 99
	_, err := svc.CreatePlan(ctx, CreatePlanRequest{
		Name:             "Conflicting virtual range",
		Description:      "invalid",
		Price:            799,
		ValidityDays:     30,
		ValidityUnit:     "day",
		Features:         "Feature",
		ProductName:      "Invalid",
		ForSale:          true,
		SortOrder:        40,
		SeatLimit:        &seatLimit,
		VirtualSeatStart: &start,
		VirtualSeatTotal: &total,
	})
	if err == nil {
		t.Fatal("CreatePlan with conflicting seat limit returned nil error")
	}
	if !strings.Contains(err.Error(), "seat limit") {
		t.Fatalf("CreatePlan error = %q, want message containing seat limit", err.Error())
	}

	validSeatLimit := 100
	created, err := svc.CreatePlan(ctx, CreatePlanRequest{
		Name:             "Valid virtual range",
		Description:      "valid",
		Price:            799,
		ValidityDays:     30,
		ValidityUnit:     "day",
		Features:         "Feature",
		ProductName:      "Valid",
		ForSale:          true,
		SortOrder:        41,
		SeatLimit:        &validSeatLimit,
		VirtualSeatStart: &start,
		VirtualSeatTotal: &total,
	})
	if err != nil {
		t.Fatalf("CreatePlan with matching seat limit returned error: %v", err)
	}

	var optionalLimit OptionalInt
	if err := optionalLimit.UnmarshalJSON([]byte(strconv.Itoa(seatLimit))); err != nil {
		t.Fatalf("decode conflicting seat limit: %v", err)
	}
	var virtualStart OptionalInt
	if err := virtualStart.UnmarshalJSON([]byte(strconv.Itoa(start))); err != nil {
		t.Fatalf("decode virtual start: %v", err)
	}
	var virtualTotal OptionalInt
	if err := virtualTotal.UnmarshalJSON([]byte(strconv.Itoa(total))); err != nil {
		t.Fatalf("decode virtual total: %v", err)
	}

	_, err = svc.UpdatePlan(ctx, int64(created.ID), UpdatePlanRequest{
		SeatLimit:        optionalLimit,
		VirtualSeatStart: virtualStart,
		VirtualSeatTotal: virtualTotal,
	})
	if err == nil {
		t.Fatal("UpdatePlan with conflicting seat limit returned nil error")
	}
	if !strings.Contains(err.Error(), "seat limit") {
		t.Fatalf("UpdatePlan error = %q, want message containing seat limit", err.Error())
	}
}

func TestPaymentConfigServicePlanVirtualSeatRangeDerivesLegacySeatLimit(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}

	seatLimit := 42
	created, err := svc.CreatePlan(ctx, CreatePlanRequest{
		Name:         "Legacy seat limit",
		Description:  "Legacy direct seat_limit path",
		Price:        199,
		ValidityDays: 30,
		ValidityUnit: "day",
		Features:     "Feature",
		ProductName:  "Legacy",
		ForSale:      true,
		SortOrder:    41,
		SeatLimit:    &seatLimit,
	})
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}
	if created.SeatLimit == nil || *created.SeatLimit != 42 {
		t.Fatalf("created.SeatLimit = %v, want 42", created.SeatLimit)
	}
	if created.VirtualSeatStart == nil || *created.VirtualSeatStart != 0 {
		t.Fatalf("created.VirtualSeatStart = %v, want 0", created.VirtualSeatStart)
	}
	if created.VirtualSeatTotal == nil || *created.VirtualSeatTotal != 42 {
		t.Fatalf("created.VirtualSeatTotal = %v, want 42", created.VirtualSeatTotal)
	}

	updatedLimit := 55
	var optionalLimit OptionalInt
	if err := optionalLimit.UnmarshalJSON([]byte(strconv.Itoa(updatedLimit))); err != nil {
		t.Fatalf("decode seat limit: %v", err)
	}
	updated, err := svc.UpdatePlan(ctx, int64(created.ID), UpdatePlanRequest{
		SeatLimit: optionalLimit,
	})
	if err != nil {
		t.Fatalf("UpdatePlan returned error: %v", err)
	}
	if updated.SeatLimit == nil || *updated.SeatLimit != 55 {
		t.Fatalf("updated.SeatLimit = %v, want 55", updated.SeatLimit)
	}
	if updated.VirtualSeatStart == nil || *updated.VirtualSeatStart != 0 {
		t.Fatalf("updated.VirtualSeatStart = %v, want 0", updated.VirtualSeatStart)
	}
	if updated.VirtualSeatTotal == nil || *updated.VirtualSeatTotal != 55 {
		t.Fatalf("updated.VirtualSeatTotal = %v, want 55", updated.VirtualSeatTotal)
	}
}

func TestPaymentConfigServicePublicPlansForSaleCachesAndInvalidates(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}

	seatLimit := 100
	start := 4900
	initialTotal := 5000
	plan, err := svc.CreatePlan(ctx, CreatePlanRequest{
		Name:             "Pro monthly",
		Description:      "Primary development",
		Price:            799,
		ValidityDays:     30,
		ValidityUnit:     "day",
		Features:         "Feature one\nFeature two",
		ProductName:      "LINX2 Pro monthly",
		ForSale:          true,
		SortOrder:        30,
		SeatLimit:        &seatLimit,
		VirtualSeatStart: &start,
		VirtualSeatTotal: &initialTotal,
	})
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}
	user := client.User.Create().SetEmail("seat-cache@example.com").SetPasswordHash("hash").SaveX(ctx)
	client.UserSubscription.Create().
		SetUserID(user.ID).
		SetPlanID(plan.ID).
		SetStartsAt(time.Now().Add(-time.Hour)).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetStatus(SubscriptionStatusActive).
		SaveX(ctx)

	first, err := svc.PublicPlansForSale(ctx)
	if err != nil {
		t.Fatalf("PublicPlansForSale returned error: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("PublicPlansForSale len = %d, want 1", len(first))
	}
	if first[0].SeatUsed != 1 {
		t.Fatalf("first SeatUsed = %d, want 1", first[0].SeatUsed)
	}
	if first[0].VirtualSeatTotal == nil || *first[0].VirtualSeatTotal != 5000 {
		t.Fatalf("first VirtualSeatTotal = %v, want 5000", first[0].VirtualSeatTotal)
	}
	first[0].Features[0] = "mutated"
	if first[0].SeatLimit != nil {
		*first[0].SeatLimit = 1
	}

	updatedTotal := 5001
	if _, err := client.SubscriptionPlan.UpdateOneID(plan.ID).SetSeatLimit(101).SetVirtualSeatTotal(updatedTotal).Save(ctx); err != nil {
		t.Fatalf("direct update plan: %v", err)
	}

	cached, err := svc.PublicPlansForSale(ctx)
	if err != nil {
		t.Fatalf("PublicPlansForSale cached returned error: %v", err)
	}
	if cached[0].Features[0] != "Feature one" {
		t.Fatalf("cached features were mutated: %v", cached[0].Features)
	}
	if cached[0].SeatLimit == nil || *cached[0].SeatLimit != 100 {
		t.Fatalf("cached SeatLimit = %v, want original 100", cached[0].SeatLimit)
	}
	if cached[0].VirtualSeatTotal == nil || *cached[0].VirtualSeatTotal != 5000 {
		t.Fatalf("cached VirtualSeatTotal = %v, want cached 5000", cached[0].VirtualSeatTotal)
	}

	svc.InvalidatePublicPlansCache()
	reloaded, err := svc.PublicPlansForSale(ctx)
	if err != nil {
		t.Fatalf("PublicPlansForSale after invalidate returned error: %v", err)
	}
	if reloaded[0].SeatLimit == nil || *reloaded[0].SeatLimit != 101 {
		t.Fatalf("reloaded SeatLimit = %v, want 101", reloaded[0].SeatLimit)
	}
	if reloaded[0].VirtualSeatTotal == nil || *reloaded[0].VirtualSeatTotal != 5001 {
		t.Fatalf("reloaded VirtualSeatTotal = %v, want 5001", reloaded[0].VirtualSeatTotal)
	}
}

func TestPaymentConfigServicePlanSevenDayQuota(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}

	quota := 110.0

	created, err := svc.CreatePlan(ctx, CreatePlanRequest{
		Name:             "Plus monthly",
		Description:      "Everyday development",
		Price:            399,
		ValidityDays:     30,
		ValidityUnit:     "day",
		Features:         "Seven-day quota\nRecharge fallback",
		ProductName:      "LINX2 Plus monthly",
		ForSale:          true,
		SortOrder:        20,
		SevenDayQuotaUSD: &quota,
	})
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}
	if created.SevenDayQuotaUsd == nil || math.Abs(*created.SevenDayQuotaUsd-110.0) > 0.000001 {
		t.Fatalf("created.SevenDayQuotaUsd = %v, want 110", created.SevenDayQuotaUsd)
	}

	updatedQuota := 260.0
	updated, err := svc.UpdatePlan(ctx, int64(created.ID), UpdatePlanRequest{
		SevenDayQuotaUSD: &updatedQuota,
	})
	if err != nil {
		t.Fatalf("UpdatePlan returned error: %v", err)
	}
	if updated.SevenDayQuotaUsd == nil || math.Abs(*updated.SevenDayQuotaUsd-260.0) > 0.000001 {
		t.Fatalf("updated.SevenDayQuotaUsd = %v, want 260", updated.SevenDayQuotaUsd)
	}

	listed, err := svc.ListPlansForSale(ctx)
	if err != nil {
		t.Fatalf("ListPlansForSale returned error: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListPlansForSale len = %d, want 1", len(listed))
	}
	if listed[0].SevenDayQuotaUsd == nil || math.Abs(*listed[0].SevenDayQuotaUsd-260.0) > 0.000001 {
		t.Fatalf("listed[0].SevenDayQuotaUsd = %v, want 260", listed[0].SevenDayQuotaUsd)
	}

	cleared, err := svc.UpdatePlan(ctx, int64(created.ID), UpdatePlanRequest{
		ClearSevenDayQuotaUSD: true,
	})
	if err != nil {
		t.Fatalf("UpdatePlan clear quota returned error: %v", err)
	}
	if cleared.SevenDayQuotaUsd != nil {
		t.Fatalf("cleared.SevenDayQuotaUsd = %v, want nil", cleared.SevenDayQuotaUsd)
	}
}

func TestPaymentConfigServicePlanSevenDayQuotaRejectsInvalidValues(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}

	negativeQuota := -1.0
	_, err := svc.CreatePlan(ctx, CreatePlanRequest{
		Name:             "Invalid quota plan",
		Description:      "Negative quotas are not allowed",
		Price:            100,
		ValidityDays:     30,
		ValidityUnit:     "day",
		Features:         "Feature",
		ProductName:      "Invalid quota plan",
		ForSale:          true,
		SortOrder:        30,
		SevenDayQuotaUSD: &negativeQuota,
	})
	if err == nil {
		t.Fatal("CreatePlan with negative SevenDayQuotaUSD returned nil error")
	}

	quota := 25.0
	created, err := svc.CreatePlan(ctx, CreatePlanRequest{
		Name:             "Valid quota plan",
		Description:      "Valid quota",
		Price:            100,
		ValidityDays:     30,
		ValidityUnit:     "day",
		Features:         "Feature",
		ProductName:      "Valid quota plan",
		ForSale:          true,
		SortOrder:        31,
		SevenDayQuotaUSD: &quota,
	})
	if err != nil {
		t.Fatalf("CreatePlan returned error: %v", err)
	}

	_, err = svc.UpdatePlan(ctx, int64(created.ID), UpdatePlanRequest{
		SevenDayQuotaUSD: &negativeQuota,
	})
	if err == nil {
		t.Fatal("UpdatePlan with negative SevenDayQuotaUSD returned nil error")
	}

	replacementQuota := 50.0
	_, err = svc.UpdatePlan(ctx, int64(created.ID), UpdatePlanRequest{
		SevenDayQuotaUSD:      &replacementQuota,
		ClearSevenDayQuotaUSD: true,
	})
	if err == nil {
		t.Fatal("UpdatePlan with both SevenDayQuotaUSD and ClearSevenDayQuotaUSD returned nil error")
	}

	reloaded, err := svc.GetPlan(ctx, int64(created.ID))
	if err != nil {
		t.Fatalf("GetPlan returned error: %v", err)
	}
	if reloaded.SevenDayQuotaUsd == nil || math.Abs(*reloaded.SevenDayQuotaUsd-25.0) > 0.000001 {
		t.Fatalf("reloaded.SevenDayQuotaUsd = %v, want 25", reloaded.SevenDayQuotaUsd)
	}
}
