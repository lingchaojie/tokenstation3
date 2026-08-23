package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/stretchr/testify/require"
)

func TestBuildCursorAgentRunSingleUserMessageIsByteExact(t *testing.T) {
	content := "  Hello\nworld  "
	raw, err := json.Marshal(content)
	require.NoError(t, err)
	req := &apicompat.ChatCompletionsRequest{
		Model:    "claude-4.5-sonnet",
		Messages: []apicompat.ChatMessage{{Role: "user", Content: raw}},
	}

	params, estimate, err := buildCursorAgentRunParams("claude-4.5-sonnet", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	require.Equal(t, content, params.Prompt)
	require.NotContains(t, params.Prompt, "User:")
	require.Equal(t, content, estimate.text)
	require.Equal(t, cursorpkg.AgentModeAgent, params.Mode)
	require.Equal(t, cursorpkg.AgentDefaultCwd, params.Cwd)
	require.Empty(t, params.ConversationID)
}

func TestBuildCursorAgentRunFlattensHistoryAndToolResultsInOrder(t *testing.T) {
	req := cursorToolRoundTripRequest()

	params, estimate, err := buildCursorAgentRunParams("gpt-5.2", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	require.Equal(t, "System rules\n\nDeveloper rules", params.SystemPrompt)
	require.Equal(t, "User: call the tool\n\n"+
		"Assistant: checking\n\n[tool call call-1] get_weather {\"city\":\"SF\"}\n\n"+
		"Tool result (call-1, weather): {\"ok\":true,\"temperature\":18}\n\n"+
		"User: thanks", params.Prompt)
	require.Contains(t, estimate.text, "call-1")
	require.Contains(t, estimate.text, "temperature")
	require.Contains(t, estimate.text, "get_weather")
}

func TestBuildCursorAgentRunPreservesLegacyAssistantFunctionCall(t *testing.T) {
	req := &apicompat.ChatCompletionsRequest{Messages: []apicompat.ChatMessage{
		{Role: "user", Content: json.RawMessage(`"weather"`)},
		{Role: "assistant", FunctionCall: &apicompat.ChatFunctionCall{Name: "legacy_weather", Arguments: `{"city":"SZ"}`}},
		{Role: "function", Name: "legacy_weather", Content: json.RawMessage(`"sunny"`)},
	}}

	params, _, err := buildCursorAgentRunParams("auto", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	require.Equal(t, "User: weather\n\n"+
		"Assistant: [function call] legacy_weather {\"city\":\"SZ\"}\n\n"+
		"Tool result (legacy_weather): sunny", params.Prompt)
}

func TestBuildCursorAgentRunUsesSafeNonTextFallbacks(t *testing.T) {
	req := &apicompat.ChatCompletionsRequest{Messages: []apicompat.ChatMessage{
		{Role: "user", Content: json.RawMessage(`{"kind":"voice","secret":"must-not-be-copied"}`)},
		{Role: "assistant", Content: json.RawMessage(`[{"type":"input_audio","data":"private-bytes"}]`)},
	}}

	params, estimate, err := buildCursorAgentRunParams("auto", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	require.Equal(t, "User: [content omitted: object]\n\nAssistant: [unsupported content: input_audio]", params.Prompt)
	require.NotContains(t, params.Prompt, "must-not-be-copied")
	require.NotContains(t, params.Prompt, "private-bytes")
	require.NotContains(t, estimate.text, "must-not-be-copied")
}

func TestBuildCursorAgentRunRejectsNilRequest(t *testing.T) {
	_, _, err := buildCursorAgentRunParams("auto", nil, cursorNativeTranslateOptions())
	require.ErrorContains(t, err, "nil chat request")
}

func TestBuildCursorAgentRunDeclaresNativeToolsAndDeterministicallyDeduplicates(t *testing.T) {
	req := cursorWeatherToolRequest()
	req.Tools = append(req.Tools,
		apicompat.ChatTool{Type: "function", Function: &apicompat.ChatFunction{
			Name: "get_weather", Description: "duplicate must not replace the first", Parameters: json.RawMessage(`{"type":"string"}`),
		}},
		apicompat.ChatTool{Type: "function", Function: &apicompat.ChatFunction{Name: "get_time"}},
		apicompat.ChatTool{Type: "x_search"},
	)
	req.Functions = []apicompat.ChatFunction{
		{Name: "get_weather", Description: "legacy duplicate"},
		{Name: "legacy_fn", Description: "legacy", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}}}`)},
	}

	params, firstEstimate, err := buildCursorAgentRunParams("gpt-5.2", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	require.Len(t, params.Tools, 3)
	require.Equal(t, []string{"get_weather", "get_time", "legacy_fn"}, []string{
		params.Tools[0].Name, params.Tools[1].Name, params.Tools[2].Name,
	})
	require.Equal(t, "Get weather", params.Tools[0].Description)
	require.Equal(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{"type": "string"},
		},
	}, params.Tools[0].InputSchema)
	require.Equal(t, map[string]any{"type": "object", "properties": map[string]any{}}, params.Tools[1].InputSchema)
	require.NotContains(t, params.SystemPrompt, "get_weather")

	_, secondEstimate, err := buildCursorAgentRunParams("gpt-5.2", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	require.Equal(t, firstEstimate, secondEstimate)
	require.Contains(t, firstEstimate.text, "get_weather")
	require.Contains(t, firstEstimate.text, "legacy_fn")
}

func TestBuildCursorAgentRunToolChoiceSemantics(t *testing.T) {
	tests := []struct {
		name              string
		choice            json.RawMessage
		wantTools         int
		wantInstruction   string
		forbiddenInSystem string
	}{
		{name: "auto", choice: json.RawMessage(`"auto"`), wantTools: 1},
		{name: "none", choice: json.RawMessage(`"none"`), forbiddenInSystem: "must call"},
		{name: "required", choice: json.RawMessage(`"required"`), wantTools: 1, wantInstruction: "must call at least one"},
		{name: "any alias", choice: json.RawMessage(`"any"`), wantTools: 1, wantInstruction: "must call at least one"},
		{name: "named", choice: json.RawMessage(`{"type":"function","function":{"name":"get_weather"}}`), wantTools: 1, wantInstruction: "must call the tool `get_weather`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := cursorWeatherToolRequest()
			req.ToolChoice = tt.choice
			params, _, err := buildCursorAgentRunParams("gpt-5.2", req, cursorNativeTranslateOptions())
			require.NoError(t, err)
			require.Len(t, params.Tools, tt.wantTools)
			if tt.wantInstruction != "" {
				require.Contains(t, params.SystemPrompt, tt.wantInstruction)
			}
			if tt.forbiddenInSystem != "" {
				require.NotContains(t, params.SystemPrompt, tt.forbiddenInSystem)
			}
		})
	}
}

func TestBuildCursorAgentRunRejectsMalformedOrUnsupportedToolChoice(t *testing.T) {
	tests := []struct {
		name   string
		choice json.RawMessage
		tools  bool
	}{
		{name: "malformed json", choice: json.RawMessage(`{"type":`)},
		{name: "unknown string", choice: json.RawMessage(`"sometimes"`), tools: true},
		{name: "unsupported object type", choice: json.RawMessage(`{"type":"computer"}`), tools: true},
		{name: "named choice missing name", choice: json.RawMessage(`{"type":"function","function":{}}`), tools: true},
		{name: "named choice undeclared", choice: json.RawMessage(`{"type":"function","function":{"name":"missing"}}`), tools: true},
		{name: "required without declarations", choice: json.RawMessage(`"required"`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &apicompat.ChatCompletionsRequest{
				Messages:   []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
				ToolChoice: tt.choice,
			}
			if tt.tools {
				req.Tools = cursorWeatherToolRequest().Tools
			}
			params, _, err := buildCursorAgentRunParams("auto", req, cursorNativeTranslateOptions())
			require.Error(t, err)
			require.Empty(t, params.Tools)
		})
	}
}

func TestBuildCursorAgentRunFallsBackToDeterministicToolInstruction(t *testing.T) {
	req := cursorWeatherToolRequest()
	req.ToolChoice = json.RawMessage(`"required"`)

	params, estimate, err := buildCursorAgentRunParams("gpt-5.2", req, cursorLegacyTranslateOptions())
	require.NoError(t, err)
	require.Empty(t, params.Tools)
	require.Contains(t, params.SystemPrompt, "Available tools:")
	require.Contains(t, params.SystemPrompt, `"name":"get_weather"`)
	require.Contains(t, params.SystemPrompt, "Tool choice preference")
	require.Contains(t, estimate.text, `"name":"get_weather"`)
}

func TestCursorAgentToolSchemaMalformedDegradesToEmptyObject(t *testing.T) {
	req := cursorWeatherToolRequest()
	req.Tools[0].Function.Parameters = json.RawMessage(`{"type":`)

	params, _, err := buildCursorAgentRunParams("gpt-5.2", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	require.Equal(t, map[string]any{"type": "object", "properties": map[string]any{}}, params.Tools[0].InputSchema)
}

func TestCursorImageDataURIDecodesLocallyAndPreservesMedia(t *testing.T) {
	uri, raw := cursorTestPNGDataURI(t, 30, 20)
	content, err := json.Marshal([]map[string]any{
		{"type": "text", "text": "describe exactly"},
		{"type": "image_url", "image_url": map[string]any{"url": uri}},
	})
	require.NoError(t, err)
	req := &apicompat.ChatCompletionsRequest{Messages: []apicompat.ChatMessage{{Role: "user", Content: content}}}

	params, estimate, err := buildCursorAgentRunParams("auto", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	require.Equal(t, "describe exactly", params.Prompt)
	require.Len(t, params.Images, 1)
	require.Equal(t, raw, params.Images[0].Data)
	require.Equal(t, "image/png", params.Images[0].MimeType)
	require.Equal(t, int32(30), params.Images[0].Width)
	require.Equal(t, int32(20), params.Images[0].Height)
	require.Equal(t, 30*20/cursorImageTokenDivisor, estimate.imageTokens)
}

func TestCursorImageRemoteURLIsTextOnlyFallback(t *testing.T) {
	content := json.RawMessage(`[
		{"type":"text","text":"describe"},
		{"type":"image_url","image_url":{"url":"http://127.0.0.1:1/private.png"}}
	]`)
	req := &apicompat.ChatCompletionsRequest{Messages: []apicompat.ChatMessage{{Role: "user", Content: content}}}

	params, _, err := buildCursorAgentRunParams("auto", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	require.Empty(t, params.Images)
	require.Equal(t, "describe\n[image: http://127.0.0.1:1/private.png]", params.Prompt)

	params, _, err = buildCursorAgentRunParams("auto", req, cursorLegacyTranslateOptions())
	require.NoError(t, err)
	require.Empty(t, params.Images)
	require.Equal(t, "describe\n[image: http://127.0.0.1:1/private.png]", params.Prompt)
}

func TestCursorImageMalformedOrOversizedDataURIOmitsPayload(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		content := json.RawMessage(`[{"type":"image_url","image_url":{"url":"data:image/png;base64,not/base64!"}}]`)
		text, images := cursorAgentMessageParts(apicompat.ChatMessage{Role: "user", Content: content}, true)
		require.Equal(t, "[image omitted: could not be decoded]", text)
		require.Empty(t, images)
		require.NotContains(t, text, "not/base64")
	})

	t.Run("oversized", func(t *testing.T) {
		raw := bytes.Repeat([]byte{0x42}, cursorpkg.MaxImageBytes+1)
		uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
		content, err := json.Marshal([]map[string]any{{
			"type": "image_url", "image_url": map[string]any{"url": uri},
		}})
		require.NoError(t, err)
		text, images := cursorAgentMessageParts(apicompat.ChatMessage{Role: "user", Content: content}, true)
		require.Equal(t, "[image omitted: could not be decoded]", text)
		require.Empty(t, images)
		require.Less(t, len(text), 100)
	})
}

func TestCursorImageOnlyMessageKeepsNativeAttachment(t *testing.T) {
	uri, _ := cursorTestPNGDataURI(t, 2, 2)
	content, err := json.Marshal([]map[string]any{{
		"type": "image_url", "image_url": map[string]any{"url": uri},
	}})
	require.NoError(t, err)
	req := &apicompat.ChatCompletionsRequest{Messages: []apicompat.ChatMessage{{Role: "user", Content: content}}}

	params, _, err := buildCursorAgentRunParams("auto", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	require.Empty(t, params.Prompt)
	require.Len(t, params.Images, 1)
}

func TestCursorAgentWireModelPreservesCursorIDsAndObservedThinkingVariants(t *testing.T) {
	observed := []string{
		"claude-4.5-sonnet",
		"claude-4.5-sonnet-thinking",
		"gpt-5-thinking",
	}
	tests := []struct {
		name      string
		model     string
		effort    string
		observed  []string
		wantModel string
		wantMax   bool
	}{
		{name: "empty is default", wantModel: cursorpkg.AgentDefaultModel},
		{name: "auto is default", model: "auto", effort: "high", observed: observed, wantModel: cursorpkg.AgentDefaultModel},
		{name: "default stays default", model: "DEFAULT", wantModel: cursorpkg.AgentDefaultModel},
		{name: "cursor id bypasses normalization", model: "gpt-5.4-codex", wantModel: "gpt-5.4-codex"},
		{name: "dash max is flag", model: "claude-4.5-sonnet-max", wantModel: "claude-4.5-sonnet", wantMax: true},
		{name: "colon max is flag", model: "claude-4.5-sonnet:max", wantModel: "claude-4.5-sonnet", wantMax: true},
		{name: "observed thinking wins", model: "claude-4.5-sonnet", effort: "high", observed: observed, wantModel: "claude-4.5-sonnet-thinking"},
		{name: "unobserved thinking is not invented", model: "gpt-5", effort: "high", observed: []string{"gpt-5"}, wantModel: "gpt-5"},
		{name: "already thinking is not doubled", model: "gpt-5-thinking", effort: "high", observed: observed, wantModel: "gpt-5-thinking"},
		{name: "thinking max", model: "gpt-5-thinking-max", effort: "high", observed: observed, wantModel: "gpt-5-thinking", wantMax: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, maxMode := cursorAgentWireModel(tt.model, tt.effort, tt.observed)
			require.Equal(t, tt.wantModel, model)
			require.Equal(t, tt.wantMax, maxMode)
		})
	}
}

func TestCursorAgentWireModelThinkingEffortVariants(t *testing.T) {
	for _, effort := range []string{"low", "medium", "high", "xhigh", "minimal", " HIGH "} {
		model, _ := cursorAgentWireModel("gpt-5", effort, []string{"gpt-5-thinking"})
		require.Equal(t, "gpt-5-thinking", model, "effort %q", effort)
	}
	for _, effort := range []string{"", "none", "unknown"} {
		model, _ := cursorAgentWireModel("gpt-5", effort, []string{"gpt-5-thinking"})
		require.Equal(t, "gpt-5", model, "effort %q", effort)
	}
}

func TestBuildCursorAgentRunReadsObservedModelsWithoutCredentials(t *testing.T) {
	secret := "crsr_must_never_enter_translation"
	account := &Account{
		Credentials: map[string]any{"api_key": secret, "access_token": secret},
		Extra: map[string]any{cursorObservedModelsExtraKey: map[string]any{
			"models": []any{"gpt-5", "gpt-5-thinking"},
		}},
	}
	req := &apicompat.ChatCompletionsRequest{
		ReasoningEffort: "high",
		Messages:        []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"think"`)}},
	}

	params, estimate, err := buildCursorAgentRun(account, "gpt-5", req)
	require.NoError(t, err)
	require.Equal(t, "gpt-5-thinking", params.Model)
	require.NotContains(t, params.Prompt, secret)
	require.NotContains(t, params.SystemPrompt, secret)
	require.NotContains(t, estimate.text, secret)
}

func TestCursorRequestOutputLimitBoundariesAndPrecedence(t *testing.T) {
	zero, negative := 0, -7
	legacy, completion := 128, 64
	large := int(^uint(0) >> 1)
	tests := []struct {
		name string
		req  *apicompat.ChatCompletionsRequest
		want int
	}{
		{name: "nil request", want: 0},
		{name: "nil fields", req: &apicompat.ChatCompletionsRequest{}, want: 0},
		{name: "legacy positive", req: &apicompat.ChatCompletionsRequest{MaxTokens: &legacy}, want: 128},
		{name: "completion positive wins", req: &apicompat.ChatCompletionsRequest{MaxTokens: &legacy, MaxCompletionTokens: &completion}, want: 64},
		{name: "completion zero falls through", req: &apicompat.ChatCompletionsRequest{MaxTokens: &legacy, MaxCompletionTokens: &zero}, want: 128},
		{name: "completion negative falls through", req: &apicompat.ChatCompletionsRequest{MaxTokens: &legacy, MaxCompletionTokens: &negative}, want: 128},
		{name: "all non-positive", req: &apicompat.ChatCompletionsRequest{MaxTokens: &negative, MaxCompletionTokens: &zero}, want: 0},
		{name: "large positive", req: &apicompat.ChatCompletionsRequest{MaxCompletionTokens: &large}, want: large},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, cursorRequestOutputLimit(tt.req))
		})
	}
}

