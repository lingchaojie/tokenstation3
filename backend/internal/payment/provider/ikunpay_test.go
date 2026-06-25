package provider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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

func TestNewIkunPayCopiesConfig(t *testing.T) {
	t.Parallel()

	cfg := newIkunPayTestConfig(t, "https://ikunpay.example/api/pay/create")
	provider, err := NewIkunPay("inst-1", cfg)
	if err != nil {
		t.Fatalf("NewIkunPay: %v", err)
	}
	cfg["pid"] = "mutated-merchant"
	cfg["apiBase"] = "https://mutated.example"

	if got := provider.MerchantIdentityMetadata()["pid"]; got != "merchant-1" {
		t.Fatalf("pid = %q, want merchant-1", got)
	}
	if provider.apiBase != "https://ikunpay.example" {
		t.Fatalf("apiBase = %q, want original normalized base", provider.apiBase)
	}
}

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
			"code":      "0",
			"msg":       "ok",
			"trade_no":  "upstream-1",
			"pay_type":  "qrcode",
			"pay_info":  "https://qr.example/order-1",
			"timestamp": "1780000000",
			"sign_type": "RSA",
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
			"code":      "0",
			"msg":       "ok",
			"trade_no":  "upstream-2",
			"pay_type":  "jump",
			"pay_info":  "https://ikunpay.example/pay/upstream-2",
			"timestamp": "1780000000",
			"sign_type": "RSA",
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

func TestIkunPayCreatePaymentRejectsUnsignedResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if err := ikunPayVerify(formValuesToMap(r.PostForm), ikunPayTestMerchantPublicKey(t), r.PostForm.Get("sign")); err != nil {
			t.Fatalf("request signature invalid: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"code":      0,
			"msg":       "ok",
			"trade_no":  "upstream-unsigned",
			"pay_type":  "qrcode",
			"pay_info":  "https://qr.example/unsigned",
			"timestamp": "1780000000",
			"sign_type": "RSA",
		}); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}))
	defer server.Close()

	provider := newTestIkunPay(t, server.URL, "qrcode")
	_, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "order-unsigned",
		Amount:      "10.00",
		PaymentType: payment.TypeAlipay,
		Subject:     "Unsigned Product",
		NotifyURL:   "https://merchant.example/notify",
		ReturnURL:   "https://merchant.example/return",
	})
	if err == nil {
		t.Fatal("CreatePayment accepted unsigned response")
	}
	if !strings.Contains(err.Error(), "verify create response") && !strings.Contains(err.Error(), "missing signature") {
		t.Fatalf("error = %v, want verify create response or missing signature", err)
	}
}

func TestIkunPayCreatePaymentVerifiesResponseWithAdditionalSignedFields(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if err := ikunPayVerify(formValuesToMap(r.PostForm), ikunPayTestMerchantPublicKey(t), r.PostForm.Get("sign")); err != nil {
			t.Fatalf("request signature invalid: %v", err)
		}
		writeIkunPayJSON(t, w, map[string]string{
			"code":         "0",
			"msg":          "ok",
			"trade_no":     "upstream-extra",
			"api_trade_no": "api-1",
			"pay_type":     "qrcode",
			"pay_info":     "https://qr.example/extra",
			"timestamp":    "1780000000",
			"sign_type":    "RSA",
		})
	}))
	defer server.Close()

	provider := newTestIkunPay(t, server.URL, "qrcode")
	resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "order-extra",
		Amount:      "10.00",
		PaymentType: payment.TypeAlipay,
		Subject:     "Extra Field Product",
		NotifyURL:   "https://merchant.example/notify",
		ReturnURL:   "https://merchant.example/return",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.TradeNo != "upstream-extra" || resp.QRCode != "https://qr.example/extra" {
		t.Fatalf("response = %+v, want extra-field signed response mapping", resp)
	}
}

