package handler

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestInboundProviderFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		// Anthropic SDK / default /v1 surfaces
		{"/v1/messages", service.PlatformAnthropic},
		{"/v1/messages/count_tokens", service.PlatformAnthropic},
		{"/v1/models", service.PlatformAnthropic},
		{"/v1/usage", service.PlatformAnthropic},
		{"/antigravity/v1/messages", service.PlatformAnthropic},
		// OpenAI SDK surfaces
		{"/v1/chat/completions", service.PlatformOpenAI},
		{"/chat/completions", service.PlatformOpenAI},
		{"/v1/responses", service.PlatformOpenAI},
		{"/v1/responses/compact", service.PlatformOpenAI},
		{"/responses", service.PlatformOpenAI},
		{"/backend-api/codex/responses", service.PlatformOpenAI},
		{"/v1/embeddings", service.PlatformOpenAI},
		{"/v1/images/generations", service.PlatformOpenAI},
		{"/v1/images/edits", service.PlatformOpenAI},
		// Gemini — out of unified scope
		{"/v1beta/models", ""},
		{"/v1beta/models/gemini-2.5-pro:generateContent", ""},
	}
	for _, c := range cases {
		if got := InboundProviderFromPath(c.path); got != c.want {
			t.Errorf("InboundProviderFromPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestInboundEndpointMiddlewareStoresIngressModelAndRestoresBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(InboundEndpointMiddleware())

	var gotProvider string
	var gotModel string
	var gotBody string
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		gotProvider, _ = c.Request.Context().Value(ctxkey.IngressProvider).(string)
		gotModel, _ = c.Request.Context().Value(ctxkey.IngressModel).(string)
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatalf("read restored body: %v", err)
		}
		gotBody = string(body)
		c.Status(http.StatusNoContent)
	})

	body := []byte(`{"model":"claude-opus-4-7","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if gotProvider != service.PlatformOpenAI {
		t.Fatalf("provider = %q, want %q", gotProvider, service.PlatformOpenAI)
	}
	if gotModel != "claude-opus-4-7" {
		t.Fatalf("model = %q, want claude-opus-4-7", gotModel)
	}
	if gotBody != string(body) {
		t.Fatalf("restored body mismatch: got %q want %q", gotBody, body)
	}
}
