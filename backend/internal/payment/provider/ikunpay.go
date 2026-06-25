package provider

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
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
	type signPair struct {
		key   string
		value string
	}
	pairs := make([]signPair, 0, len(params))
	for key, value := range params {
		key = strings.TrimSpace(key)
		if key == "" || key == "sign" || key == "sign_type" || strings.TrimSpace(value) == "" {
			continue
		}
		pairs = append(pairs, signPair{key: key, value: value})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].key < pairs[j].key
	})
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, pair.key+"="+pair.value)
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

type ikunPayCreateResponse struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	Message   string `json:"message"`
	TradeNo   string `json:"trade_no"`
	PayType   string `json:"pay_type"`
	PayInfo   string `json:"pay_info"`
	Timestamp string `json:"timestamp"`
	Sign      string `json:"sign"`
	SignType  string `json:"sign_type"`
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
		return fmt.Errorf("ikunpay response missing signature")
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
	RefundNo  string `json:"refund_no"`
	Timestamp string `json:"timestamp"`
	Sign      string `json:"sign"`
	SignType  string `json:"sign_type"`
}

func (r ikunPayBasicResponse) Signable() map[string]string {
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
