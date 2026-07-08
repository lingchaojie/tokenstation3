package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBuildKiroCaptureHeadersRedactsAndSplits(t *testing.T) {
	reqH := http.Header{
		"Authorization":     []string{"Bearer secret"},
		"Anthropic-Version": []string{"2023-06-01"},
	}
	resp := &http.Response{
		Header: http.Header{
			"Set-Cookie":   []string{"s=1"},
			"X-Request-Id": []string{"req_up"},
		},
		Request: &http.Request{Header: reqH},
	}
	h := buildKiroCaptureHeaders(resp)
	if h.RequestHeaders == nil || h.ResponseHeaders == nil {
		t.Fatal("both header blobs expected")
	}
	if got := string(h.RequestHeaders); strings.Contains(got, "secret") || strings.Contains(got, "Authorization") {
		t.Fatalf("credential not redacted in req headers: %s", got)
	}
	if got := string(h.RequestHeaders); !strings.Contains(got, "Anthropic-Version") {
		t.Fatalf("benign req header dropped: %s", got)
	}
	if got := string(h.ResponseHeaders); strings.Contains(got, "Set-Cookie") {
		t.Fatalf("set-cookie not redacted in resp headers: %s", got)
	}
	if got := string(h.ResponseHeaders); !strings.Contains(got, "X-Request-Id") {
		t.Fatalf("benign resp header dropped: %s", got)
	}
}

func TestBuildKiroCaptureHeadersNilSafe(t *testing.T) {
	if h := buildKiroCaptureHeaders(nil); h.RequestHeaders != nil || h.ResponseHeaders != nil {
		t.Fatal("nil resp -> empty")
	}
	// resp without Request -> only response headers
	h := buildKiroCaptureHeaders(&http.Response{Header: http.Header{"X-A": []string{"1"}}})
	if h.RequestHeaders != nil {
		t.Fatal("no request -> nil request headers")
	}
	if h.ResponseHeaders == nil {
		t.Fatal("response headers expected")
	}
}

func TestStashAndTakeKiroCaptureHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	resp := &http.Response{
		Header:  http.Header{"X-Request-Id": []string{"req_up"}},
		Request: &http.Request{Header: http.Header{"Anthropic-Version": []string{"2023-06-01"}}},
	}
	stashKiroCaptureHeaders(c, resp)
	reqH, respH := takeKiroCaptureHeaders(c)
	if reqH == nil || respH == nil {
		t.Fatal("stashed headers must be retrievable")
	}
}

func TestTakeKiroCaptureHeadersEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	if reqH, respH := takeKiroCaptureHeaders(c); reqH != nil || respH != nil {
		t.Fatal("unset -> nil,nil")
	}
}
