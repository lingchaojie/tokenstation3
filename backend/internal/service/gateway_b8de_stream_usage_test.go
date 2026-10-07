//go:build unit

package service

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestB8deKiroFinalUsageHonorsClientOptIn(t *testing.T) {
	for _, include := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[include], func(t *testing.T) {
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			result, err := (&GatewayService{}).handleCCStreamingFromAnthropic(
				context.Background(), markedKiroFinalUsageAnthropicResponse("kiro_usage_opt_in"),
				c, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), include, true,
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Zero(t, result.Usage.InputTokens, "marked final zero must replace initial input for billing")
			require.Equal(t, 120, result.Usage.CacheReadInputTokens)
			require.Equal(t, include, strings.Contains(writer.Body.String(), `"usage":`))
			require.Equal(t, 1, strings.Count(writer.Body.String(), "data: [DONE]"))
		})
	}
}
