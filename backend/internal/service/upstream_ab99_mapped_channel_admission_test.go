//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The HTTP handlers now pass the channel-mapped model to the scheduler for
// capability selection. Its early pricing gate must still honor the configured
// requested-model basis (approved D13 compatibility correction).
func TestUpstreamAB99MappedSchedulingPreservesRequestedChannelAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, pricedModel string
		wantRestricted    bool
	}{
		{"configured public alias remains admitted", "public-coding", false},
		{"unconfigured public alias remains restricted", "gpt-5.6-sol", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channelSvc := newTestChannelService(makeStandardRepo(Channel{
				ID: 1, Status: StatusActive, GroupIDs: []int64{10}, RestrictModels: true,
				BillingModelSource: BillingModelSourceRequested,
				ModelPricing:       []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{tc.pricedModel}}},
				ModelMapping:       map[string]map[string]string{PlatformOpenAI: {"public-coding": "gpt-5.6-sol"}},
			}, map[int64]string{10: PlatformOpenAI}))
			svc := &OpenAIGatewayService{channelService: channelSvc}
			ctx := context.Background()
			groupID := int64(10)
			mapping := channelSvc.ResolveChannelMapping(ctx, groupID, "public-coding")
			require.True(t, mapping.Mapped)
			require.Equal(t, "gpt-5.6-sol", mapping.MappedModel)
			require.Equal(t, tc.wantRestricted, svc.checkChannelPricingRestriction(ctx, &groupID, "public-coding"),
				"the configured requested-model contract before capability routing")
			// Exact value now supplied by Responses/ChatCompletions/WS handlers
			// to SelectAccountWithSchedulerForCapability and its early gate.
			ctx = WithOpenAIChannelRequestModel(ctx, &groupID, "public-coding")
			require.Equal(t, tc.wantRestricted, svc.checkChannelPricingRestriction(ctx, &groupID, mapping.MappedModel),
				"capability routing must not change channel admission")
		})
	}
}

func TestUpstreamAB99ChannelMappedAdmissionDoesNotMapTwice(t *testing.T) {
	channelSvc := newTestChannelService(makeStandardRepo(Channel{
		ID: 1, Status: StatusActive, GroupIDs: []int64{10}, RestrictModels: true,
		BillingModelSource: BillingModelSourceChannelMapped,
		ModelPricing:       []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-5.6-sol"}}},
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {
			"public-coding": "gpt-5.6-sol", "gpt-5.6-sol": "unpriced-second-hop",
		}},
	}, map[int64]string{10: PlatformOpenAI}))
	svc := &OpenAIGatewayService{channelService: channelSvc}
	groupID := int64(10)
	ctx := WithOpenAIChannelRequestModel(context.Background(), &groupID, "public-coding")
	require.False(t, svc.checkChannelPricingRestriction(ctx, &groupID, "gpt-5.6-sol"))
	// A plain internal caller still supplies its original request model.
	require.True(t, svc.checkChannelPricingRestriction(context.Background(), &groupID, "gpt-5.6-sol"))
}

func TestUpstreamAB99ChannelAdmissionContextIsGroupScoped(t *testing.T) {
	channelSvc := newTestChannelService(makeStandardRepo(Channel{
		ID: 1, Status: StatusActive, GroupIDs: []int64{10, 20}, RestrictModels: true,
		BillingModelSource: BillingModelSourceRequested,
		ModelPricing:       []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"allowed"}}},
	}, map[int64]string{10: PlatformOpenAI, 20: PlatformOpenAI}))
	svc := &OpenAIGatewayService{channelService: channelSvc}
	groupID, otherGroupID := int64(10), int64(20)
	ctx := WithOpenAIChannelRequestModel(context.Background(), &groupID, "allowed")
	require.False(t, svc.checkChannelPricingRestriction(ctx, &groupID, "restricted"))
	require.True(t, svc.checkChannelPricingRestriction(ctx, &otherGroupID, "restricted"))
	require.True(t, svc.checkChannelPricingRestriction(WithOpenAIChannelRequestModel(ctx, &groupID, ""), &groupID, "restricted"))
	require.True(t, svc.checkChannelPricingRestriction(WithOpenAIChannelRequestModel(ctx, nil, "allowed"), &groupID, "restricted"))
}

func TestUpstreamAB99MappedSchedulingSelectsAccountUsingMappedModel(t *testing.T) {
	for _, source := range []string{BillingModelSourceRequested, BillingModelSourceChannelMapped, BillingModelSourceUpstream} {
		t.Run(source, func(t *testing.T) {
			pricedModel := "public-coding"
			switch source {
			case BillingModelSourceChannelMapped:
				pricedModel = "gpt-5.6-sol"
			case BillingModelSourceUpstream:
				pricedModel = "allowed-provider-model"
			}
			channelSvc := newTestChannelService(makeStandardRepo(Channel{
				ID: 1, Status: StatusActive, GroupIDs: []int64{10}, RestrictModels: true,
				BillingModelSource: source,
				ModelPricing:       []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{pricedModel}}},
				ModelMapping:       map[string]map[string]string{PlatformOpenAI: {"public-coding": "gpt-5.6-sol"}},
			}, map[int64]string{10: PlatformOpenAI}))
			svc := &OpenAIGatewayService{
				channelService: channelSvc,
				accountRepo: stubOpenAIAccountRepo{accounts: []Account{
					{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Priority: 10,
						Credentials: map[string]any{"model_mapping": map[string]any{"public-coding": "wrong-model"}}},
					{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Priority: 20,
						Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.6-sol": "allowed-provider-model"}}},
				}},
			}
			groupID := int64(10)
			ctx := WithOpenAIChannelRequestModel(context.Background(), &groupID, "public-coding")
			account, err := svc.SelectAccountForModelWithExclusions(ctx, &groupID, "", "gpt-5.6-sol", nil)
			require.NoError(t, err)
			require.NotNil(t, account)
			require.Equal(t, int64(2), account.ID, "admission context must not change account model selection")
		})
	}
}
