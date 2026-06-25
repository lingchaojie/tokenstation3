package provider

import (
	"bytes"
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
	configCopy := make(map[string]string, len(config))
	for key, value := range config {
		configCopy[key] = value
	}
	for _, key := range []string{"pid", "merchantPrivateKey", "platformPublicKey", "notifyUrl", "returnUrl"} {
		if strings.TrimSpace(configCopy[key]) == "" {
			return nil, fmt.Errorf("ikunpay config missing required key: %s", key)
		}
	}
	if _, err := parseIkunPayPrivateKey(configCopy["merchantPrivateKey"]); err != nil {
		return nil, fmt.Errorf("ikunpay merchantPrivateKey invalid: %w", err)
	}
	if _, err := parseIkunPayPublicKey(configCopy["platformPublicKey"]); err != nil {
		return nil, fmt.Errorf("ikunpay platformPublicKey invalid: %w", err)
	}
	apiBase := normalizeIkunPayAPIBase(configCopy["apiBase"])
	if apiBase == "" {
		apiBase = ikunpayDefaultAPIBase
	}
	configCopy["apiBase"] = apiBase
	return &IkunPay{
		instanceID: instanceID,
		config:     configCopy,
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
	rawResp, err := i.postForm(ctx, "/api/pay/create", params)
	if err != nil {
		return nil, fmt.Errorf("ikunpay create: %w", err)
	}
	resp, err := ikunPayCreateResponseFromMap(rawResp)
	if err != nil {
		return nil, fmt.Errorf("ikunpay create response: %w", err)
	}
	if err := i.verifyResponseSignature(rawResp); err != nil {
		return nil, fmt.Errorf("ikunpay verify create response: %w", err)
	}
	if resp.Code != ikunpayCodeSuccess {
		return nil, fmt.Errorf("ikunpay error: %s", firstNonEmpty(resp.Msg, resp.Message))
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

func ikunPayCreateResponseFromMap(params map[string]string) (ikunPayCreateResponse, error) {
	code, err := parseIkunPayResponseCode(params)
	if err != nil {
		return ikunPayCreateResponse{}, err
	}
	return ikunPayCreateResponse{
		Code:      code,
		Msg:       params["msg"],
		Message:   params["message"],
		TradeNo:   params["trade_no"],
		PayType:   params["pay_type"],
		PayInfo:   params["pay_info"],
		Timestamp: params["timestamp"],
		Sign:      params["sign"],
		SignType:  params["sign_type"],
	}, nil
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

func (i *IkunPay) postForm(ctx context.Context, path string, params map[string]string) (map[string]string, error) {
	form := url.Values{}
	for key, value := range params {
		if strings.TrimSpace(value) != "" {
			form.Set(key, value)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, i.apiBase+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIkunPayResponseSize))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, summarizeIkunPayBody(body))
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	rawResp, err := decodeIkunPayResponseMap(body)
	if err != nil {
		return nil, err
	}
	return rawResp, nil
}

func decodeIkunPayResponseMap(body []byte) (map[string]string, error) {
	var raw map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse JSON: %w: %s", err, summarizeIkunPayBody(body))
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		switch v := value.(type) {
		case nil:
			out[key] = ""
		case string:
			out[key] = v
		case json.Number:
			out[key] = v.String()
		case bool:
			out[key] = strconv.FormatBool(v)
		default:
			out[key] = fmt.Sprint(v)
		}
	}
	return out, nil
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
	rawResp, err := i.postForm(ctx, "/api/pay/query", params)
	if err != nil {
		return nil, fmt.Errorf("ikunpay query: %w", err)
	}
	resp, err := ikunPayQueryResponseFromMap(rawResp)
	if err != nil {
		return nil, fmt.Errorf("ikunpay query response: %w", err)
	}
	if err := i.verifyResponseSignature(rawResp); err != nil {
		return nil, fmt.Errorf("ikunpay verify query response: %w", err)
	}
	if resp.Code != ikunpayCodeSuccess {
		return nil, fmt.Errorf("ikunpay query error: %s", firstNonEmpty(resp.Msg, resp.Message))
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
	expectedPID := strings.TrimSpace(i.config["pid"])
	actualPID := strings.TrimSpace(params["pid"])
	if actualPID == "" {
		return nil, fmt.Errorf("ikunpay notification missing pid")
	}
	if !strings.EqualFold(expectedPID, actualPID) {
		return nil, fmt.Errorf("ikunpay pid mismatch: expected %s, got %s", expectedPID, actualPID)
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
	params["money"] = strings.TrimSpace(req.Amount)
	params["out_refund_no"] = "refund-" + firstNonEmpty(req.OrderID, req.TradeNo)
	if strings.TrimSpace(req.TradeNo) != "" {
		params["trade_no"] = strings.TrimSpace(req.TradeNo)
	} else if strings.TrimSpace(req.OrderID) != "" {
		params["out_trade_no"] = strings.TrimSpace(req.OrderID)
	} else {
		return nil, fmt.Errorf("ikunpay refund missing order identifier")
	}
	resp, err := i.refundOnce(ctx, params)
	if err != nil && strings.TrimSpace(req.TradeNo) != "" && strings.TrimSpace(req.OrderID) != "" && resp != nil && ikunPayIsNotFoundMessage(firstNonEmpty(resp.Msg, resp.Message)) {
		params = i.baseParams()
		params["out_trade_no"] = strings.TrimSpace(req.OrderID)
		params["money"] = strings.TrimSpace(req.Amount)
		params["out_refund_no"] = "refund-" + firstNonEmpty(req.OrderID, req.TradeNo)
		resp, err = i.refundOnce(ctx, params)
	}
	if err != nil {
		return nil, err
	}
	return &payment.RefundResponse{RefundID: firstNonEmpty(resp.RefundNo, params["out_refund_no"], params["trade_no"], params["out_trade_no"]), Status: payment.ProviderStatusSuccess}, nil
}

func (i *IkunPay) refundOnce(ctx context.Context, params map[string]string) (*ikunPayRefundResponse, error) {
	if err := i.signParams(params); err != nil {
		return nil, fmt.Errorf("ikunpay sign refund: %w", err)
	}
	rawResp, err := i.postForm(ctx, "/api/pay/refund", params)
	if err != nil {
		return nil, fmt.Errorf("ikunpay refund: %w", err)
	}
	if err := i.verifyResponseSignature(rawResp); err != nil {
		return nil, fmt.Errorf("ikunpay verify refund response: %w", err)
	}
	resp, err := ikunPayRefundResponseFromMap(rawResp)
	if err != nil {
		return nil, fmt.Errorf("ikunpay refund response: %w", err)
	}
	if resp.Code != ikunpayCodeSuccess {
		return &resp, fmt.Errorf("ikunpay refund error: %s", firstNonEmpty(resp.Msg, resp.Message))
	}
	return &resp, nil
}

func ikunPayIsNotFoundMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "订单编号不存在") ||
		strings.Contains(message, "order not found") ||
		strings.Contains(message, "not found") ||
		strings.Contains(message, "不存在")
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
	rawResp, err := i.postForm(ctx, "/api/pay/close", params)
	if err != nil {
		return fmt.Errorf("ikunpay close: %w", err)
	}
	resp, err := ikunPayBasicResponseFromMap(rawResp)
	if err != nil {
		return fmt.Errorf("ikunpay close response: %w", err)
	}
	if err := i.verifyResponseSignature(rawResp); err != nil {
		return fmt.Errorf("ikunpay verify close response: %w", err)
	}
	if resp.Code != ikunpayCodeSuccess {
		return fmt.Errorf("ikunpay close error: %s", firstNonEmpty(resp.Msg, resp.Message))
	}
	return nil
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

func ikunPayQueryResponseFromMap(params map[string]string) (ikunPayQueryResponse, error) {
	code, err := parseIkunPayResponseCode(params)
	if err != nil {
		return ikunPayQueryResponse{}, err
	}
	return ikunPayQueryResponse{
		Code:       code,
		Msg:        params["msg"],
		Message:    params["message"],
		TradeNo:    params["trade_no"],
		OutTradeNo: params["out_trade_no"],
		Status:     params["status"],
		Money:      params["money"],
		Timestamp:  params["timestamp"],
		Sign:       params["sign"],
		SignType:   params["sign_type"],
	}, nil
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

func ikunPayRefundResponseFromMap(params map[string]string) (ikunPayRefundResponse, error) {
	code, err := parseIkunPayResponseCode(params)
	if err != nil {
		return ikunPayRefundResponse{}, err
	}
	return ikunPayRefundResponse{
		Code:      code,
		Msg:       params["msg"],
		Message:   params["message"],
		RefundNo:  params["refund_no"],
		Timestamp: params["timestamp"],
		Sign:      params["sign"],
		SignType:  params["sign_type"],
	}, nil
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

func ikunPayBasicResponseFromMap(params map[string]string) (ikunPayBasicResponse, error) {
	code, err := parseIkunPayResponseCode(params)
	if err != nil {
		return ikunPayBasicResponse{}, err
	}
	return ikunPayBasicResponse{
		Code:      code,
		Msg:       params["msg"],
		Message:   params["message"],
		RefundNo:  params["refund_no"],
		Timestamp: params["timestamp"],
		Sign:      params["sign"],
		SignType:  params["sign_type"],
	}, nil
}

func parseIkunPayResponseCode(params map[string]string) (int, error) {
	code := strings.TrimSpace(params["code"])
	if code == "" {
		return 0, fmt.Errorf("missing code")
	}
	parsed, err := strconv.Atoi(code)
	if err != nil {
		return 0, fmt.Errorf("invalid code: %w", err)
	}
	return parsed, nil
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
