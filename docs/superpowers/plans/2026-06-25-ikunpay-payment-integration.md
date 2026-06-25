# IkunPay Payment Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate IkunPay V2 RSA as a backend provider that can source the existing user-facing Alipay and WeChat payment methods.

**Architecture:** Add a distinct `ikunpay` provider beside EasyPay, keeping V1 MD5 and V2 RSA protocols isolated. Route visible `alipay` and `wxpay` methods through official, EasyPay, or IkunPay provider instances via the existing visible-method source layer. Expose IkunPay in admin provider configuration while masking RSA key material from read APIs.

**Tech Stack:** Go payment providers and services, Gin webhook routes, Ent-backed provider instances, TypeScript/Vue admin UI, Vitest frontend tests, Go unit tests with `httptest`.

---

## File Structure

- Create: `backend/internal/payment/provider/ikunpay.go`
  - Owns IkunPay V2 config validation, RSA signing/verification, form POST requests, response parsing, notification verification, refunds, and order close.
- Create: `backend/internal/payment/provider/ikunpay_signature_test.go`
  - Tests canonical signing content, RSA key parsing, signing, verification, and merchant metadata without any static secret.
- Create: `backend/internal/payment/provider/ikunpay_test.go`
  - Tests provider create/query/notify/refund/cancel behavior against `httptest` servers.
- Modify: `backend/internal/payment/types.go`
  - Adds `payment.TypeIkunPay` and preserves visible-method normalization.
- Modify: `backend/internal/payment/provider/factory.go`
  - Creates `provider.NewIkunPay` for provider key `ikunpay`.
- Modify: `backend/internal/service/payment_config_providers.go`
  - Adds IkunPay to valid provider keys, sensitive fields, and protected pending-order config fields.
- Modify: `backend/internal/service/payment_resume_service.go`
  - Adds `ikunpay_alipay` and `ikunpay_wxpay` source constants, aliases, and provider-key mapping.
- Modify: `backend/internal/service/payment_visible_method_instances.go`
  - Treats IkunPay provider instances as enabled sources for visible Alipay/WeChat methods.
- Modify: `backend/internal/service/payment_config_service.go`
  - Adds IkunPay source availability in config responses.
- Modify: `backend/internal/service/payment_order_provider_snapshot.go`
  - Validates IkunPay callback `pid` against the order provider snapshot merchant identity.
- Modify: `backend/internal/service/payment_order.go`
  - Snapshots IkunPay `pid` as the provider merchant identity when creating orders.
- Modify: `backend/internal/handler/payment_webhook_handler.go`
  - Adds `IkunPayNotify` and `out_trade_no` extraction for IkunPay GET/POST form callbacks.
- Modify: `backend/internal/server/routes/payment.go`
  - Registers `GET` and `POST` `/api/v1/payment/webhook/ikunpay`.
- Modify: backend tests under `backend/internal/service`, `backend/internal/handler`, and `backend/internal/server/routes`
  - Covers config masking, source routing, visible-source availability, snapshot validation, webhook extraction/response, and route registration.
- Modify: `frontend/src/types/payment.ts`
  - Adds `ikunpay` to provider key typing where the admin provider UI uses provider keys.
- Modify: `frontend/src/components/payment/providerConfig.ts`
  - Adds IkunPay supported types, callback paths, config fields, and default API base.
- Modify: `frontend/src/components/payment/PaymentProviderDialog.vue`
  - Enables EasyPay-style QR/popup payment modes for IkunPay.
- Modify: `frontend/src/components/payment/ProviderCard.vue`
  - Adds display label mapping for IkunPay provider cards.
- Modify: `frontend/src/api/admin/settings.ts`
  - Adds IkunPay visible-method source options and aliases.
- Modify: `frontend/src/i18n/locales/zh.ts`
  - Adds Chinese labels and field hints for IkunPay.
- Modify: `frontend/src/i18n/locales/en.ts`
  - Adds English labels and field hints for IkunPay.
- Modify: frontend tests under `frontend/src/api/__tests__` and `frontend/src/components/payment/__tests__`
  - Covers provider config, visible source options, source aliases, and provider dialog mode behavior.

## Task 1: Backend Provider Catalog And Config Maps

**Files:**
- Modify: `backend/internal/payment/types.go`
- Modify: `backend/internal/service/payment_config_providers.go`
- Test: `backend/internal/service/payment_config_providers_test.go`

- [ ] **Step 1: Add failing service tests for IkunPay provider-key validation and secret masking**

Append IkunPay cases to the existing table tests in `backend/internal/service/payment_config_providers_test.go`:

```go
{
	name:           "valid ikunpay provider",
	providerKey:    payment.TypeIkunPay,
	providerName:   "IkunPay Provider",
	supportedTypes: "alipay,wxpay",
	wantErr:        false,
},
```

Append these cases to `TestIsSensitiveProviderConfigField`:

```go
// IkunPay
{payment.TypeIkunPay, "merchantPrivateKey", true},
{payment.TypeIkunPay, "platformPublicKey", true},
{payment.TypeIkunPay, "MerchantPrivateKey", true},
{payment.TypeIkunPay, "pid", false},
{payment.TypeIkunPay, "apiBase", false},
```

Add this test near the other provider-config protection tests:

```go
func TestIkunPayProtectedConfigFields(t *testing.T) {
	t.Parallel()

	current := map[string]string{
		"pid":                "merchant-a",
		"merchantPrivateKey": "private-a",
		"platformPublicKey":  "public-a",
		"apiBase":            "https://ikunpay.example",
		"notifyUrl":          "https://merchant.example/notify",
		"returnUrl":          "https://merchant.example/return",
	}
	next := map[string]string{
		"pid":                "merchant-b",
		"merchantPrivateKey": "private-a",
		"platformPublicKey":  "public-a",
		"apiBase":            "https://ikunpay.example",
		"notifyUrl":          "https://merchant.example/notify",
		"returnUrl":          "https://merchant.example/return",
	}

	require.True(t, hasPendingOrderProtectedConfigChange(payment.TypeIkunPay, current, next))

	next["pid"] = "merchant-a"
	next["notifyUrl"] = "https://merchant.example/notify-v2"
	require.False(t, hasPendingOrderProtectedConfigChange(payment.TypeIkunPay, current, next))
}
```

- [ ] **Step 2: Run the focused failing backend test**

Run:

```bash
cd backend && go test -tags=unit ./internal/service -run 'TestValidateProviderRequest|TestIsSensitiveProviderConfigField|TestIkunPayProtectedConfigFields'
```

Expected: FAIL with `undefined: payment.TypeIkunPay`.

- [ ] **Step 3: Add the IkunPay payment type constant**

In `backend/internal/payment/types.go`, add the new provider key beside `TypeEasyPay`:

```go
TypeEasyPay   PaymentType = "easypay"
TypeIkunPay   PaymentType = "ikunpay"
TypeAirwallex PaymentType = "airwallex"
```

Update `GetBasePaymentType` to preserve `ikunpay` as a provider identity:

```go
case t == TypeEasyPay:
	return TypeEasyPay
case t == TypeIkunPay:
	return TypeIkunPay
case t == TypeAirwallex:
	return TypeAirwallex
```

- [ ] **Step 4: Add IkunPay to backend config field maps**

In `backend/internal/service/payment_config_providers.go`, update the maps:

```go
var providerSensitiveConfigFields = map[string]map[string]struct{}{
	payment.TypeEasyPay:   {"pkey": {}},
	payment.TypeIkunPay:   {"merchantprivatekey": {}, "platformpublickey": {}},
	payment.TypeAlipay:    {"privatekey": {}, "publickey": {}, "alipaypublickey": {}},
	payment.TypeWxpay:     {"privatekey": {}, "apiv3key": {}, "publickey": {}},
	payment.TypeStripe:    {"secretkey": {}, "webhooksecret": {}},
	payment.TypeAirwallex: {"apikey": {}, "webhooksecret": {}},
}

var providerPendingOrderProtectedConfigFields = map[string]map[string]struct{}{
	payment.TypeEasyPay:   {"pkey": {}, "pid": {}},
	payment.TypeIkunPay:   {"merchantprivatekey": {}, "platformpublickey": {}, "pid": {}, "apibase": {}},
	payment.TypeAlipay:    {"privatekey": {}, "publickey": {}, "alipaypublickey": {}, "appid": {}},
	payment.TypeWxpay:     {"privatekey": {}, "apiv3key": {}, "publickey": {}, "appid": {}, "mpappid": {}, "mchid": {}, "publickeyid": {}, "certserial": {}},
	payment.TypeStripe:    {"secretkey": {}, "webhooksecret": {}, "currency": {}},
	payment.TypeAirwallex: {"clientid": {}, "apikey": {}, "webhooksecret": {}, "apibase": {}, "accountid": {}, "currency": {}},
}
```

