package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUpstreamAB99AnthropicPolicySupportsOpenAIShape(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	key := &service.APIKey{Group: &service.Group{
		Platform: service.PlatformAnthropic, MaxReasoningEffort: "xhigh",
	}}
	got, changed, err := applyOpenAIReasoningEffortPolicyForRequest(c, key, []byte(`{"reasoning":{"effort":"max"}}`))
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "xhigh", gjson.GetBytes(got, "reasoning.effort").String())
}

func TestUpstreamAB99AnthropicPolicyNormalizesUnsupportedMinimal(t *testing.T) {
	mappings := []service.ReasoningEffortMapping{{From: "max", To: "minimal"}}
	maxEffort, normalized := anthropicCompatibleReasoningEffortPolicy("minimal", mappings)
	require.Equal(t, "low", maxEffort)
	require.Equal(t, []service.ReasoningEffortMapping{{From: "max", To: "low"}}, normalized)
	require.Equal(t, "minimal", mappings[0].To, "normalization must not mutate group configuration")
}

func TestUpstreamAB99ReasoningPolicyPreservesRequestedEffort(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		for _, policy := range []struct {
			name, ceiling, action, want string
			deny                        bool
		}{
			{name: "unlimited", action: "deny", want: "max"},
			{name: "default downgrade", ceiling: "high", want: "high"},
			{name: "explicit deny", ceiling: "high", action: "deny", want: "max", deny: true},
		} {
			t.Run(platform+"/"+policy.name, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				key := &service.APIKey{Group: &service.Group{
					Platform: platform, MaxReasoningEffort: policy.ceiling,
					MaxReasoningEffortOverLimit: policy.action,
				}}
				body := []byte(`{"model":"gpt-5.4","reasoning":{"effort":"max"}}`)
				path := "reasoning.effort"
				apply := applyOpenAIReasoningEffortPolicyForRequest
				if platform == service.PlatformAnthropic {
					body = []byte(`{"model":"claude-fable-5-1","output_config":{"effort":"max"}}`)
					path = "output_config.effort"
					apply = applyAnthropicReasoningEffortPolicyForRequest
					// Messages captures the original effort before applying its policy.
					bindRequestedReasoningEffort(c, body, "claude-fable-5-1")
				}
				result, changed, err := apply(c, key, body)
				if policy.deny {
					var denied *service.ReasoningEffortOverLimitError
					require.ErrorAs(t, err, &denied)
					require.Equal(t, body, result)
					require.False(t, changed)
				} else {
					require.NoError(t, err)
					require.Equal(t, policy.want != "max", changed)
				}
				require.Equal(t, policy.want, gjson.GetBytes(result, path).String())
				requested := service.RequestedReasoningEffortFromContext(c.Request.Context())
				require.NotNil(t, requested)
				require.Equal(t, "max", *requested)
				require.Equal(t, policy.ceiling, key.Group.MaxReasoningEffort)
			})
		}
	}
}
