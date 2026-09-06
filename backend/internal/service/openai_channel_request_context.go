package service

import "context"

type openAIChannelRequestModelKey struct{}

type openAIChannelRequestModel struct {
	groupID int64
	model   string
	valid   bool
}

// WithOpenAIChannelRequestModel preserves the original model for channel
// admission while the scheduler uses the mapped model for account capability
// selection. It does not change forwarding or billing, and applies only to the
// supplied group. Bind it to the selection call, not the whole request lifetime.
func WithOpenAIChannelRequestModel(ctx context.Context, groupID *int64, requestedModel string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	value := openAIChannelRequestModel{}
	if groupID != nil && requestedModel != "" {
		value = openAIChannelRequestModel{groupID: *groupID, model: requestedModel, valid: true}
	}
	// An invalid binding deliberately masks any earlier binding.
	return context.WithValue(ctx, openAIChannelRequestModelKey{}, value)
}

func openAIChannelRequestModelFromContext(ctx context.Context, groupID int64) (string, bool) {
	if ctx == nil {
		return "", false
	}
	value, ok := ctx.Value(openAIChannelRequestModelKey{}).(openAIChannelRequestModel)
	return value.model, ok && value.valid && value.groupID == groupID
}