Update `validProviderKeys` to include IkunPay:

```go
var validProviderKeys = map[string]bool{
	payment.TypeEasyPay: true, payment.TypeIkunPay: true, payment.TypeAlipay: true, payment.TypeWxpay: true, payment.TypeStripe: true, payment.TypeAirwallex: true,
}
```

- [ ] **Step 5: Run focused tests and commit**

Run:

```bash
cd backend && go test -tags=unit ./internal/service -run 'TestValidateProviderRequest|TestIsSensitiveProviderConfigField|TestIkunPayProtectedConfigFields'
```

Expected: PASS.

Commit:

```bash
git add backend/internal/payment/types.go backend/internal/service/payment_config_providers.go backend/internal/service/payment_config_providers_test.go
git commit -m "feat: add ikunpay provider catalog entries"
```

## Task 2: IkunPay RSA Signature Helpers

**Files:**
- Create: `backend/internal/payment/provider/ikunpay.go`
- Create: `backend/internal/payment/provider/ikunpay_signature_test.go`

- [ ] **Step 1: Write failing RSA helper tests**

Create `backend/internal/payment/provider/ikunpay_signature_test.go`:

```go
package provider

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
)

func TestIkunPaySignContentExcludesSignTypeSignAndEmptyValues(t *testing.T) {
	t.Parallel()

	params := map[string]string{
		"pid":          "merchant-1",
		"type":         "alipay",
		"out_trade_no": "order-1",
		"money":        "10.00",
		"device":       "",
		"sign":         "ignored",
		"sign_type":    "RSA",
	}

	got := ikunPaySignContent(params)
	want := "money=10.00&out_trade_no=order-1&pid=merchant-1&type=alipay"
	if got != want {
		t.Fatalf("sign content = %q, want %q", got, want)
	}
}

func TestIkunPaySignAndVerifyAcceptPEMAndBareBase64Keys(t *testing.T) {
	t.Parallel()

	key := generateIkunPayTestKey(t)
	privateDER := x509.MarshalPKCS1PrivateKey(key)
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privateDER}))
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	privateBare := base64.StdEncoding.EncodeToString(privateDER)
	publicBare := base64.StdEncoding.EncodeToString(publicDER)

	params := map[string]string{
		"pid":          "merchant-1",
		"out_trade_no": "order-1",
		"money":        "10.00",
	}

	signature, err := ikunPaySign(params, privatePEM)
	if err != nil {
		t.Fatalf("ikunPaySign PEM: %v", err)
	}
	if signature == "" {
		t.Fatal("signature is empty")
	}
	if err := ikunPayVerify(params, publicPEM, signature); err != nil {
		t.Fatalf("ikunPayVerify PEM: %v", err)
	}
	if err := ikunPayVerify(params, publicBare, signature); err != nil {
		t.Fatalf("ikunPayVerify bare public key: %v", err)
	}
	signatureBare, err := ikunPaySign(params, privateBare)
	if err != nil {
		t.Fatalf("ikunPaySign bare private key: %v", err)
	}
	if err := ikunPayVerify(params, publicPEM, signatureBare); err != nil {
		t.Fatalf("ikunPayVerify bare signature: %v", err)
	}

	params["money"] = "11.00"
	if err := ikunPayVerify(params, publicPEM, signature); err == nil {
		t.Fatal("tampered params verified successfully")
	}
}

func TestIkunPayMerchantIdentityMetadata(t *testing.T) {
	t.Parallel()

	provider := &IkunPay{config: map[string]string{"pid": "merchant-1"}}
	got := provider.MerchantIdentityMetadata()
	if got["pid"] != "merchant-1" {
		t.Fatalf("pid = %q, want merchant-1", got["pid"])
	}
}

func generateIkunPayTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return key
}
```

- [ ] **Step 2: Run helper tests to verify they fail**

Run:

```bash
cd backend && go test ./internal/payment/provider -run 'TestIkunPaySign|TestIkunPayMerchantIdentity'
```

Expected: FAIL with `undefined: ikunPaySignContent` and `undefined: IkunPay`.

- [ ] **Step 3: Add IkunPay struct, constructor, RSA helpers, and identity metadata**

Create `backend/internal/payment/provider/ikunpay.go` with this starting implementation:

```go
package provider

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

const (
	ikunpayDefaultAPIBase  = "https://ikunpay.com"
	ikunpaySignTypeRSA    = "RSA"
	ikunpayCodeSuccess    = 0
	ikunpayHTTPTimeout    = 10 * time.Second
	maxIkunPayResponseSize = 1 << 20
)

type IkunPay struct {
	instanceID string
	config     map[string]string
	apiBase    string
	httpClient *http.Client
}

func NewIkunPay(instanceID string, config map[string]string) (*IkunPay, error) {
	for _, key := range []string{"pid", "merchantPrivateKey", "platformPublicKey", "notifyUrl", "returnUrl"} {
		if strings.TrimSpace(config[key]) == "" {
			return nil, fmt.Errorf("ikunpay config missing required key: %s", key)
		}
	}
	if _, err := parseIkunPayPrivateKey(config["merchantPrivateKey"]); err != nil {
		return nil, fmt.Errorf("ikunpay merchantPrivateKey invalid: %w", err)
	}
	if _, err := parseIkunPayPublicKey(config["platformPublicKey"]); err != nil {
		return nil, fmt.Errorf("ikunpay platformPublicKey invalid: %w", err)
	}
	apiBase := normalizeIkunPayAPIBase(config["apiBase"])
	if apiBase == "" {
		apiBase = ikunpayDefaultAPIBase
	}
	return &IkunPay{
		instanceID: instanceID,
		config:     config,
		apiBase:    apiBase,
		httpClient: &http.Client{Timeout: ikunpayHTTPTimeout},
	}, nil
}

func (i *IkunPay) Name() string { return "IkunPay" }
func (i *IkunPay) ProviderKey() string { return payment.TypeIkunPay }
func (i *IkunPay) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeAlipay, payment.TypeWxpay}
}

func (i *IkunPay) MerchantIdentityMetadata() map[string]string {
	return map[string]string{"pid": strings.TrimSpace(i.config["pid"])}
}

func normalizeIkunPayAPIBase(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	for _, suffix := range []string{"/api/pay/create", "/api/pay/query", "/api/pay/refund", "/api/pay/refundquery", "/api/pay/close"} {
		if strings.HasSuffix(parsed.Path, suffix) {
			parsed.Path = strings.TrimSuffix(parsed.Path, suffix)
			break
		}
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func ikunPaySignContent(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key, value := range params {
		key = strings.TrimSpace(key)
		if key == "" || key == "sign" || key == "sign_type" || strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params[key])
	}
	return strings.Join(parts, "&")
}

func ikunPaySign(params map[string]string, privateKeyText string) (string, error) {
	privateKey, err := parseIkunPayPrivateKey(privateKeyText)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(ikunPaySignContent(params)))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

func ikunPayVerify(params map[string]string, publicKeyText string, signatureText string) error {
	publicKey, err := parseIkunPayPublicKey(publicKeyText)
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signatureText))
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	digest := sha256.Sum256([]byte(ikunPaySignContent(params)))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return fmt.Errorf("verify signature: %w", err)
	}
	return nil
}

func parseIkunPayPrivateKey(raw string) (*rsa.PrivateKey, error) {
	der, err := decodeIkunPayKey(raw)
	if err != nil {
		return nil, err
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA")
	}
	return key, nil
}

func parseIkunPayPublicKey(raw string) (*rsa.PublicKey, error) {
	der, err := decodeIkunPayKey(raw)
	if err != nil {
		return nil, err
	}
	if key, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not RSA")
	}
	return key, nil
}

func decodeIkunPayKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty key")
	}
	if block, _ := pem.Decode([]byte(raw)); block != nil {
		return block.Bytes, nil
	}
	compact := strings.Join(strings.Fields(raw), "")
	der, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("decode key: %w", err)
	}
	return der, nil
}
```

Task 3 adds the `encoding/json`, `io`, and `strconv` imports when HTTP response parsing is introduced.

- [ ] **Step 4: Add temporary compile-safe provider methods**

Append these methods to `backend/internal/payment/provider/ikunpay.go` so the file compiles before HTTP behavior is added:

```go
func (i *IkunPay) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	_, _ = ctx, req
	return nil, fmt.Errorf("ikunpay create payment is unavailable")
}

func (i *IkunPay) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	_, _ = ctx, tradeNo
	return nil, fmt.Errorf("ikunpay query order is unavailable")
}

func (i *IkunPay) VerifyNotification(ctx context.Context, rawBody string, headers map[string]string) (*payment.PaymentNotification, error) {
	_, _, _ = ctx, rawBody, headers
	return nil, fmt.Errorf("ikunpay notification verification is unavailable")
}

func (i *IkunPay) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	_, _ = ctx, req
	return nil, fmt.Errorf("ikunpay refund is unavailable")
}

func (i *IkunPay) CancelPayment(ctx context.Context, tradeNo string) error {
	_, _ = ctx, tradeNo
	return fmt.Errorf("ikunpay close order is unavailable")
}
```

