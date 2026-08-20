package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFinalizeGeminiForwardResultMissingUsageMarksDurableFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	result := &ForwardResult{Model: "gemini-test", Stream: false}

	got, err := finalizeGeminiForwardResult(c, result, nil, "generateContent")

	require.Same(t, result, got)
	require.ErrorIs(t, err, ErrUpstreamUsageMissing)
	require.True(t, result.UpstreamFailed)
	require.True(t, result.CaptureTerminalError)
	require.True(t, result.CaptureResponseComplete)
	marked, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "upstream_error", marked.ErrType)
	require.Equal(t, upstreamUsageMissingErrorCode, marked.Code)
	require.Equal(t, http.StatusBadGateway, marked.IntendedStatus)
	require.True(t, marked.CountTowardsSLA)
	require.False(t, marked.Stream)
}

func TestFinalizeGeminiForwardResultCountTokensExemptsMissingUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	result := &ForwardResult{Model: "gemini-test"}

	got, err := finalizeGeminiForwardResult(c, result, nil, "countTokens")

	require.Same(t, result, got)
	require.NoError(t, err)
	require.False(t, result.UpstreamFailed)
	_, marked := GetOpsStreamError(c)
	require.False(t, marked)
}

func TestFinalizeGeminiForwardResultPreservesExistingError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	wantErr := errors.New("provider read failed")
	result := &ForwardResult{Model: "gemini-test", Stream: true}

	_, err := finalizeGeminiForwardResult(c, result, wantErr, "streamGenerateContent")

	require.ErrorIs(t, err, wantErr)
	_, marked := GetOpsStreamError(c)
	require.False(t, marked, "an existing provider error keeps its original classification")
}