func TestCursorInputEstimateIsDeterministicOverflowSafeAndDoesNotMutateRequest(t *testing.T) {
	uri, _ := cursorTestPNGDataURI(t, 2000, 1000)
	content, err := json.Marshal([]map[string]any{
		{"type": "text", "text": "estimate this"},
		{"type": "image_url", "image_url": map[string]any{"url": uri}},
	})
	require.NoError(t, err)
	req := cursorWeatherToolRequest()
	req.Messages = append(req.Messages,
		apicompat.ChatMessage{Role: "assistant", ToolCalls: []apicompat.ChatToolCall{{
			ID: "estimate-call", Type: "function", Function: apicompat.ChatFunctionCall{Name: "get_weather", Arguments: `{"city":"HZ"}`},
		}}},
		apicompat.ChatMessage{Role: "tool", ToolCallID: "estimate-call", Content: json.RawMessage(`"20C"`)},
		apicompat.ChatMessage{Role: "user", Content: content},
	)
	before, err := json.Marshal(req)
	require.NoError(t, err)

	_, first, err := buildCursorAgentRunParams("gpt-5.2", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	_, second, err := buildCursorAgentRunParams("gpt-5.2", req, cursorNativeTranslateOptions())
	require.NoError(t, err)
	after, err := json.Marshal(req)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, before, after)
	require.Contains(t, first.text, "estimate-call")
	require.Contains(t, first.text, "20C")
	require.Contains(t, first.text, "get_weather")
	require.Equal(t, 2000*1000/cursorImageTokenDivisor, first.imageTokens)

	hugeImages := make([]cursorpkg.AgentImage, 2000)
	for i := range hugeImages {
		hugeImages[i].Width = int32(^uint32(0) >> 1)
		hugeImages[i].Height = int32(^uint32(0) >> 1)
	}
	require.Equal(t, int(^uint(0)>>1), cursorImageTokens(hugeImages))
}