- [ ] **Step 5: Run helper tests and commit**

Run:

```bash
cd backend && go test ./internal/payment/provider -run 'TestIkunPaySign|TestIkunPayMerchantIdentity'
```

Expected: PASS.

Commit:

```bash
git add backend/internal/payment/provider/ikunpay.go backend/internal/payment/provider/ikunpay_signature_test.go
git commit -m "feat: add ikunpay rsa signing helpers"
```

## Task 3: IkunPay HTTP Provider Behavior

**Files:**
- Modify: `backend/internal/payment/provider/ikunpay.go`
- Modify: `backend/internal/payment/provider/factory.go`
- Create: `backend/internal/payment/provider/ikunpay_test.go`

- [ ] **Step 1: Write failing constructor and factory tests**

Add to `backend/internal/payment/provider/ikunpay_test.go`:

```go
package provider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

func TestNewIkunPayDefaultsAPIBaseAndFactoryCreatesProvider(t *testing.T) {
	t.Parallel()

	cfg := newIkunPayTestConfig(t, "https://ikunpay.example/api/pay/create")
	provider, err := NewIkunPay("inst-1", cfg)
	if err != nil {
		t.Fatalf("NewIkunPay: %v", err)
	}
	if provider.apiBase != "https://ikunpay.example" {
		t.Fatalf("apiBase = %q, want normalized base", provider.apiBase)
	}
	created, err := CreateProvider(payment.TypeIkunPay, "inst-1", cfg)
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	if created.ProviderKey() != payment.TypeIkunPay {
		t.Fatalf("provider key = %q", created.ProviderKey())
	}
}
```

- [ ] **Step 2: Run constructor test to verify it fails at factory**

Run:

```bash
cd backend && go test ./internal/payment/provider -run TestNewIkunPayDefaultsAPIBaseAndFactoryCreatesProvider
```

Expected: FAIL with `unknown provider key: ikunpay`.

- [ ] **Step 3: Add IkunPay to provider factory**

In `backend/internal/payment/provider/factory.go`, add:

```go
case payment.TypeIkunPay:
	return NewIkunPay(instanceID, config)
```

- [ ] **Step 4: Write failing create-payment tests**

Append to `backend/internal/payment/provider/ikunpay_test.go`:

```go
func TestIkunPayCreatePaymentPostsSignedFormAndMapsQRCode(t *testing.T) {
	t.Parallel()

	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pay/create" {
			t.Fatalf("path = %q, want /api/pay/create", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		if err := ikunPayVerify(formValuesToMap(gotForm), ikunPayTestMerchantPublicKey(t), gotForm.Get("sign")); err != nil {
			t.Fatalf("request signature invalid: %v", err)
		}
		writeIkunPayJSON(t, w, map[string]string{
			"code":       "0",
			"msg":        "ok",
			"trade_no":   "upstream-1",
			"pay_type":   "qrcode",
			"pay_info":   "https://qr.example/order-1",
			"timestamp":  "1780000000",
			"sign_type":  "RSA",
		})
	}))
	defer server.Close()

	provider := newTestIkunPay(t, server.URL, "qrcode")
	resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "order-1",
		Amount:      "10.00",
		PaymentType: payment.TypeAlipay,
		Subject:     "Test Product",
		NotifyURL:   "https://merchant.example/notify",
		ReturnURL:   "https://merchant.example/return",
		ClientIP:    "203.0.113.1",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.TradeNo != "upstream-1" || resp.QRCode != "https://qr.example/order-1" || resp.PayURL != "" {
		t.Fatalf("response = %+v, want qrcode mapping", resp)
	}
	for key, want := range map[string]string{
		"pid":          "merchant-1",
		"type":         payment.TypeAlipay,
		"out_trade_no": "order-1",
		"name":         "Test Product",
		"money":        "10.00",
		"method":       "web",
		"device":       "pc",
		"notify_url":   "https://merchant.example/notify",
		"return_url":   "https://merchant.example/return",
		"clientip":     "203.0.113.1",
		"sign_type":    "RSA",
	} {
		if got := gotForm.Get(key); got != want {
			t.Fatalf("form[%s] = %q, want %q (form=%v)", key, got, want, gotForm)
		}
	}
}

func TestIkunPayCreatePaymentPopupMapsJumpToPayURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.PostForm.Get("method"); got != "jump" {
			t.Fatalf("method = %q, want jump", got)
		}
		writeIkunPayJSON(t, w, map[string]string{
			"code":       "0",
			"msg":        "ok",
			"trade_no":   "upstream-2",
			"pay_type":   "jump",
			"pay_info":   "https://ikunpay.example/pay/upstream-2",
			"timestamp":  "1780000000",
			"sign_type":  "RSA",
		})
	}))
	defer server.Close()

	provider := newTestIkunPay(t, server.URL, "popup")
	resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "order-2",
		Amount:      "12.00",
		PaymentType: payment.TypeWxpay,
		Subject:     "Popup Product",
		NotifyURL:   "https://merchant.example/notify",
		ReturnURL:   "https://merchant.example/return",
		IsMobile:    true,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PayURL != "https://ikunpay.example/pay/upstream-2" || resp.QRCode != "" {
		t.Fatalf("response = %+v, want pay url mapping", resp)
	}
}
```

- [ ] **Step 5: Replace temporary CreatePayment with real create behavior**

In `backend/internal/payment/provider/ikunpay.go`, replace the temporary `CreatePayment` method with:

```go
func (i *IkunPay) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	if req.PaymentType != payment.TypeAlipay && req.PaymentType != payment.TypeWxpay {
		return nil, fmt.Errorf("ikunpay unsupported payment type: %s", req.PaymentType)
	}
	method := "web"
	if strings.EqualFold(strings.TrimSpace(i.config["paymentMode"]), "popup") || strings.EqualFold(strings.TrimSpace(i.config["paymentMode"]), "redirect") {
		method = "jump"
	}
	device := "pc"
	if req.IsMobile {
		device = "mobile"
	}
	params := map[string]string{
		"pid":          i.config["pid"],
		"method":       method,
		"device":       device,
		"type":         req.PaymentType,
		"out_trade_no": req.OrderID,
		"notify_url":   firstNonEmpty(req.NotifyURL, i.config["notifyUrl"]),
		"return_url":   firstNonEmpty(req.ReturnURL, i.config["returnUrl"]),
		"name":         req.Subject,
		"money":        req.Amount,
		"clientip":     req.ClientIP,
		"timestamp":    strconv.FormatInt(time.Now().Unix(), 10),
		"sign_type":    ikunpaySignTypeRSA,
	}
	if err := i.signParams(params); err != nil {
		return nil, fmt.Errorf("ikunpay sign create: %w", err)
	}
	var resp ikunPayCreateResponse
	if err := i.postForm(ctx, "/api/pay/create", params, &resp); err != nil {
		return nil, fmt.Errorf("ikunpay create: %w", err)
	}
	if resp.Code != ikunpayCodeSuccess {
		return nil, fmt.Errorf("ikunpay error: %s", firstNonEmpty(resp.Msg, resp.Message))
	}
	if err := i.verifyResponseSignature(resp.Signable()); err != nil {
		return nil, fmt.Errorf("ikunpay verify create response: %w", err)
	}
	result := &payment.CreatePaymentResponse{TradeNo: resp.TradeNo}
	switch strings.ToLower(strings.TrimSpace(resp.PayType)) {
	case "qrcode":
		result.QRCode = strings.TrimSpace(resp.PayInfo)
	case "jump", "urlscheme":
		result.PayURL = strings.TrimSpace(resp.PayInfo)
	case "html":
		if strings.HasPrefix(strings.TrimSpace(resp.PayInfo), "http://") || strings.HasPrefix(strings.TrimSpace(resp.PayInfo), "https://") {
			result.PayURL = strings.TrimSpace(resp.PayInfo)
		} else {
			return nil, fmt.Errorf("ikunpay unsupported html pay_info response")
		}
	default:
		return nil, fmt.Errorf("ikunpay unsupported pay_type: %s", resp.PayType)
	}
	return result, nil
}
```

Add the response and helper types in the same file:

