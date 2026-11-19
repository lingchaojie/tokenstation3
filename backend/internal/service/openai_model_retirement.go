package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/coder/websocket"
)

// GPT-5.5 retired from Codex with ChatGPT sign-in on 2026-10-14 at
// 19:00 UTC. API-key upstreams remain supported. Do not infer upstream
// eligibility from the API key a client uses to authenticate to this gateway.
// https://learn.chatgpt.com/docs/models#gpt-55-retirement
func isRetiredOpenAISubscriptionModel(account *Account, upstreamModel string) bool {
	if account == nil || !account.IsOpenAI() || (!account.IsOpenAIOAuthLike() && !account.IsShadow()) {
		return false
	}
	model := canonicalizeOpenAIModelAliasSpelling(upstreamModel)
	return model == "gpt-5.5" || strings.HasPrefix(model, "gpt-5.5-")
}

// Only remove entries blocked by retirement. Unknown models and other platforms
// retain their existing listing policy. An explicitly mapped replacement is
// allowed, but a public alias targeting retired GPT-5.5 is not.
func openAIModelRetiredForAccounts(accounts []Account, modelID string) bool {
	retired := false
	for i := range accounts {
		account := &accounts[i]
		if !account.IsOpenAI() {
			continue
		}
		target := resolveOpenAIAccountUpstreamModelForRequest(account, modelID, false)
		if isRetiredOpenAISubscriptionModel(account, target) {
			retired = true
		} else if account.IsModelSupported(modelID) {
			return false
		}
	}
	return retired
}

func filterRetiredOpenAIModelIDs(accounts []Account, models []string) []string {
	filtered := make([]string, 0, len(models))
	for _, model := range models {
		if !openAIModelRetiredForAccounts(accounts, model) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

// Entries must already use client-visible names, not raw upstream IDs. Resolve
// each public name exactly as routing does, after projection and before the
// final client ETag is calculated. Preserve unknown fields.
func filterRetiredOpenAIPublicModelsBody(body []byte, accounts []Account, codex bool) ([]byte, bool, error) {
	field, idField := "data", "id"
	if codex {
		field, idField = "models", "slug"
	}
	envelope, entries, err := modelCatalogEntries(body, field)
	if err != nil {
		return nil, false, err
	}
	filtered := make([]json.RawMessage, 0, len(entries))
	for _, raw := range entries {
		var entry map[string]json.RawMessage
		var id string
		if json.Unmarshal(raw, &entry) == nil {
			_ = json.Unmarshal(entry[idField], &id)
		}
		if !openAIModelRetiredForAccounts(accounts, id) {
			filtered = append(filtered, raw)
		}
	}
	if len(filtered) == len(entries) {
		return body, false, nil
	}
	envelope[field], err = json.Marshal(filtered)
	if err != nil {
		return nil, false, err
	}
	encoded, err := json.Marshal(envelope)
	return encoded, true, err
}

// OpenAICompatModelRoute identifies which model mapping is performed by an
// OpenAI-compatible forwarding endpoint.
type OpenAICompatModelRoute int

const (
	OpenAICompatRouteMapped OpenAICompatModelRoute = iota
	OpenAICompatRouteMessages
	OpenAICompatRouteMessagesCountTokens
)

type openAICompatModelRoutingKey struct{}
type openAICompactModelRoutingKey struct{}

type openAICompatModelRouting struct {
	route           OpenAICompatModelRoute
	requestModel    string
	normalizedModel string
	dispatchModel   string
}

// WithOpenAICompatModelRouting carries the channel-mapped body model and the
// dispatch fallback into account selection. The ordinary selection model still
// controls existing eligibility and ranking. Retirement uses the model that
// this endpoint's conversion will send.
func WithOpenAICompatModelRouting(ctx context.Context, route OpenAICompatModelRoute, bodyModel, dispatchModel string) context.Context {
	normalizedModel := bodyModel
	if route == OpenAICompatRouteMessages || route == OpenAICompatRouteMessagesCountTokens {
		req := apicompat.AnthropicRequest{Model: bodyModel}
		applyOpenAICompatModelNormalization(&req)
		normalizedModel = req.Model
	}
	return context.WithValue(ctx, openAICompatModelRoutingKey{}, openAICompatModelRouting{
		route: route, requestModel: bodyModel, normalizedModel: normalizedModel, dispatchModel: dispatchModel,
	})
}

func isRetiredOpenAIRoutingModel(ctx context.Context, account *Account, requestedModel string, requireCompact bool) bool {
	if account == nil || !account.IsOpenAI() || (!account.IsOpenAIOAuthLike() && !account.IsShadow()) {
		return false
	}
	if route, ok := ctx.Value(openAICompatModelRoutingKey{}).(openAICompatModelRouting); ok {
		var mapped string
		if route.route == OpenAICompatRouteMessages {
			mapped = resolveOpenAIAnthropicCompatBillingModel(account, route.requestModel, route.normalizedModel, route.dispatchModel)
		} else {
			mapped = resolveOpenAIForwardModel(account, route.normalizedModel, route.dispatchModel)
		}
		return isRetiredOpenAISubscriptionModel(account, normalizeOpenAIModelForUpstream(account, mapped))
	}
	// Account refresh may delay the compact capability check, but retirement
	// still uses the same account override or configured fallback as Forward.
	compactOverride, compactRoute := ctx.Value(openAICompactModelRoutingKey{}).(func(*Account, string) string)
	if compactRoute {
		if model := compactOverride(account, requestedModel); model != "" {
			return isRetiredOpenAISubscriptionModel(account, model)
		}
	}
	return isRetiredOpenAISubscriptionModel(account, resolveOpenAIAccountUpstreamModelForRequest(account, requestedModel, requireCompact || compactRoute))
}

func supportsOpenAIRoutingModel(ctx context.Context, account *Account, requestedModel string, requireCompact bool) bool {
	return !isRetiredOpenAIRoutingModel(ctx, account, requestedModel, requireCompact) && account.isModelMappingSupported(requestedModel)
}

func supportsOpenAIRequestModel(account *Account, requestedModel string, requireCompact bool) bool {
	return supportsOpenAIRoutingModel(context.Background(), account, requestedModel, requireCompact)
}

var ErrOpenAIWSUnsupportedModelSwitch = errors.New("selected account does not support websocket model switch")

func NewOpenAIWSUnsupportedModelSwitchError(model string) error {
	cause := fmt.Errorf("%w: model %q", ErrOpenAIWSUnsupportedModelSwitch, strings.TrimSpace(model))
	return NewOpenAIWSClientCloseError(websocket.StatusPolicyViolation, "model switch requires reconnect", cause)
}

func validateOpenAIWSAccountModels(account *Account, requestedModel, upstreamModel string) error {
	if account == nil || !account.IsOpenAI() {
		return nil
	}
	if isRetiredOpenAISubscriptionModel(account, upstreamModel) {
		return NewOpenAIWSUnsupportedModelSwitchError(upstreamModel)
	}
	if requestedModel != "" && account.IsOpenAIPassthroughModelFilterEnabled() && !account.isModelMappingSupported(requestedModel) {
		return NewOpenAIWSUnsupportedModelSwitchError(requestedModel)
	}
	return nil
}
