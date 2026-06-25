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