```go
type ikunPayCreateResponse struct {
	Code     int    `json:"code"`
	Msg      string `json:"msg"`
	Message  string `json:"message"`
	TradeNo  string `json:"trade_no"`
	PayType  string `json:"pay_type"`
	PayInfo  string `json:"pay_info"`
	Timestamp string `json:"timestamp"`
	Sign     string `json:"sign"`
	SignType string `json:"sign_type"`
}

func (r ikunPayCreateResponse) Signable() map[string]string {
	return map[string]string{
		"code":      strconv.Itoa(r.Code),
		"msg":       r.Msg,
		"message":   r.Message,
		"trade_no":  r.TradeNo,
		"pay_type":  r.PayType,
		"pay_info":  r.PayInfo,
		"timestamp": r.Timestamp,
		"sign_type": r.SignType,
		"sign":      r.Sign,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (i *IkunPay) signParams(params map[string]string) error {
	signature, err := ikunPaySign(params, i.config["merchantPrivateKey"])
	if err != nil {
		return err
	}
	params["sign"] = signature
	return nil
}

func (i *IkunPay) verifyResponseSignature(params map[string]string) error {
	signature := strings.TrimSpace(params["sign"])
	if signature == "" {
		return nil
	}
	return ikunPayVerify(params, i.config["platformPublicKey"], signature)
}

func (i *IkunPay) postForm(ctx context.Context, path string, params map[string]string, out any) error {
	form := url.Values{}
	for key, value := range params {
		if strings.TrimSpace(value) != "" {
			form.Set(key, value)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, i.apiBase+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := i.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIkunPayResponseSize))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, summarizeIkunPayBody(body))
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return fmt.Errorf("empty response")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("parse JSON: %w: %s", err, summarizeIkunPayBody(body))
	}
	return nil
}

func summarizeIkunPayBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "<empty>"
	}
	if len(text) > 200 {
		return text[:200] + "...(truncated)"
	}
	return text
}
```

- [ ] **Step 6: Write failing query/refund/cancel/notify tests**

Append to `backend/internal/payment/provider/ikunpay_test.go`:

```go
func TestIkunPayQueryOrderMapsStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     string
		wantStatus string
	}{
		{name: "pending", status: "0", wantStatus: payment.ProviderStatusPending},
		{name: "paid", status: "1", wantStatus: payment.ProviderStatusPaid},
		{name: "refunded", status: "2", wantStatus: payment.ProviderStatusRefunded},
		{name: "frozen", status: "3", wantStatus: payment.ProviderStatusFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/pay/query" {
					t.Fatalf("path = %q, want /api/pay/query", r.URL.Path)
				}
				if err := r.ParseForm(); err != nil {
					t.Fatalf("ParseForm: %v", err)
				}
				if got := r.PostForm.Get("out_trade_no"); got != "order-1" {
					t.Fatalf("out_trade_no = %q, want order-1", got)
				}
				writeIkunPayJSON(t, w, map[string]string{
					"code":          "0",
					"msg":           "ok",
					"trade_no":      "upstream-1",
					"out_trade_no":  "order-1",
					"status":        tt.status,
					"money":         "10.00",
					"timestamp":     "1780000000",
					"sign_type":     "RSA",
				})
			}))
			defer server.Close()

			provider := newTestIkunPay(t, server.URL, "qrcode")
			resp, err := provider.QueryOrder(context.Background(), "order-1")
			if err != nil {
				t.Fatalf("QueryOrder: %v", err)
			}
			if resp.Status != tt.wantStatus || resp.TradeNo != "upstream-1" || resp.Amount != 10 {
				t.Fatalf("response = %+v, want status %s", resp, tt.wantStatus)
			}
		})
	}
}

func TestIkunPayVerifyNotificationAcceptsSignedTradeSuccessAndRejectsTampering(t *testing.T) {
	t.Parallel()

	provider := newTestIkunPay(t, "https://ikunpay.example", "qrcode")
	values := url.Values{}
	values.Set("pid", "merchant-1")
	values.Set("trade_no", "upstream-1")
	values.Set("out_trade_no", "order-1")
	values.Set("type", payment.TypeAlipay)
	values.Set("trade_status", "TRADE_SUCCESS")
	values.Set("money", "10.00")
	values.Set("timestamp", "1780000000")
	values.Set("sign_type", "RSA")
	signature, err := ikunPaySign(formValuesToMap(values), ikunPayTestPlatformPrivateKey(t))
	if err != nil {
		t.Fatalf("ikunPaySign: %v", err)
	}
	values.Set("sign", signature)

	notification, err := provider.VerifyNotification(context.Background(), values.Encode(), nil)
	if err != nil {
		t.Fatalf("VerifyNotification: %v", err)
	}
	if notification.OrderID != "order-1" || notification.TradeNo != "upstream-1" || notification.Status != payment.NotificationStatusSuccess {
		t.Fatalf("notification = %+v", notification)
	}
	if notification.Metadata["pid"] != "merchant-1" {
		t.Fatalf("metadata pid = %q", notification.Metadata["pid"])
	}

	values.Set("money", "11.00")
	if _, err := provider.VerifyNotification(context.Background(), values.Encode(), nil); err == nil {
		t.Fatal("tampered notification verified successfully")
	}
}

func TestIkunPayRefundAndCancelPostSignedRequests(t *testing.T) {
	t.Parallel()

	var gotPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if err := ikunPayVerify(formValuesToMap(r.PostForm), ikunPayTestMerchantPublicKey(t), r.PostForm.Get("sign")); err != nil {
			t.Fatalf("request signature invalid: %v", err)
		}
		writeIkunPayJSON(t, w, map[string]string{
			"code":       "0",
			"msg":        "ok",
			"refund_no":  "refund-1",
			"timestamp":  "1780000000",
			"sign_type":  "RSA",
		})
	}))
	defer server.Close()

	provider := newTestIkunPay(t, server.URL, "qrcode")
	refundResp, err := provider.Refund(context.Background(), payment.RefundRequest{
		TradeNo: "upstream-1",
		OrderID: "order-1",
		Amount:  "3.50",
		Reason:  "requested",
	})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if refundResp.Status != payment.ProviderStatusSuccess || refundResp.RefundID != "refund-1" {
		t.Fatalf("refund response = %+v", refundResp)
	}
	if err := provider.CancelPayment(context.Background(), "order-1"); err != nil {
		t.Fatalf("CancelPayment: %v", err)
	}
	if strings.Join(gotPaths, ",") != "/api/pay/refund,/api/pay/close" {
		t.Fatalf("paths = %v", gotPaths)
	}
}
```

Add shared test helpers at the bottom of `ikunpay_test.go`:

```go
func newTestIkunPay(t *testing.T, apiBase string, paymentMode string) *IkunPay {
	t.Helper()

	cfg := newIkunPayTestConfig(t, apiBase)
	cfg["paymentMode"] = paymentMode
	provider, err := NewIkunPay("test-instance", cfg)
	if err != nil {
		t.Fatalf("NewIkunPay: %v", err)
	}
	return provider
}

func newIkunPayTestConfig(t *testing.T, apiBase string) map[string]string {
	t.Helper()

	return map[string]string{
		"pid":                "merchant-1",
		"merchantPrivateKey": ikunPayTestMerchantPrivateKey(t),
		"platformPublicKey":  ikunPayTestPlatformPublicKey(t),
		"apiBase":            apiBase,
		"notifyUrl":          "https://merchant.example/notify",
		"returnUrl":          "https://merchant.example/return",
	}
}

func writeIkunPayJSON(t *testing.T, w http.ResponseWriter, fields map[string]string) {
	t.Helper()

	signature, err := ikunPaySign(fields, ikunPayTestPlatformPrivateKey(t))
	if err != nil {
		t.Fatalf("sign response: %v", err)
	}
	fields["sign"] = signature
	w.Header().Set("Content-Type", "application/json")
	payload := make(map[string]any, len(fields))
	for key, value := range fields {
		if key == "code" {
			payload[key] = 0
			continue
		}
		payload[key] = value
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("Encode: %v", err)
	}
}

func formValuesToMap(values url.Values) map[string]string {
	out := make(map[string]string, len(values))
	for key := range values {
		out[key] = values.Get(key)
	}
	return out
}

func ikunPayTestMerchantPrivateKey(t *testing.T) string {
	t.Helper()
	return testRSAPrivateKeyPEM(t, "merchant")
}

func ikunPayTestMerchantPublicKey(t *testing.T) string {
	t.Helper()
	return testRSAPublicKeyPEM(t, "merchant")
}

func ikunPayTestPlatformPrivateKey(t *testing.T) string {
	t.Helper()
	return testRSAPrivateKeyPEM(t, "platform")
}

func ikunPayTestPlatformPublicKey(t *testing.T) string {
	t.Helper()
	return testRSAPublicKeyPEM(t, "platform")
}

var ikunPayTestKeysMu sync.Mutex
var ikunPayTestKeys = map[string]*rsa.PrivateKey{}

func testRSAKey(t *testing.T, name string) *rsa.PrivateKey {
	t.Helper()
	ikunPayTestKeysMu.Lock()
	defer ikunPayTestKeysMu.Unlock()
	if key := ikunPayTestKeys[name]; key != nil {
		return key
	}
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	ikunPayTestKeys[name] = key
	return key
}

func testRSAPrivateKeyPEM(t *testing.T, name string) string {
	t.Helper()
	der := x509.MarshalPKCS1PrivateKey(testRSAKey(t, name))
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

func testRSAPublicKeyPEM(t *testing.T, name string) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&testRSAKey(t, name).PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func testRSAPublicKeyBare(t *testing.T, name string) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&testRSAKey(t, name).PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return base64.StdEncoding.EncodeToString(der)
}
```

