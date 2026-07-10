//go:build unit

package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCopyOpenAIUsageFromResponsesUsageTrustsCanonicalCacheCreationValue(t *testing.T) {
	usage := &apicompat.ResponsesUsage{
		InputTokens:              20,
		OutputTokens:             2,
		CacheCreationInputTokens: 0,
		InputTokensDetails: &apicompat.ResponsesInputTokensDetails{
			CachedTokens:     3,
			CacheWriteTokens: 19,
		},
	}

	got := copyOpenAIUsageFromResponsesUsage(usage)

	require.Equal(t, 20, got.InputTokens)
	require.Equal(t, 3, got.CacheReadInputTokens)
	require.Zero(t, got.CacheCreationInputTokens)
}

func TestResponsesUsageFromCCStreamUsagePreservesCacheWriteDetails(t *testing.T) {
	t.Parallel()

	got := responsesUsageFromCCStreamUsage(OpenAIUsage{
		InputTokens:              12,
		OutputTokens:             3,
		CacheReadInputTokens:     4,
		CacheCreationInputTokens: 6,
	})

	require.NotNil(t, got)
	require.Equal(t, 12, got.InputTokens)
	require.Equal(t, 3, got.OutputTokens)
	require.Equal(t, 15, got.TotalTokens)
	require.Equal(t, 6, got.CacheCreationInputTokens)
	require.NotNil(t, got.InputTokensDetails)
	require.Equal(t, 4, got.InputTokensDetails.CachedTokens)
	require.Equal(t, 6, got.InputTokensDetails.CacheWriteTokens)
}

func TestStreamChatCompletionsAsAnthropicPreservesRawUsageAliasesAndKiroCredits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	payload := `{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"glm-5.2","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"cache_creation_input_tokens":6,"cache_read_input_tokens":4,"_sub2api_kiro_credits":0.17}}`
	resp := &http.Response{
		Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			"data: " + payload + "\n\ndata: [DONE]\n\n",
		)),
	}

	svc := &OpenAIGatewayService{}
	result, err := svc.streamChatCompletionsAsAnthropic(
		c,
		resp,
		"claude-test",
		"claude-test",
		"glm-5.2",
		nil,
		nil,
		time.Now(),
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 6, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
	require.InDelta(t, 0.17, result.Usage.KiroCredits, 0.000001)

	wire := recorder.Body.String()
	require.Contains(t, wire, `"cache_creation_input_tokens":6`)
	require.Contains(t, wire, `"cache_read_input_tokens":4`)
}
