//go:build unit

package service

import (
	"fmt"
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

func TestAnthropicCompatUpstreamObservation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "responses"} {
		for _, streaming := range []bool{false, true} {
			for _, scenario := range []string{"normal", "missing", "conflict", "partial-error"} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", protocol, streaming, scenario), func(t *testing.T) {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
					beginUpstreamResponseModelObservation(c).Observe("stale-model", false)
					body := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"actual-model\",\"usage\":{\"input_tokens\":3,\"speed\":\"fast\"}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

					expectedModel, expectedTier := "actual-model", "fast"
					switch scenario {
					case "missing":
						body = strings.ReplaceAll(body, `,"model":"actual-model"`, "")
						body = strings.ReplaceAll(body, `,"speed":"fast"`, "")
						expectedModel, expectedTier = "", ""
					case "conflict":
						body = strings.Replace(body, `"type":"message_delta","delta"`, `"type":"message_delta","model":"other-model","delta"`, 1)
					case "partial-error":
						body = body[:strings.Index(body, "event: message_delta")]
						body += "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"
						body += "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"
					}
					resp := &http.Response{Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
					svc := &GatewayService{}
					var result *ForwardResult
					var err error
					if protocol == "chat" {
						if streaming {
							result, err = svc.handleCCStreamingFromAnthropic(ginRequestContext(c), resp, c, "public-model", "sent-model", nil, time.Now(), true, false)
						} else {
							result, err = svc.handleCCBufferedFromAnthropic(resp, c, "public-model", "sent-model", nil, time.Now(), false)
						}
					} else {
						if streaming {
							result, err = svc.handleResponsesStreamingResponse(resp, c, "public-model", "sent-model", nil, time.Now(), false, apicompat.ResponsesClientToolMapping{})
						} else {
							result, err = svc.handleResponsesBufferedStreamingResponse(resp, c, "public-model", "sent-model", nil, time.Now(), false, apicompat.ResponsesClientToolMapping{})
						}
					}
					if scenario == "partial-error" {
						require.Error(t, err)
						if !streaming {
							var failover *UpstreamFailoverError
							require.ErrorAs(t, err, &failover)
							require.Nil(t, result)
							require.Equal(t, expectedModel, observedUpstreamResponseModel(c))
							require.Equal(t, expectedTier, observedUpstreamResponseServiceTier(c))
							return
						}
					} else {
						require.NoError(t, err)
					}
					require.NotNil(t, result)
					require.Equal(t, expectedModel, result.UpstreamResponseModel)
					require.Equal(t, expectedTier, result.UpstreamResponseServiceTier)
					require.Equal(t, scenario == "conflict", result.UpstreamResponseModelConflict)
					require.Equal(t, "sent-model", result.UpstreamModel)
					require.Equal(t, "public-model", result.Model)
				})
			}
		}
	}
}