- [ ] **Step 7: Replace temporary query/notify/refund/cancel methods**

In `backend/internal/payment/provider/ikunpay.go`, replace the temporary methods with:

```go
func (i *IkunPay) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	params := i.baseParams()
	params["out_trade_no"] = strings.TrimSpace(tradeNo)
	if params["out_trade_no"] == "" {
		return nil, fmt.Errorf("ikunpay query missing order identifier")
	}
	if err := i.signParams(params); err != nil {
		return nil, fmt.Errorf("ikunpay sign query: %w", err)
	}
	var resp ikunPayQueryResponse
	if err := i.postForm(ctx, "/api/pay/query", params, &resp); err != nil {
		return nil, fmt.Errorf("ikunpay query: %w", err)
	}
	if resp.Code != ikunpayCodeSuccess {
		return nil, fmt.Errorf("ikunpay query error: %s", firstNonEmpty(resp.Msg, resp.Message))
	}
	if err := i.verifyResponseSignature(resp.Signable()); err != nil {
		return nil, fmt.Errorf("ikunpay verify query response: %w", err)
	}
	amount, err := strconv.ParseFloat(strings.TrimSpace(resp.Money), 64)
	if err != nil && strings.TrimSpace(resp.Money) != "" {
		return nil, fmt.Errorf("ikunpay query invalid money: %w", err)
	}
	return &payment.QueryOrderResponse{
		TradeNo: resp.TradeNo,
		Status:  ikunPayProviderStatus(resp.Status),
		Amount:  amount,
		Metadata: map[string]string{
			"pid":          i.config["pid"],
			"out_trade_no": resp.OutTradeNo,
			"status":       resp.Status,
		},
	}, nil
}

func (i *IkunPay) VerifyNotification(ctx context.Context, rawBody string, headers map[string]string) (*payment.PaymentNotification, error) {
	_, _ = ctx, headers
	values, err := url.ParseQuery(rawBody)
	if err != nil {
		return nil, fmt.Errorf("ikunpay parse notification: %w", err)
	}
	params := formValuesToStringMap(values)
	if err := ikunPayVerify(params, i.config["platformPublicKey"], params["sign"]); err != nil {
		return nil, fmt.Errorf("ikunpay verify notification: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(params["trade_status"]), "TRADE_SUCCESS") {
		return nil, nil
	}
	amount, err := strconv.ParseFloat(strings.TrimSpace(params["money"]), 64)
	if err != nil {
		return nil, fmt.Errorf("ikunpay notification invalid money: %w", err)
	}
	return &payment.PaymentNotification{
		TradeNo: params["trade_no"],
		OrderID: params["out_trade_no"],
		Amount:  amount,
		Status:  payment.NotificationStatusSuccess,
		RawData: rawBody,
		Metadata: map[string]string{
			"pid":          params["pid"],
			"type":         params["type"],
			"trade_status": params["trade_status"],
		},
	}, nil
}

func (i *IkunPay) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	params := i.baseParams()
	if strings.TrimSpace(req.TradeNo) != "" {
		params["trade_no"] = strings.TrimSpace(req.TradeNo)
	} else if strings.TrimSpace(req.OrderID) != "" {
		params["out_trade_no"] = strings.TrimSpace(req.OrderID)
	} else {
		return nil, fmt.Errorf("ikunpay refund missing order identifier")
	}
	params["money"] = strings.TrimSpace(req.Amount)
	params["out_refund_no"] = "refund-" + firstNonEmpty(req.OrderID, req.TradeNo)
	if err := i.signParams(params); err != nil {
		return nil, fmt.Errorf("ikunpay sign refund: %w", err)
	}
	var resp ikunPayRefundResponse
	if err := i.postForm(ctx, "/api/pay/refund", params, &resp); err != nil {
		return nil, fmt.Errorf("ikunpay refund: %w", err)
	}
	if resp.Code != ikunpayCodeSuccess {
		return nil, fmt.Errorf("ikunpay refund error: %s", firstNonEmpty(resp.Msg, resp.Message))
	}
	if err := i.verifyResponseSignature(resp.Signable()); err != nil {
		return nil, fmt.Errorf("ikunpay verify refund response: %w", err)
	}
	return &payment.RefundResponse{RefundID: firstNonEmpty(resp.RefundNo, params["out_refund_no"], params["trade_no"], params["out_trade_no"]), Status: payment.ProviderStatusSuccess}, nil
}

func (i *IkunPay) CancelPayment(ctx context.Context, tradeNo string) error {
	params := i.baseParams()
	params["out_trade_no"] = strings.TrimSpace(tradeNo)
	if params["out_trade_no"] == "" {
		return fmt.Errorf("ikunpay close missing order identifier")
	}
	if err := i.signParams(params); err != nil {
		return fmt.Errorf("ikunpay sign close: %w", err)
	}
	var resp ikunPayBasicResponse
	if err := i.postForm(ctx, "/api/pay/close", params, &resp); err != nil {
		return fmt.Errorf("ikunpay close: %w", err)
	}
	if resp.Code != ikunpayCodeSuccess {
		return fmt.Errorf("ikunpay close error: %s", firstNonEmpty(resp.Msg, resp.Message))
	}
	return i.verifyResponseSignature(resp.Signable())
}
```

Add supporting response helpers:

```go
type ikunPayQueryResponse struct {
	Code       int    `json:"code"`
	Msg        string `json:"msg"`
	Message    string `json:"message"`
	TradeNo    string `json:"trade_no"`
	OutTradeNo string `json:"out_trade_no"`
	Status     string `json:"status"`
	Money      string `json:"money"`
	Timestamp  string `json:"timestamp"`
	Sign       string `json:"sign"`
	SignType   string `json:"sign_type"`
}

func (r ikunPayQueryResponse) Signable() map[string]string {
	return map[string]string{
		"code":         strconv.Itoa(r.Code),
		"msg":          r.Msg,
		"message":      r.Message,
		"trade_no":     r.TradeNo,
		"out_trade_no": r.OutTradeNo,
		"status":       r.Status,
		"money":        r.Money,
		"timestamp":    r.Timestamp,
		"sign_type":    r.SignType,
		"sign":         r.Sign,
	}
}

type ikunPayRefundResponse struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	Message   string `json:"message"`
	RefundNo  string `json:"refund_no"`
	Timestamp string `json:"timestamp"`
	Sign      string `json:"sign"`
	SignType  string `json:"sign_type"`
}

func (r ikunPayRefundResponse) Signable() map[string]string {
	return map[string]string{
		"code":      strconv.Itoa(r.Code),
		"msg":       r.Msg,
		"message":   r.Message,
		"refund_no": r.RefundNo,
		"timestamp": r.Timestamp,
		"sign_type": r.SignType,
		"sign":      r.Sign,
	}
}

type ikunPayBasicResponse struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
	Sign      string `json:"sign"`
	SignType  string `json:"sign_type"`
}

func (r ikunPayBasicResponse) Signable() map[string]string {
	return map[string]string{
		"code":      strconv.Itoa(r.Code),
		"msg":       r.Msg,
		"message":   r.Message,
		"timestamp": r.Timestamp,
		"sign_type": r.SignType,
		"sign":      r.Sign,
	}
}

func (i *IkunPay) baseParams() map[string]string {
	return map[string]string{
		"pid":       i.config["pid"],
		"timestamp": strconv.FormatInt(time.Now().Unix(), 10),
		"sign_type": ikunpaySignTypeRSA,
	}
}

func ikunPayProviderStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "0":
		return payment.ProviderStatusPending
	case "1":
		return payment.ProviderStatusPaid
	case "2":
		return payment.ProviderStatusRefunded
	default:
		return payment.ProviderStatusFailed
	}
}

func formValuesToStringMap(values url.Values) map[string]string {
	params := make(map[string]string, len(values))
	for key := range values {
		params[key] = values.Get(key)
	}
	return params
}
```

- [ ] **Step 8: Run provider tests and commit**

Run:

```bash
cd backend && go test ./internal/payment/provider -run 'TestIkunPay'
```

Expected: PASS.

Run:

```bash
cd backend && go test ./internal/payment/provider
```

Expected: PASS.

Commit:

```bash
git add backend/internal/payment/provider/factory.go backend/internal/payment/provider/ikunpay.go backend/internal/payment/provider/ikunpay_test.go
git commit -m "feat: implement ikunpay provider"
```

