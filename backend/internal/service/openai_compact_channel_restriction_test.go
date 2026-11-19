//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestR16CompactConfiguredModelMatchesChannelRestriction(t *testing.T) {
	const requested = "gpt-5.5"
	const configured = "gpt-6.1-sol"
	groupID := int64(12001)
	for _, tt := range []struct {
		name          string
		listedModel   string
		wantSelection bool
	}{
		{name: "configured model is listed", listedModel: configured, wantSelection: true},
		{name: "submitted model only is listed", listedModel: requested, wantSelection: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, scheduler := range []struct {
				name                             string
				loadBatch, advanced, passthrough bool
			}{
				{name: "legacy"},
				{name: "load batch", loadBatch: true},
				{name: "advanced", loadBatch: true, advanced: true},
				{name: "legacy passthrough", passthrough: true},
			} {
				t.Run(scheduler.name, func(t *testing.T) {
					account := Account{ID: 12002, Type: AccountTypeOAuth, Platform: PlatformOpenAI,
						Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID},
						Credentials: map[string]any{"access_token": "offline-test"},
						Extra:       map[string]any{"openai_compact_mode": OpenAICompactModeForceOn, "openai_passthrough": scheduler.passthrough},
					}
					cfg := &config.Config{RunMode: config.RunModeStandard}
					cfg.Gateway.Scheduling.LoadBatchEnabled = scheduler.loadBatch
					cfg.Gateway.OpenAICompactModel = configured
					channelService := newTestChannelService(makeStandardRepo(Channel{
						ID: 1, Status: StatusActive, GroupIDs: []int64{groupID}, RestrictModels: true,
						BillingModelSource: BillingModelSourceUpstream,
						ModelPricing:       []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{tt.listedModel}}},
					}, map[int64]string{groupID: PlatformOpenAI}))
					transport := &httpUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"id":"compact","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
					}}
					svc := &OpenAIGatewayService{cfg: cfg, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
						concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{account.ID: true}}),
						channelService:     channelService, httpUpstream: transport,
					}
					if scheduler.advanced {
						resetOpenAIAdvancedSchedulerSettingCacheForTest()
						t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
						svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
					}
					ctx := WithOpenAIForwardModel(context.Background(), requested, true)
					ctx = WithOpenAIChannelRequestModel(ctx, &groupID, requested)
					require.True(t, svc.needsUpstreamChannelRestrictionCheck(ctx, &groupID))
					require.Equal(t, !tt.wantSelection, channelService.IsModelRestricted(ctx, groupID, configured))
					selected, decision, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "", requested,
						nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, true, false, true)
					if selected != nil && selected.ReleaseFunc != nil {
						t.Cleanup(selected.ReleaseFunc)
					}
					if tt.wantSelection {
						assert.NoError(t, err)
						assert.NotNil(t, selected)
					} else {
						assert.Error(t, err)
						assert.Nil(t, selected)
					}

					forwarded := &account
					if selected != nil {
						forwarded = selected.Account
					}
					c := newOpenAICompactFallbackTestContext(t, "/v1/responses/compact")
					c.Request = c.Request.WithContext(ctx)
					_, err = svc.Forward(ctx, c, forwarded, []byte(`{"model":"`+requested+`","input":[],"instructions":"compact","stream":false}`))
					require.NoError(t, err)
					require.Equal(t, configured, gjson.GetBytes(transport.lastBody, "model").String())
					t.Logf("decision=%+v actual upstream model=%s", decision, configured)
				})
			}
		})
	}
}

func TestR16NativeCompactRetryRespectsOutboundModelRestriction(t *testing.T) {
	const requested = "gpt-6.1-terra"
	const configured = "gpt-6.1-sol"
	groupID := int64(12001)
	for _, passthrough := range []bool{false, true} {
		for _, allowed := range []bool{false, true} {
			name := "mapped"
			if passthrough {
				name = "passthrough"
			}
			if allowed {
				name += "/fallback listed"
			} else {
				name += "/fallback absent"
			}
			t.Run(name, func(t *testing.T) {
				models := []string{requested}
				if allowed {
					models = append(models, configured)
				}
				account := Account{ID: 12002, Type: AccountTypeOAuth, Platform: PlatformOpenAI,
					Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID},
					Credentials: map[string]any{"access_token": "offline-test"},
					Extra:       map[string]any{"openai_passthrough": passthrough},
				}
				channelService := newTestChannelService(makeStandardRepo(Channel{
					ID: 1, Status: StatusActive, GroupIDs: []int64{groupID}, RestrictModels: true,
					BillingModelSource: BillingModelSourceUpstream,
					ModelPricing:       []ChannelModelPricing{{Platform: PlatformOpenAI, Models: models}},
				}, map[int64]string{groupID: PlatformOpenAI}))
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"error":{"code":"context_length_exceeded","message":"context window exceeded"}}`))},
					{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"id":"compact","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`))},
				}}
				cfg := &config.Config{RunMode: config.RunModeStandard}
				cfg.Gateway.OpenAICompactModel = configured
				svc := &OpenAIGatewayService{cfg: cfg, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}},
					concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{account.ID: true}}),
					channelService:     channelService, httpUpstream: upstream,
				}
				ctx := WithOpenAIForwardModel(context.Background(), requested, false)
				require.Equal(t, !allowed, channelService.IsModelRestricted(ctx, groupID, configured))
				selected, _, err := svc.SelectAccountWithSchedulerForCapability(WithOpenAIChannelRequestModel(ctx, &groupID, requested),
					&groupID, "", "", requested, nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, false, false, true)
				require.NoError(t, err)
				require.NotNil(t, selected)
				if selected.ReleaseFunc != nil {
					t.Cleanup(selected.ReleaseFunc)
				}
				c := newOpenAICompactFallbackTestContext(t, "/v1/responses")
				c.Request = c.Request.WithContext(ctx)
				c.Set("api_key", &APIKey{ID: 42, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformOpenAI}})
				MarkOpenAINativeCompactionV2(c)
				_, err = svc.Forward(ctx, c, selected.Account, []byte(`{"model":"`+requested+`","input":[{"type":"compaction_trigger"}],"instructions":"compact","stream":false}`))
				require.NotEmpty(t, upstream.bodies)
				require.Equal(t, requested, gjson.GetBytes(upstream.bodies[0], "model").String())
				if allowed {
					require.NoError(t, err)
					require.Len(t, upstream.bodies, 2)
					require.Equal(t, configured, gjson.GetBytes(upstream.bodies[1], "model").String())
					return
				}
				assert.Error(t, err)
				assert.Len(t, upstream.bodies, 1)
				if len(upstream.bodies) > 1 {
					t.Logf("unexpected retry model=%s", gjson.GetBytes(upstream.bodies[1], "model").String())
				}
			})
		}
	}
}
