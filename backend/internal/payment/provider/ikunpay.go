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
	ikunpaySignTypeRSA     = "RSA"
	ikunpayCodeSuccess     = 0
	ikunpayHTTPTimeout     = 10 * time.Second
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

func (i *IkunPay) Name() string        { return "IkunPay" }
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