## Task 4: Backend Webhook Route And Handler

**Files:**
- Modify: `backend/internal/handler/payment_webhook_handler.go`
- Modify: `backend/internal/handler/payment_webhook_handler_test.go`
- Modify: `backend/internal/server/routes/payment.go`
- Test: `backend/internal/server/routes/payment_public_plans_test.go`

- [ ] **Step 1: Write failing handler tests for IkunPay success response and order extraction**

In `backend/internal/handler/payment_webhook_handler_test.go`, add cases beside existing EasyPay form/query cases:

```go
{
	name:            "ikunpay returns plain text success",
	providerKey:     payment.TypeIkunPay,
	wantStatus:      http.StatusOK,
	wantBody:        "success",
	wantContentType: "text/plain",
},
```

Add a direct extraction test:

```go
func TestExtractOutTradeNoIkunPayQueryPayload(t *testing.T) {
	t.Parallel()

	got := extractOutTradeNo("pid=merchant-1&out_trade_no=order-1&trade_no=upstream-1", payment.TypeIkunPay)
	if got != "order-1" {
		t.Fatalf("out_trade_no = %q, want order-1", got)
	}
}
```

- [ ] **Step 2: Run handler tests and confirm failure**

Run:

```bash
cd backend && go test ./internal/handler -run 'TestWriteSuccessResponse|TestExtractOutTradeNoIkunPay'
```

Expected: FAIL with empty `out_trade_no` or `undefined: payment.TypeIkunPay` if Task 1 has not been applied in this execution session.

- [ ] **Step 3: Add IkunPay notify method and extraction**

In `backend/internal/handler/payment_webhook_handler.go`, add:

```go
// IkunPayNotify handles IkunPay payment notifications.
// GET /api/v1/payment/webhook/ikunpay
func (h *PaymentWebhookHandler) IkunPayNotify(c *gin.Context) {
	h.handleNotify(c, payment.TypeIkunPay)
}
```

Update `extractOutTradeNo`:

```go
case payment.TypeEasyPay, payment.TypeIkunPay, payment.TypeAlipay:
	values, err := url.ParseQuery(rawBody)
	if err == nil {
		return values.Get("out_trade_no")
	}
```

- [ ] **Step 4: Write failing route registration test**

Append this test to `backend/internal/server/routes/payment_public_plans_test.go`, which already imports `net/http`, `gin`, `handler`, and `adminhandler`:

```go
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
```

- [ ] **Step 5: Register IkunPay webhook route**

In `backend/internal/server/routes/payment.go`, add:

```go
webhook.GET("/ikunpay", webhookHandler.IkunPayNotify)
webhook.POST("/ikunpay", webhookHandler.IkunPayNotify)
```

- [ ] **Step 6: Run route and handler tests, then commit**

Run:

```bash
cd backend && go test ./internal/handler ./internal/server/routes -run 'IkunPay|RegisterPaymentRoutesIncludesIkunPayWebhook'
```

Expected: PASS.

Commit:

```bash
git add backend/internal/handler/payment_webhook_handler.go backend/internal/handler/payment_webhook_handler_test.go backend/internal/server/routes/payment.go backend/internal/server/routes/payment_public_plans_test.go
git commit -m "feat: add ikunpay webhook endpoint"
```

## Task 5: Visible Method Source Routing And Provider Snapshot

**Files:**
- Modify: `backend/internal/service/payment_resume_service.go`
- Modify: `backend/internal/service/payment_visible_method_instances.go`
- Modify: `backend/internal/service/payment_config_service.go`
- Modify: `backend/internal/service/payment_order_provider_snapshot.go`
- Modify: `backend/internal/service/payment_order.go`
- Modify: `backend/internal/service/payment_resume_service_test.go`
- Modify: `backend/internal/service/payment_config_service_test.go`
- Modify: `backend/internal/service/payment_config_limits_test.go`
- Modify: `backend/internal/service/payment_order_provider_snapshot_test.go`

- [ ] **Step 1: Write failing visible-source normalization and provider-key tests**

In `backend/internal/service/payment_resume_service_test.go`, extend `TestNormalizeVisibleMethodSource`:

```go
{method: payment.TypeAlipay, source: VisibleMethodSourceIkunPayAlipay, want: VisibleMethodSourceIkunPayAlipay},
{method: payment.TypeAlipay, source: payment.TypeIkunPay, want: VisibleMethodSourceIkunPayAlipay},
{method: payment.TypeWxpay, source: VisibleMethodSourceIkunPayWechat, want: VisibleMethodSourceIkunPayWechat},
{method: payment.TypeWxpay, source: payment.TypeIkunPay, want: VisibleMethodSourceIkunPayWechat},
```

Extend `TestVisibleMethodProviderKeyForSource`:

```go
{method: payment.TypeAlipay, source: VisibleMethodSourceIkunPayAlipay, wantProviderKey: payment.TypeIkunPay, wantOK: true},
{method: payment.TypeWxpay, source: VisibleMethodSourceIkunPayWechat, wantProviderKey: payment.TypeIkunPay, wantOK: true},
```

- [ ] **Step 2: Run source tests and verify failure**

Run:

```bash
cd backend && go test -tags=unit ./internal/service -run 'TestNormalizeVisibleMethodSource|TestVisibleMethodProviderKeyForSource'
```

Expected: FAIL with `undefined: VisibleMethodSourceIkunPayAlipay`.

- [ ] **Step 3: Add IkunPay source constants and mappings**

In `backend/internal/service/payment_resume_service.go`, add constants:

```go
VisibleMethodSourceIkunPayAlipay  = "ikunpay_alipay"
VisibleMethodSourceIkunPayWechat  = "ikunpay_wxpay"
```

Update `NormalizeVisibleMethodSource`:

```go
case VisibleMethodSourceIkunPayAlipay, payment.TypeIkunPay:
	return VisibleMethodSourceIkunPayAlipay
```

and:

```go
case VisibleMethodSourceIkunPayWechat, payment.TypeIkunPay:
	return VisibleMethodSourceIkunPayWechat
```

Update `VisibleMethodProviderKeyForSource`:

```go
case VisibleMethodSourceIkunPayAlipay:
	return payment.TypeIkunPay, NormalizeVisibleMethod(method) == payment.TypeAlipay
```

and:

```go
case VisibleMethodSourceIkunPayWechat:
	return payment.TypeIkunPay, NormalizeVisibleMethod(method) == payment.TypeWxpay
```

- [ ] **Step 4: Write failing visible-instance and availability tests**

In `backend/internal/service/payment_visible_method_instances_test.go` or the existing service test file that covers `enabledVisibleMethodsForProvider`, add:

```go
func TestEnabledVisibleMethodsForProviderIncludesIkunPay(t *testing.T) {
	t.Parallel()

	got := enabledVisibleMethodsForProvider(payment.TypeIkunPay, "wxpay,alipay")
	require.Equal(t, []string{payment.TypeAlipay, payment.TypeWxpay}, got)
}
```

In `backend/internal/service/payment_config_service_test.go`, add an IkunPay instance to `TestBuildVisibleMethodSourceAvailability`:

```go
{ProviderKey: payment.TypeIkunPay, SupportedTypes: "alipay,wxpay"},
```

Then assert flat availability keys:

```go
if !got[VisibleMethodSourceIkunPayAlipay] {
	t.Fatalf("expected %q to be available", VisibleMethodSourceIkunPayAlipay)
}
if !got[VisibleMethodSourceIkunPayWechat] {
	t.Fatalf("expected %q to be available", VisibleMethodSourceIkunPayWechat)
}
```

- [ ] **Step 5: Add IkunPay visible-method support and availability**

In `backend/internal/service/payment_visible_method_instances.go`, update the provider switch:

```go
case payment.TypeEasyPay, payment.TypeIkunPay:
	for _, supportedType := range splitTypes(supportedTypes) {
		addMethod(supportedType)
	}
```

In `backend/internal/service/payment_config_service.go`, update `buildVisibleMethodSourceAvailability`:

```go
case payment.TypeEasyPay, payment.TypeIkunPay:
	for _, supportedType := range splitTypes(inst.SupportedTypes) {
		switch NormalizeVisibleMethod(supportedType) {
		case payment.TypeAlipay:
			if inst.ProviderKey == payment.TypeEasyPay {
				available[VisibleMethodSourceEasyPayAlipay] = true
			}
			if inst.ProviderKey == payment.TypeIkunPay {
				available[VisibleMethodSourceIkunPayAlipay] = true
			}
		case payment.TypeWxpay:
			if inst.ProviderKey == payment.TypeEasyPay {
				available[VisibleMethodSourceEasyPayWechat] = true
			}
			if inst.ProviderKey == payment.TypeIkunPay {
				available[VisibleMethodSourceIkunPayWechat] = true
			}
		}
	}
}
```

