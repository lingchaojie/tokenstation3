//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAICompactSchedulerAppliesRetirementToForwardedModel(t *testing.T) {
	groupID := int64(12001)
	for _, tc := range []struct {
		name      string
		request   string
		target    string
		loadBatch bool
		advanced  bool
		eligible  bool
	}{
		{"supported target with batched load", "gpt-5.5", "gpt-6.1-sol", true, false, true},
		{"supported target without batched load", "gpt-5.5", "gpt-6.1-sol", false, false, true},
		{"retired target with batched load", "gpt-6.1-sol", "gpt-5.5", true, false, false},
		{"retired target without batched load", "gpt-6.1-sol", "gpt-5.5", false, false, false},
		{"supported control with batched load", "gpt-6.1-sol", "gpt-6.1-sol", true, false, true},
		{"supported control without batched load", "gpt-6.1-sol", "gpt-6.1-sol", false, false, true},
		{"supported target with advanced scheduler", "gpt-5.5", "gpt-6.1-sol", true, true, true},
		{"supported control with advanced scheduler", "gpt-6.1-sol", "gpt-6.1-sol", true, true, true},
		{"retired target with advanced scheduler", "gpt-6.1-sol", "gpt-5.5", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := Account{ID: 12002, Type: AccountTypeOAuth, Platform: PlatformOpenAI,
				Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID},
				Credentials: map[string]any{"access_token": "offline-test", "compact_model_mapping": map[string]any{tc.request: tc.target}},
				Extra:       map[string]any{"openai_compact_mode": OpenAICompactModeForceOn},
			}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Gateway.Scheduling.LoadBatchEnabled = tc.loadBatch
			cfg.Gateway.OpenAICompactModel = "gpt-5.4"
			svc := &OpenAIGatewayService{cfg: cfg, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{account.ID: true}})}
			if tc.advanced {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
				svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
			}
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(context.Background(), &groupID, "", "", tc.request,
				nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, true, false, true)
			if selection != nil && selection.ReleaseFunc != nil {
				t.Cleanup(selection.ReleaseFunc)
			}
			if !tc.eligible {
				require.Error(t, err)
				require.Nil(t, selection)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, account.ID, selection.Account.ID)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"compact","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
			}}
			svc.httpUpstream = upstream
			c := newOpenAICompactFallbackTestContext(t, "/v1/responses/compact")
			body := []byte(`{"model":"` + tc.request + `","input":[],"instructions":"compact","stream":false}`)
			_, err = svc.Forward(c.Request.Context(), c, selection.Account, body)
			require.NoError(t, err)
			require.Equal(t, tc.target, gjson.GetBytes(upstream.lastBody, "model").String())
		})
	}
}

func TestOpenAICompactSchedulerUsesConfiguredInitialOverride(t *testing.T) {
	const requested = "gpt-5.5"
	const configured = "gpt-6.1-sol"
	groupID := int64(12001)
	for _, tc := range []struct {
		name                             string
		loadBatch, advanced, passthrough bool
	}{
		{"legacy", false, false, false},
		{"load batch", true, false, false},
		{"advanced", true, true, false},
		{"passthrough legacy", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := Account{ID: 12002, Type: AccountTypeOAuth, Platform: PlatformOpenAI,
				Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID},
				Credentials: map[string]any{"access_token": "offline-test"},
				Extra:       map[string]any{"openai_compact_mode": OpenAICompactModeForceOn, "openai_passthrough": tc.passthrough},
			}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Gateway.Scheduling.LoadBatchEnabled = tc.loadBatch
			cfg.Gateway.OpenAICompactModel = configured
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"compact","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
			}}
			svc := &OpenAIGatewayService{cfg: cfg, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
				httpUpstream: upstream, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{account.ID: true}})}
			if tc.advanced {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
				svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
			}

			// Independently establish the real initial outbound model using the same account and configuration.
			c := newOpenAICompactFallbackTestContext(t, "/v1/responses/compact")
			_, err := svc.Forward(c.Request.Context(), c, &account, []byte(`{"model":"`+requested+`","input":[],"instructions":"compact","stream":false}`))
			require.NoError(t, err)
			require.Equal(t, configured, gjson.GetBytes(upstream.lastBody, "model").String())

			selection, _, err := svc.SelectAccountWithSchedulerForCapability(context.Background(), &groupID, "", "", requested,
				nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, true, false, true)
			if selection != nil && selection.ReleaseFunc != nil {
				t.Cleanup(selection.ReleaseFunc)
			}
			require.NoError(t, err)
			require.NotNil(t, selection)
			_, _, nonCompactErr := svc.SelectAccountWithSchedulerForCapability(context.Background(), &groupID, "", "", requested,
				nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, false, false, true)
			require.Error(t, nonCompactErr)
		})
	}
}