func cursorNativeTranslateOptions() cursorTranslateOptions {
	return cursorTranslateOptions{nativeTools: true, nativeImages: true, cwd: cursorpkg.AgentDefaultCwd}
}

func cursorLegacyTranslateOptions() cursorTranslateOptions {
	return cursorTranslateOptions{cwd: cursorpkg.AgentDefaultCwd}
}

func cursorWeatherToolRequest() *apicompat.ChatCompletionsRequest {
	return &apicompat.ChatCompletionsRequest{
		Model:    "gpt-5.2",
		Messages: []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"call the tool"`)}},
		Tools: []apicompat.ChatTool{{
			Type: "function",
			Function: &apicompat.ChatFunction{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
			},
		}},
	}
}

func cursorToolRoundTripRequest() *apicompat.ChatCompletionsRequest {
	req := cursorWeatherToolRequest()
	req.Instructions = "System rules"
	req.Messages = []apicompat.ChatMessage{
		{Role: "developer", Content: json.RawMessage(`"Developer rules"`)},
		{Role: "user", Content: json.RawMessage(`"call the tool"`)},
		{Role: "assistant", Content: json.RawMessage(`"checking"`), ToolCalls: []apicompat.ChatToolCall{{
			ID: "call-1", Type: "function", Function: apicompat.ChatFunctionCall{Name: "get_weather", Arguments: `{"city":"SF"}`},
		}}},
		{Role: "tool", Name: "weather", ToolCallID: "call-1", Content: json.RawMessage(`{"ok":true,"temperature":18}`)},
		{Role: "user", Content: json.RawMessage(`"thanks"`)},
	}
	return req
}

func cursorTestPNGDataURI(t *testing.T, width, height int) (string, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	raw := buf.Bytes()
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw), raw
}

func TestCursorTranslateOptionsParseEnvBoolDefaultsTrue(t *testing.T) {
	for _, raw := range []string{"", "1", "true", "anything", "ON"} {
		require.True(t, parseCursorEnvBoolDefaultTrue(raw), "raw %q", raw)
	}
	for _, raw := range []string{"0", "false", "No", " OFF "} {
		require.False(t, parseCursorEnvBoolDefaultTrue(raw), "raw %q", raw)
	}
	for _, raw := range []string{"data:secret", strings.Repeat("x", 64)} {
		require.True(t, parseCursorEnvBoolDefaultTrue(raw), "raw %q", raw)
	}
}