- [ ] **Step 6: Write failing route selection and limits tests**

In `backend/internal/service/payment_resume_service_test.go`, add a selection case using configured `VisibleMethodSourceIkunPayAlipay`:

```go
{
	name:          "alipay visible method routes to ikunpay source",
	method:        payment.TypeAlipay,
	sourceSetting: VisibleMethodSourceIkunPayAlipay,
	instances: []*dbent.PaymentProviderInstance{
		{ProviderKey: payment.TypeEasyPay, SupportedTypes: payment.TypeAlipay, Enabled: true},
		{ProviderKey: payment.TypeIkunPay, SupportedTypes: payment.TypeAlipay, Enabled: true},
	},
	wantProviderKey: payment.TypeIkunPay,
}
```

In `backend/internal/service/payment_config_limits_test.go`, add an IkunPay source case:

```go
{
	name:          "alipay uses ikunpay source limits",
	method:        payment.TypeAlipay,
	sourceSetting: VisibleMethodSourceIkunPayAlipay,
	providerKey:   payment.TypeIkunPay,
	wantProvider:  payment.TypeIkunPay,
}
```

- [ ] **Step 7: Write failing provider snapshot tests**

In `backend/internal/service/payment_order_provider_snapshot_test.go`, add:

```go
func TestBuildPaymentOrderProviderSnapshotIncludesIkunPayMerchantIdentity(t *testing.T) {
	t.Parallel()

	snapshot := buildPaymentOrderProviderSnapshot(&payment.InstanceSelection{
		InstanceID:  "42",
		ProviderKey: payment.TypeIkunPay,
		Config: map[string]string{
			"pid": "merchant-1",
		},
		PaymentMode: "qrcode",
	}, CreateOrderRequest{PaymentType: payment.TypeAlipay})

	require.Equal(t, payment.TypeIkunPay, snapshot["provider_key"])
	require.Equal(t, "merchant-1", snapshot["merchant_id"])
}

func TestValidateProviderSnapshotMetadataRejectsIkunPayPIDMismatch(t *testing.T) {
	t.Parallel()

	order := &dbent.PaymentOrder{
		ProviderSnapshot: map[string]any{
			"provider_key": "ikunpay",
			"merchant_id":  "merchant-1",
		},
	}

	err := validateProviderSnapshotMetadata(order, payment.TypeIkunPay, map[string]string{"pid": "merchant-2"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ikunpay pid mismatch")
}
```

- [ ] **Step 8: Add IkunPay snapshot validation**

In `backend/internal/service/payment_order_provider_snapshot.go`, add a branch:

```go
case payment.TypeIkunPay:
	if expected := strings.TrimSpace(snapshot.MerchantID); expected != "" {
		actual := strings.TrimSpace(metadata["pid"])
		if actual == "" {
			return fmt.Errorf("ikunpay pid missing")
		}
		if !strings.EqualFold(expected, actual) {
			return fmt.Errorf("ikunpay pid mismatch: expected %s, got %s", expected, actual)
		}
	}
```

In `backend/internal/service/payment_order.go`, add IkunPay merchant identity to `buildPaymentOrderProviderSnapshot`:

```go
if providerKey == payment.TypeIkunPay {
	if merchantID := strings.TrimSpace(sel.Config["pid"]); merchantID != "" {
		snapshot["merchant_id"] = merchantID
	}
}
```

- [ ] **Step 9: Run service tests and commit**

Run:

```bash
cd backend && go test -tags=unit ./internal/service -run 'IkunPay|VisibleMethod|ProviderSnapshot|AvailableMethodLimits'
```

Expected: PASS.

Commit:

```bash
git add backend/internal/service/payment_resume_service.go backend/internal/service/payment_visible_method_instances.go backend/internal/service/payment_config_service.go backend/internal/service/payment_order.go backend/internal/service/payment_order_provider_snapshot.go backend/internal/service/*_test.go
git commit -m "feat: route visible methods through ikunpay"
```

## Task 6: Frontend Provider Configuration And Dialog

**Files:**
- Modify: `frontend/src/types/payment.ts`
- Modify: `frontend/src/components/payment/providerConfig.ts`
- Modify: `frontend/src/components/payment/PaymentProviderDialog.vue`
- Modify: `frontend/src/components/payment/ProviderCard.vue`
- Modify: `frontend/src/i18n/locales/zh.ts`
- Modify: `frontend/src/i18n/locales/en.ts`
- Modify: `frontend/src/components/payment/__tests__/providerConfig.spec.ts`
- Modify: `frontend/src/components/payment/__tests__/PaymentProviderDialog.spec.ts`

- [ ] **Step 1: Write failing provider config tests**

In `frontend/src/components/payment/__tests__/providerConfig.spec.ts`, replace the providerConfig import with:

```ts
import {
  PAYMENT_CURRENCY_OPTIONS,
  PROVIDER_CALLBACK_PATHS,
  PROVIDER_CONFIG_FIELDS,
  PROVIDER_SUPPORTED_TYPES,
  WEBHOOK_PATHS,
} from '@/components/payment/providerConfig'
```

Add tests:

```ts
describe('PROVIDER_CONFIG_FIELDS.ikunpay', () => {
  it('supports Alipay and WeChat through the IkunPay source provider', () => {
    expect(PROVIDER_SUPPORTED_TYPES.ikunpay).toEqual(['alipay', 'wxpay'])
  })

  it('exposes IkunPay callback paths', () => {
    expect(WEBHOOK_PATHS.ikunpay).toBe('/api/v1/payment/webhook/ikunpay')
    expect(PROVIDER_CALLBACK_PATHS.ikunpay).toEqual({
      notifyUrl: '/api/v1/payment/webhook/ikunpay',
      returnUrl: '/payment/result',
    })
  })

  it('marks RSA key fields sensitive and defaults apiBase', () => {
    expect(findField('ikunpay', 'pid')?.sensitive).toBe(false)
    expect(findField('ikunpay', 'merchantPrivateKey')?.sensitive).toBe(true)
    expect(findField('ikunpay', 'platformPublicKey')?.sensitive).toBe(true)
    expect(findField('ikunpay', 'apiBase')?.defaultValue).toBe('https://ikunpay.com')
    expect(findField('ikunpay', 'apiBase')?.hintKey).toBe('admin.settings.payment.field_ikunpayApiBaseHint')
  })
})
```

- [ ] **Step 2: Run provider config test and verify failure**

Run:

```bash
pnpm --dir frontend exec vitest run src/components/payment/__tests__/providerConfig.spec.ts
```

Expected: FAIL because `PROVIDER_SUPPORTED_TYPES.ikunpay` is undefined.

- [ ] **Step 3: Add IkunPay provider config constants**

In `frontend/src/types/payment.ts`, add `ikunpay`:

```ts
export type PaymentType = 'alipay' | 'wxpay' | 'alipay_direct' | 'wxpay_direct' | 'stripe' | 'easypay' | 'ikunpay' | 'airwallex'
```

In `frontend/src/components/payment/providerConfig.ts`, update supported types:

```ts
ikunpay: ['alipay', 'wxpay'],
```

Add webhook path:

```ts
ikunpay: '/api/v1/payment/webhook/ikunpay',
```

Add callback paths:

```ts
ikunpay: { notifyUrl: WEBHOOK_PATHS.ikunpay, returnUrl: RETURN_PATH },
```

Add config fields:

```ts
ikunpay: [
  { key: 'pid', label: 'PID', sensitive: false },
  { key: 'merchantPrivateKey', label: '', sensitive: true },
  { key: 'platformPublicKey', label: '', sensitive: true },
  { key: 'apiBase', label: '', sensitive: false, defaultValue: 'https://ikunpay.com', hintKey: 'admin.settings.payment.field_ikunpayApiBaseHint' },
],
```

- [ ] **Step 4: Write failing provider dialog mode tests**

In `frontend/src/components/payment/__tests__/PaymentProviderDialog.spec.ts`, expand the providerConfig import:

```ts
import {
  PAYMENT_MODE_POPUP,
  PAYMENT_MODE_QRCODE,
  STRIPE_SDK_API_VERSION,
} from '@/components/payment/providerConfig'
```

Add a test beside EasyPay mode behavior:

```ts
it('defaults IkunPay providers to QR code mode and allows popup mode', async () => {
  const provider = providerFactory({
    provider_key: 'ikunpay',
    name: 'IkunPay',
    config: {},
    supported_types: ['alipay', 'wxpay'],
    payment_mode: '',
  })
  const wrapper = mountDialog({ editing: provider })

  expect((wrapper.vm as any).form.payment_mode).toBe(PAYMENT_MODE_QRCODE)
  expect((wrapper.vm as any).providerSupportsPaymentMode('ikunpay')).toBe(true)
  ;(wrapper.vm as any).form.payment_mode = PAYMENT_MODE_POPUP
  await wrapper.vm.$nextTick()
  expect((wrapper.vm as any).form.payment_mode).toBe(PAYMENT_MODE_POPUP)
})
```

