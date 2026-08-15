package handler

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBuildOpenAICaptureRecordUsesFinalAttemptAndCombinesRequestTruncation(t *testing.T) {
	result := &service.OpenAIForwardResult{
		Model:           "requested",
		UpstreamModel:   "mapped",
		UpstreamRequest: []byte(`{"model":"mapped","reasoning":{"effort":"high"}}`),
		CaptureResponse: []byte(`{"ok":true}`),
	}
	record := buildOpenAICaptureRecord(result, &service.Account{Platform: service.PlatformOpenAI}, []byte(`{"model":"inbound"}`), "/v1/responses", 12)

	require.NotNil(t, record)
	require.Equal(t, result.UpstreamRequest[:12], record.RawRequest)
	require.NotContains(t, string(record.RawRequest), "inbound")
	require.True(t, record.Truncated, "request-only truncation must mark the combined record")
}

func TestFinalOpenAIUpstreamRequestFallsBackOnlyWithoutAttemptSnapshot(t *testing.T) {
	fallback := []byte(`{"model":"inbound"}`)
	require.Equal(t, fallback, finalOpenAIUpstreamRequest(nil, fallback))
	result := &service.OpenAIForwardResult{UpstreamRequest: []byte(`{"model":"wire"}`)}
	require.Equal(t, []byte(`{"model":"wire"}`), finalOpenAIUpstreamRequest(result, fallback))
	require.Equal(t, service.HashUsageRequestPayload(result.UpstreamRequest), hashFinalOpenAIUpstreamRequest(result, fallback), "normal and cyber sinks share the final-attempt hash helper")
	require.NotEqual(t, service.HashUsageRequestPayload(fallback), hashFinalOpenAIUpstreamRequest(result, fallback))
}

func TestBuildOpenAICaptureRecordUsesFinalUpstreamStatusWithLegacySuccessDefault(t *testing.T) {
	account := &service.Account{Platform: service.PlatformGrok}
	failed := &service.OpenAIForwardResult{
		UpstreamFailed:     true,
		UpstreamHTTPStatus: http.StatusBadRequest,
		UpstreamRequest:    []byte(`{}`),
		CaptureResponse:    []byte(`{"error":"bad request"}`),
	}
	require.Equal(t, http.StatusBadRequest, buildOpenAICaptureRecord(failed, account, nil, "/v1/responses", 1024).HTTPStatus)

	legacySuccess := &service.OpenAIForwardResult{UpstreamRequest: []byte(`{}`), CaptureResponse: []byte(`{"ok":true}`)}
	require.Equal(t, http.StatusOK, buildOpenAICaptureRecord(legacySuccess, account, nil, "/v1/responses", 1024).HTTPStatus)
}

func TestBuildOpenAICaptureRecordGeneratesRequestIDWhenProviderOmitsIt(t *testing.T) {
	result := &service.OpenAIForwardResult{UpstreamRequest: []byte{}, CaptureResponse: []byte{}}
	account := &service.Account{Platform: service.PlatformOpenAI}
	first := buildOpenAICaptureRecord(result, account, nil, "/v1/responses", 1024)
	second := buildOpenAICaptureRecord(result, account, nil, "/v1/responses", 1024)
	require.NotEmpty(t, first.RequestID)
	require.NotEmpty(t, second.RequestID)
	require.NotEqual(t, first.RequestID, second.RequestID)
}

func TestBuildOpenAICaptureRecordRejectsMissingProviderRequest(t *testing.T) {
	result := &service.OpenAIForwardResult{CaptureResponse: []byte(`{"ok":true}`)}
	require.Nil(t, buildOpenAICaptureRecord(result, &service.Account{Platform: service.PlatformOpenAI}, []byte(`{"model":"inbound"}`), "/v1/responses", 1024))
}