func TestIkunPayCreatePaymentAcceptsStringCodeAndNumericTimestamp(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if err := ikunPayVerify(formValuesToMap(r.PostForm), ikunPayTestMerchantPublicKey(t), r.PostForm.Get("sign")); err != nil {
			t.Fatalf("request signature invalid: %v", err)
		}
		fields := map[string]string{
			"code":      "0",
			"msg":       "ok",
			"trade_no":  "upstream-string-code",
			"pay_type":  "qrcode",
			"pay_info":  "https://qr.example/string-code",
			"timestamp": "1780000000",
			"sign_type": "RSA",
		}
		signature, err := ikunPaySign(fields, ikunPayTestPlatformPrivateKey(t))
		if err != nil {
			t.Fatalf("sign response: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":"0","msg":"ok","trade_no":"upstream-string-code","pay_type":"qrcode","pay_info":"https://qr.example/string-code","timestamp":1780000000,"sign_type":"RSA","sign":%q}`, signature)
	}))
	defer server.Close()

	provider := newTestIkunPay(t, server.URL, "qrcode")
	resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "order-string-code",
		Amount:      "10.00",
		PaymentType: payment.TypeAlipay,
		Subject:     "String Code Product",
		NotifyURL:   "https://merchant.example/notify",
		ReturnURL:   "https://merchant.example/return",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.TradeNo != "upstream-string-code" || resp.QRCode != "https://qr.example/string-code" {
		t.Fatalf("response = %+v, want string-code response mapping", resp)
	}
}

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
					"code":         "0",
					"msg":          "ok",
					"trade_no":     "upstream-1",
					"out_trade_no": "order-1",
					"status":       tt.status,
					"money":        "10.00",
					"timestamp":    "1780000000",
					"sign_type":    "RSA",
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

func TestIkunPayQueryOrderAcceptsNumericMoneyAndTimestamp(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if err := ikunPayVerify(formValuesToMap(r.PostForm), ikunPayTestMerchantPublicKey(t), r.PostForm.Get("sign")); err != nil {
			t.Fatalf("request signature invalid: %v", err)
		}
		fields := map[string]string{
			"code":         "0",
			"msg":          "ok",
			"trade_no":     "upstream-money",
			"out_trade_no": "order-money",
			"status":       "1",
			"money":        "10.00",
			"timestamp":    "1780000000",
			"sign_type":    "RSA",
		}
		signature, err := ikunPaySign(fields, ikunPayTestPlatformPrivateKey(t))
		if err != nil {
			t.Fatalf("sign response: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"msg":"ok","trade_no":"upstream-money","out_trade_no":"order-money","status":"1","money":10.00,"timestamp":1780000000,"sign_type":"RSA","sign":%q}`, signature)
	}))
	defer server.Close()

	provider := newTestIkunPay(t, server.URL, "qrcode")
	resp, err := provider.QueryOrder(context.Background(), "order-money")
	if err != nil {
		t.Fatalf("QueryOrder: %v", err)
	}
	if resp.Status != payment.ProviderStatusPaid || resp.TradeNo != "upstream-money" || resp.Amount != 10 {
		t.Fatalf("response = %+v, want paid amount 10", resp)
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
			"code":      "0",
			"msg":       "ok",
			"refund_no": "refund-1",
			"timestamp": "1780000000",
			"sign_type": "RSA",
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

func TestIkunPayRefundRetriesWithOutTradeNoWhenTradeNoNotFound(t *testing.T) {
	t.Parallel()

	var formsMu sync.Mutex
	var gotForms []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pay/refund" {
			t.Fatalf("path = %q, want /api/pay/refund", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if err := ikunPayVerify(formValuesToMap(r.PostForm), ikunPayTestMerchantPublicKey(t), r.PostForm.Get("sign")); err != nil {
			t.Fatalf("request signature invalid: %v", err)
		}

		formsMu.Lock()
		gotForms = append(gotForms, r.PostForm)
		attempt := len(gotForms)
		formsMu.Unlock()

		switch attempt {
		case 1:
			if got := r.PostForm.Get("trade_no"); got != "upstream-1" {
				t.Fatalf("first trade_no = %q, want upstream-1", got)
			}
			if got := r.PostForm.Get("out_trade_no"); got != "" {
				t.Fatalf("first out_trade_no = %q, want empty", got)
			}
			writeIkunPayJSON(t, w, map[string]string{
				"code":      "1",
				"msg":       "订单编号不存在！",
				"timestamp": "1780000000",
				"sign_type": "RSA",
			})
		case 2:
			if got := r.PostForm.Get("trade_no"); got != "" {
				t.Fatalf("second trade_no = %q, want empty", got)
			}
			if got := r.PostForm.Get("out_trade_no"); got != "order-1" {
				t.Fatalf("second out_trade_no = %q, want order-1", got)
			}
			writeIkunPayJSON(t, w, map[string]string{
				"code":      "0",
				"msg":       "ok",
				"refund_no": "refund-1",
				"timestamp": "1780000000",
				"sign_type": "RSA",
			})
		default:
			t.Fatalf("unexpected refund attempt %d", attempt)
		}
	}))
	defer server.Close()

	provider := newTestIkunPay(t, server.URL, "qrcode")
	resp, err := provider.Refund(context.Background(), payment.RefundRequest{
		TradeNo: "upstream-1",
		OrderID: "order-1",
		Amount:  "3.50",
		Reason:  "requested",
	})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if resp.Status != payment.ProviderStatusSuccess || resp.RefundID != "refund-1" {
		t.Fatalf("refund response = %+v", resp)
	}

	formsMu.Lock()
	defer formsMu.Unlock()
	if len(gotForms) != 2 {
		t.Fatalf("refund attempts = %d, want 2", len(gotForms))
	}
}

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
			code, err := strconv.Atoi(value)
			if err != nil {
				t.Fatalf("Atoi code: %v", err)
			}
			payload[key] = code
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