- [ ] **Step 5: Enable IkunPay payment modes and card labels**

In `frontend/src/components/payment/PaymentProviderDialog.vue`, update mode helpers:

```ts
if (providerKey === 'easypay' || providerKey === 'ikunpay') return PAYMENT_MODE_QRCODE
```

and:

```ts
return providerKey === 'easypay' || providerKey === 'ikunpay' || providerKey === 'alipay'
```

Where EasyPay-specific available modes are selected, add IkunPay to the same branch:

```ts
if (providerKey === 'easypay' || providerKey === 'ikunpay') {
  return [
    { value: PAYMENT_MODE_QRCODE, labelKey: 'admin.settings.payment.easypayQrcode' },
    { value: PAYMENT_MODE_POPUP, labelKey: 'admin.settings.payment.easypayRedirect' },
  ]
}
```

In `frontend/src/components/payment/ProviderCard.vue`, add:

```ts
ikunpay: 'admin.settings.payment.providerIkunpay',
```

- [ ] **Step 6: Add i18n labels**

In `frontend/src/i18n/locales/zh.ts`, add payment admin labels near the existing provider labels:

```ts
providerIkunpay: 'IkunPay',
field_merchantPrivateKey: '商户 RSA 私钥',
field_platformPublicKey: '平台 RSA 公钥',
field_ikunpayApiBaseHint: '默认 https://ikunpay.com，除非 IkunPay 后台提供了专属 API 地址。',
```

Add payment method label near existing method/provider names:

```ts
ikunpay: 'IkunPay',
```

In `frontend/src/i18n/locales/en.ts`, add:

```ts
providerIkunpay: 'IkunPay',
field_merchantPrivateKey: 'Merchant RSA Private Key',
field_platformPublicKey: 'Platform RSA Public Key',
field_ikunpayApiBaseHint: 'Defaults to https://ikunpay.com unless the IkunPay dashboard provides a dedicated API base URL.',
```

Add payment method label:

```ts
ikunpay: 'IkunPay',
```

- [ ] **Step 7: Run frontend provider tests and commit**

Run:

```bash
pnpm --dir frontend exec vitest run src/components/payment/__tests__/providerConfig.spec.ts src/components/payment/__tests__/PaymentProviderDialog.spec.ts
```

Expected: PASS.

Commit:

```bash
git add frontend/src/types/payment.ts frontend/src/components/payment/providerConfig.ts frontend/src/components/payment/PaymentProviderDialog.vue frontend/src/components/payment/ProviderCard.vue frontend/src/i18n/locales/zh.ts frontend/src/i18n/locales/en.ts frontend/src/components/payment/__tests__/providerConfig.spec.ts frontend/src/components/payment/__tests__/PaymentProviderDialog.spec.ts
git commit -m "feat: add ikunpay admin provider configuration"
```

## Task 7: Frontend Visible Method Source Settings

**Files:**
- Modify: `frontend/src/api/admin/settings.ts`
- Modify: `frontend/src/api/__tests__/settings.paymentVisibleMethods.spec.ts`

- [ ] **Step 1: Write failing source option and alias tests**

In `frontend/src/api/__tests__/settings.paymentVisibleMethods.spec.ts`, extend normalization expectations:

```ts
expect(normalizePaymentVisibleMethodSource('alipay', 'ikunpay')).toBe('ikunpay_alipay')
expect(normalizePaymentVisibleMethodSource('alipay', 'ikunpay_alipay')).toBe('ikunpay_alipay')
expect(normalizePaymentVisibleMethodSource('wxpay', 'ikunpay')).toBe('ikunpay_wxpay')
expect(normalizePaymentVisibleMethodSource('wxpay', 'ikunpay_wxpay')).toBe('ikunpay_wxpay')
```

Extend `getPaymentVisibleMethodSourceOptions('alipay')` expected array with:

```ts
{
  value: 'ikunpay_alipay',
  labelZh: 'IkunPay 支付宝',
  labelEn: 'IkunPay Alipay',
},
```

Extend `getPaymentVisibleMethodSourceOptions('wxpay')` expected array with:

```ts
{
  value: 'ikunpay_wxpay',
  labelZh: 'IkunPay 微信',
  labelEn: 'IkunPay WeChat Pay',
},
```

- [ ] **Step 2: Run visible method settings test and verify failure**

Run:

```bash
pnpm --dir frontend exec vitest run src/api/__tests__/settings.paymentVisibleMethods.spec.ts
```

Expected: FAIL because IkunPay source aliases are not known.

- [ ] **Step 3: Add IkunPay source types, options, and aliases**

In `frontend/src/api/admin/settings.ts`, update the union:

```ts
export type PaymentVisibleMethodSource =
  | ""
  | "official_alipay"
  | "easypay_alipay"
  | "ikunpay_alipay"
  | "official_wxpay"
  | "easypay_wxpay"
  | "ikunpay_wxpay";
```

Add Alipay option:

```ts
{
  value: "ikunpay_alipay",
  labelZh: "IkunPay 支付宝",
  labelEn: "IkunPay Alipay",
},
```

Add WeChat option:

```ts
{
  value: "ikunpay_wxpay",
  labelZh: "IkunPay 微信",
  labelEn: "IkunPay WeChat Pay",
},
```

Add aliases:

```ts
ikunpay_alipay: "ikunpay_alipay",
ikunpay: "ikunpay_alipay",
```

and:

```ts
ikunpay_wxpay: "ikunpay_wxpay",
ikunpay: "ikunpay_wxpay",
```

- [ ] **Step 4: Run settings test and commit**

Run:

```bash
pnpm --dir frontend exec vitest run src/api/__tests__/settings.paymentVisibleMethods.spec.ts
```

Expected: PASS.

Commit:

```bash
git add frontend/src/api/admin/settings.ts frontend/src/api/__tests__/settings.paymentVisibleMethods.spec.ts
git commit -m "feat: add ikunpay visible method source settings"
```

## Task 8: Full Verification And Secret Audit

**Files:**
- Inspect: all changed files
- No new implementation file expected unless a verification failure exposes a missing import, stale type, or test harness mismatch.

- [ ] **Step 1: Run backend focused payment tests**

Run:

```bash
cd backend && go test ./internal/payment/provider ./internal/handler ./internal/server/routes
```

Expected: PASS.

Run:

```bash
cd backend && go test -tags=unit ./internal/service
```

Expected: PASS.

- [ ] **Step 2: Run backend full verification**

Run:

```bash
PATH="$(go env GOPATH)/bin:$PATH" make test-backend
```

Expected: PASS for backend tests and lint.

- [ ] **Step 3: Run frontend focused tests**

Run:

```bash
pnpm --dir frontend exec vitest run src/api/__tests__/settings.paymentVisibleMethods.spec.ts src/components/payment/__tests__/providerConfig.spec.ts src/components/payment/__tests__/PaymentProviderDialog.spec.ts
```

Expected: PASS.

- [ ] **Step 4: Run frontend critical verification**

Run:

```bash
pnpm --dir frontend run lint:check
pnpm --dir frontend run typecheck
make test-frontend-critical
```

Expected: PASS. Existing Vue Router warnings are acceptable only if the tests still exit successfully.

- [ ] **Step 5: Run secret and docs audit**

Run:

```bash
git diff --cached --stat
git diff --stat origin/dev...HEAD
rg -n "merchantPrivateKey\\s*[:=]\\s*['\\\"][A-Za-z0-9+/=]{80,}|platformPublicKey\\s*[:=]\\s*['\\\"][A-Za-z0-9+/=]{80,}|RSA PRIVATE KEY|PRIVATE KEY-----" backend frontend docs
```

Expected: the `rg` command may only show test-generated key labels or field labels, not real key material. If it shows a real key body or a committed dashboard credential, remove that content before continuing.

- [ ] **Step 6: Commit verification cleanup if needed**

If formatting or import fixes were required by verification, commit only those changed files:

```bash
git add backend frontend
git commit -m "test: verify ikunpay integration"
```

- [ ] **Step 7: Summarize final branch state**

Run:

```bash
git status --short --branch
git log --oneline --decorate -n 8
```

Expected: clean worktree, branch ahead of `origin/dev` by the IkunPay design/plan plus implementation commits.

Final response should include:

```text
Implemented IkunPay as an Alipay/WeChat source provider on branch worktree-ikunpay-payment-integration.
Verification passed:
- PATH="$(go env GOPATH)/bin:$PATH" make test-backend
- pnpm --dir frontend run lint:check
- pnpm --dir frontend run typecheck
- make test-frontend-critical
No real IkunPay secrets were committed.
```
