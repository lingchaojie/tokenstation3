//go:build unit

package handler

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesWebSocket_CaptureModelAllowlistPerTurn(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		for _, tc := range []struct {
			name, first, second string
			want                int
		}{
			{"astra_to_sol", "gpt-6-astra", "gpt-5.6-sol", 1},
			{"sol_to_astra", "gpt-5.6-sol", "gpt-6-astra", 1},
			{"inherited_astra", "gpt-6-astra", "", 2},
			{"inherited_sol", "gpt-5.6-sol", "", 0},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				second := `{"type":"response.create","stream":false}`
				if tc.second != "" {
					second = fmt.Sprintf(`{"type":"response.create","model":%q,"stream":false}`, tc.second)
				}
				got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
					firstPayload:  fmt.Sprintf(`{"type":"response.create","model":%q,"stream":false}`, tc.first),
					secondPayload: second,
					ingressMode:   mode,
					captureSetup:  newAstraOnlyOpenAIWSCaptureForTest,
					// Both public models route to Astra; only the requested model grants capture.
					channelMapping:      map[string]string{"gpt-6-astra": "gpt-6-astra", "gpt-5.6-sol": "gpt-6-astra"},
					accountModelMapping: map[string]any{"gpt-6-astra": "gpt-6-astra"},
				})
				require.Len(t, got.logs, 2)
				require.Len(t, got.captures, tc.want)
				for _, record := range got.captures {
					require.Contains(t, string(record.RawRequest), "gpt-6-astra")
					require.Contains(t, string(record.RawResponse), "response.completed")
				}
			})
		}
	}
}

func newAstraOnlyOpenAIWSCaptureForTest(t *testing.T, cfg *config.Config) (*service.SettingService, *service.ConversationCapturePool, chan *service.CaptureRecord) {
	t.Helper()
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 8 << 20
	settings := newEnabledCaptureSettingService(t, cfg)
	policy := service.DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	policy.ModelAllowlists.OpenAI = []string{"gpt-6-astra"}
	_, err := settings.UpdateCaptureRuntimePolicy(context.Background(), policy)
	require.NoError(t, err)
	records := make(chan *service.CaptureRecord, 2)
	pool := service.NewConversationCapturePoolForUnitTest(records)
	t.Cleanup(pool.Stop)
	return settings, pool, records
}
