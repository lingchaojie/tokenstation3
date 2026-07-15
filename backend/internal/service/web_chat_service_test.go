package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestWebChatResponseCapture_CapturesBoundedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	capture := NewWebChatResponseCapture(c.Writer, 5)
	n, err := capture.Write([]byte("hello world"))

	require.NoError(t, err)
	require.Equal(t, len("hello world"), n)
	require.Equal(t, "hello world", rec.Body.String())
	require.Equal(t, []byte("hello"), capture.Body())
}

func TestWebChatResponseCapture_CapturesWriteStringBoundedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	capture := NewWebChatResponseCapture(c.Writer, 6)
	n, err := capture.WriteString("streamed text")

	require.NoError(t, err)
	require.Equal(t, len("streamed text"), n)
	require.Equal(t, "streamed text", rec.Body.String())
	require.Equal(t, []byte("stream"), capture.Body())
}

func TestWebChatResponseCapture_ExtractAssistantTextFromChatCompletions(t *testing.T) {
	streamed := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n" +
		"data: [DONE]\n\n")
	buffered := []byte(`{"choices":[{"message":{"content":[{"type":"text","text":"hi "},{"type":"text","text":"there"}]}}]}`)

	require.Equal(t, "hello", ExtractAssistantTextFromChatCompletions(streamed, true))
	require.Equal(t, "hi there", ExtractAssistantTextFromChatCompletions(buffered, false))
}

func TestWebChatResponseCapture_ExtractAssistantTextFromResponses(t *testing.T) {
	streamed := []byte(strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hel"}`,
		`data: {"type":"response.output_text.delta","delta":"lo"}`,
		`data: {"type":"response.completed","response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}}`,
		``,
	}, "\n\n"))
	buffered := []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi "},{"type":"output_text","text":"there"}]}]}`)

	require.Equal(t, "hello", ExtractAssistantTextFromChatCompletions(streamed, true))
	require.Equal(t, "hi there", ExtractAssistantTextFromChatCompletions(buffered, false))
}

func TestWebChatResponseCapture_ExtractAssistantTextFromLongStreamLine(t *testing.T) {
	longText := strings.Repeat("x", 70<<10)
	body := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"" + longText + "\"}}]}\n\n")

	require.Equal(t, longText, ExtractAssistantTextFromChatCompletions(body, true))
}

func TestWebChatResponseCapture_ExtractAssistantProcessFromChatCompletionsStream(t *testing.T) {
	body := []byte(strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"I should inspect this. "}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"README.md\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{"content":"Done"}}]}`,
		`data: [DONE]`,
		``,
	}, "\n\n"))

	require.Equal(t, []map[string]any{
		{
			"type": "reasoning",
			"text": "I should inspect this. ",
		},
		{
			"type":  "tool_call",
			"id":    "call_1",
			"index": 0,
			"name":  "read_file",
			"input": `{"path":"README.md"}`,
		},
	}, ExtractAssistantProcessFromChatCompletions(body, true))
}

func TestWebChatResponseCapture_ExtractAssistantProcessFromResponsesStreamReplacesFinalArguments(t *testing.T) {
	body := []byte(strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"search_docs"}}`,
		`data: {"type":"response.function_call_arguments.delta","call_id":"call_1","delta":"{\"query\":"}`,
		`data: {"type":"response.function_call_arguments.delta","call_id":"call_1","delta":"\"chat ui\"}"}`,
		`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"search_docs","arguments":"{\"query\":\"chat ui\"}"}}`,
		`data: {"type":"response.reasoning_text.delta","delta":"Use the docs."}`,
		``,
	}, "\n\n"))

	require.Equal(t, []map[string]any{
		{
			"type":  "tool_call",
			"id":    "call_1",
			"name":  "search_docs",
			"input": `{"query":"chat ui"}`,
		},
		{
			"type": "reasoning",
			"text": "Use the docs.",
		},
	}, ExtractAssistantProcessFromChatCompletions(body, true))
}

func TestWebChatSend_SavesOpenAIImageResultsAsArtifacts(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	user := &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}
	svc.availableGroups = []Group{{ID: 11, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true}}
	svc.openAISelection = &AccountSelectionResult{Account: &Account{ID: 77, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, Acquired: true}
	svc.openAIForwardResult = &OpenAIForwardResult{
		RequestID:     "openai_req",
		Model:         "gpt-image-2",
		UpstreamModel: openAIImagesResponsesMainModel,
		Stream:        true,
		ImageCount:    1,
		// Gateway producer coverage is exercised by
		// TestOpenAIGatewayService_ResponsesStreamingImageResultsSurviveNativeAndOAuthPassthrough.
		// This stub isolates the WebChat artifact consumer and persistence path.
		imageResults: []openAIResponsesImageResult{{
			Result:        "aGVsbG8=",
			RevisedPrompt: "draw a small icon",
			OutputFormat:  "webp",
			Size:          "1024x1024",
		}},
	}

	result, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           user,
		ConversationID: 7,
		Model:          "gpt-image-2",
		Provider:       "openai",
		Text:           "draw a small icon",
		Stream:         true,
		ImageGeneration: WebChatImageGenerationConfig{
			Enabled:      true,
			Size:         "1024x1024",
			OutputFormat: "webp",
		},
		GinContext: newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.Equal(t, result.AssistantMessageID, svc.finalizedAssistantMessageID)
	require.Len(t, svc.savedFiles, 1)
	require.Equal(t, "generated-image-1.webp", svc.savedFiles[0].Filename)
	require.Equal(t, "image/webp", svc.savedFiles[0].ContentType)
	require.Equal(t, []byte("hello"), svc.savedFiles[0].Body)
	require.Len(t, svc.createdArtifacts, 1)
	require.Equal(t, result.AssistantMessageID, svc.createdArtifacts[0].MessageID)
	require.Equal(t, int64(7), svc.createdArtifacts[0].ConversationID)
	require.Equal(t, int64(42), svc.createdArtifacts[0].UserID)
	require.Equal(t, "generated-image-1.webp", svc.createdArtifacts[0].Filename)
	require.Equal(t, "image/webp", svc.createdArtifacts[0].ContentType)
	require.Equal(t, WebChatArtifactSourceImageOutput, svc.createdArtifacts[0].Source)
	requireOrderedEvents(t, svc.events, "forward_openai_responses", "record_openai_usage", "usage_lookup")
	require.Equal(t, "/v1/responses", svc.openAIRecordUsageInput.UpstreamEndpoint)
	require.Equal(t, "image_generation", gjson.GetBytes(svc.forwardedBody, "tools.0.type").String())
	require.Equal(t, "image_generation", gjson.GetBytes(svc.forwardedBody, "tool_choice.type").String())
	require.False(t, gjson.GetBytes(svc.forwardedBody, "tools.#(type==\"web_search\")").Exists())
}

func TestWebChatSend_DropsOversizedOpenAIImageResultsBeforeArtifactSave(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	user := &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}
	svc.availableGroups = []Group{{ID: 11, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true}}
	svc.openAISelection = &AccountSelectionResult{Account: &Account{ID: 77, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, Acquired: true}
	oversizedBase64 := strings.Repeat("A", 4*((webChatMaxUploadBytes+1+2)/3))
	svc.openAIForwardResult = &OpenAIForwardResult{
		RequestID:     "openai_req_oversized",
		Model:         "gpt-image-2",
		UpstreamModel: openAIImagesResponsesMainModel,
		Stream:        true,
		ImageCount:    1,
		imageResults: []openAIResponsesImageResult{{
			Result:       "data:image/png;base64," + oversizedBase64,
			OutputFormat: "png",
			Size:         "1024x1024",
		}},
	}

	_, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           user,
		ConversationID: 7,
		Model:          "gpt-image-2",
		Provider:       "openai",
		Text:           "draw an oversized image",
		Stream:         true,
		ImageGeneration: WebChatImageGenerationConfig{
			Enabled:      true,
			Size:         "1024x1024",
			OutputFormat: "png",
		},
		GinContext: newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.Empty(t, svc.savedFiles)
	require.Empty(t, svc.createdArtifacts)
}

func TestWebChatSend_OpenAIAlwaysUsesResponsesWithAutomaticSearch(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	user := &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}
	svc.availableGroups = []Group{{ID: 11, Platform: PlatformOpenAI, Status: StatusActive}}
	svc.openAISelection = &AccountSelectionResult{Account: &Account{ID: 77, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, Acquired: true}
	svc.openAIForwardResult = &OpenAIForwardResult{
		RequestID:     "openai_req",
		Model:         "gpt-5.5",
		UpstreamModel: "gpt-5.5",
		Stream:        true,
	}

	result, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           user,
		ConversationID: 7,
		Model:          "gpt-5.5",
		Provider:       "openai",
		Text:           "search today's AI news",
		Stream:         true,
		GinContext:     newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.Equal(t, result.AssistantMessageID, svc.finalizedAssistantMessageID)
	requireOrderedEvents(t, svc.events, "forward_openai_responses", "record_openai_usage", "usage_lookup")
	require.Equal(t, "/v1/responses", svc.openAIRecordUsageInput.UpstreamEndpoint)
	require.Equal(t, "web_search", gjson.GetBytes(svc.forwardedBody, "tools.0.type").String())
	require.Equal(t, "auto", gjson.GetBytes(svc.forwardedBody, "tool_choice").String())
	require.Equal(t, "search today's AI news", gjson.GetBytes(svc.forwardedBody, "input.1.content.0.text").String())
	require.Equal(t, "Done.", *svc.finalUpdate.ContentText)
}

func TestWebChatSend_OpenAIIgnoresLegacyDisabledSearchConfig(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	user := &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}
	svc.availableGroups = []Group{{ID: 11, Platform: PlatformOpenAI, Status: StatusActive}}
	svc.openAIForwardResult = &OpenAIForwardResult{
		RequestID: "openai_req", Model: "gpt-5.5", UpstreamModel: "gpt-5.5", Stream: true,
	}

	_, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID: 42, User: user, ConversationID: 7,
		Model: "gpt-5.5", Provider: "openai", Text: "answer", Stream: true,
		WebSearch:  WebChatWebSearchConfig{Configured: true, Enabled: false},
		GinContext: newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.Equal(t, "web_search", gjson.GetBytes(svc.forwardedBody, "tools.0.type").String())
	require.Equal(t, "auto", gjson.GetBytes(svc.forwardedBody, "tool_choice").String())
}

func TestForwardWebChatOpenAIResponsesSkipsAPIKeyWithoutNativeResponses(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	unsupportedReleases := 0
	supportedReleases := 0
	svc.openAISelections = []*AccountSelectionResult{
		{
			Account: &Account{
				ID: 700, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
			},
			Acquired: false,
		},
		{
			Account: &Account{
				ID: 701, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
			},
			Acquired: true,
			ReleaseFunc: func() {
				unsupportedReleases++
			},
		},
		{
			Account: &Account{
				ID: 702, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			},
			Acquired: true,
			ReleaseFunc: func() {
				supportedReleases++
			},
		},
	}
	body := []byte(`{"model":"gpt-5.5","input":[{"role":"user","content":[{"type":"input_file","filename":"paper.pdf","file_data":"data:application/pdf;base64,JVBERg=="}]}],"tools":[{"type":"web_search"}],"tool_choice":"auto"}`)

	result, account, err := svc.forwardWebChatOpenAIResponses(
		context.Background(),
		newTestGinContext(context.Background()),
		&Group{ID: 11, Platform: PlatformOpenAI},
		body,
		webChatDispatchInput{Model: "gpt-5.5"},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(702), account.ID)
	require.Equal(t, int64(702), svc.openAIForwardAccountID)
	require.Equal(t, 1, unsupportedReleases)
	require.Equal(t, 1, supportedReleases)
	require.Len(t, svc.openAISelectionExclusions, 3)
	require.Empty(t, svc.openAISelectionExclusions[0])
	require.Contains(t, svc.openAISelectionExclusions[1], int64(700))
	require.Contains(t, svc.openAISelectionExclusions[2], int64(700))
	require.Contains(t, svc.openAISelectionExclusions[2], int64(701))
	require.Equal(t, "input_file", gjson.GetBytes(svc.forwardedBody, "input.0.content.0.type").String())
	require.Equal(t, "web_search", gjson.GetBytes(svc.forwardedBody, "tools.0.type").String())
	require.Equal(t, "auto", gjson.GetBytes(svc.forwardedBody, "tool_choice").String())
}

func TestWebChatDispatchOpenAIResponsesFailsWhenOnlyAccountLacksNativeResponses(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.availableGroups = []Group{{ID: 11, Platform: PlatformOpenAI, Status: StatusActive}}
	releases := 0
	svc.openAISelections = []*AccountSelectionResult{{
		Account: &Account{
			ID: 701, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false},
		},
		Acquired:    true,
		ReleaseFunc: func() { releases++ },
	}}

	result, err := svc.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
		User:               &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true},
		ConversationID:     7,
		AssistantMessageID: 101,
		Model:              "gpt-5.5",
		Provider:           "openai",
		Capabilities: WebChatModelCapability{
			Provider: "openai", Platform: PlatformOpenAI, Model: "gpt-5.5", SupportsText: true, SupportsWebSearch: true,
		},
		Messages: []WebChatMessage{{Role: WebChatRoleUser, ContentText: "search"}},
		Stream:   true,
	})

	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, result)
	require.Equal(t, 1, releases)
	require.Equal(t, 2, svc.openAISelectCalls)
	require.NotContains(t, svc.events, "forward_openai_responses")
	require.Nil(t, svc.openAIRecordUsageInput)
}

func TestForwardWebChatOpenAIResponsesAcceptsNativeResponsesAccounts(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
	}{
		{
			name:    "oauth",
			account: &Account{ID: 711, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		},
		{
			name: "supported API key",
			account: &Account{
				ID: 712, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			},
		},
		{
			name:    "unknown API key",
			account: &Account{ID: 713, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newWebChatServiceWithStubs(t)
			svc.openAISelections = []*AccountSelectionResult{{Account: tt.account, Acquired: true}}

			result, account, err := svc.forwardWebChatOpenAIResponses(
				context.Background(),
				newTestGinContext(context.Background()),
				&Group{ID: 11, Platform: PlatformOpenAI},
				[]byte(`{"model":"gpt-5.5","input":"hello"}`),
				webChatDispatchInput{Model: "gpt-5.5"},
			)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.account.ID, account.ID)
			require.Equal(t, tt.account.ID, svc.openAIForwardAccountID)
			require.Equal(t, 1, svc.openAISelectCalls)
		})
	}
}

func TestWebChatDispatch_OpenAIFileOnlyAndMixedInputsUseNativeResponses(t *testing.T) {
	pdf := []byte("%PDF-1.7\n%%EOF")
	png := []byte("\x89PNG\r\n\x1a\n")
	cases := []struct {
		name     string
		messages []WebChatMessage
		files    map[string][]byte
		keys     []string
		assert   func(*testing.T, []byte)
	}{
		{
			name: "file only",
			messages: []WebChatMessage{{Role: WebChatRoleUser, Attachments: []WebChatAttachment{{
				Kind: WebChatAttachmentKindFile, Filename: "paper.pdf", ContentType: "application/pdf", StorageKey: "paper.pdf",
			}}}},
			files: map[string][]byte{"paper.pdf": pdf},
			keys:  []string{"paper.pdf"},
			assert: func(t *testing.T, body []byte) {
				require.Equal(t, "input_file", gjson.GetBytes(body, "input.0.content.0.type").String())
				require.Equal(t, "auto", gjson.GetBytes(body, "input.0.content.0.detail").String())
			},
		},
		{
			name: "mixed",
			messages: []WebChatMessage{{Role: WebChatRoleUser, ContentText: "compare", Attachments: []WebChatAttachment{
				{Kind: WebChatAttachmentKindImage, Filename: "diagram.png", ContentType: "image/png", StorageKey: "diagram.png"},
				{Kind: WebChatAttachmentKindFile, Filename: "paper.pdf", ContentType: "application/pdf", StorageKey: "paper.pdf"},
			}}},
			files: map[string][]byte{"diagram.png": png, "paper.pdf": pdf},
			keys:  []string{"diagram.png", "paper.pdf"},
			assert: func(t *testing.T, body []byte) {
				require.Equal(t, "input_text", gjson.GetBytes(body, "input.0.content.0.type").String())
				require.Equal(t, "input_image", gjson.GetBytes(body, "input.0.content.1.type").String())
				require.Equal(t, "input_file", gjson.GetBytes(body, "input.0.content.2.type").String())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newWebChatServiceWithStubs(t)
			svc.availableGroups = []Group{{ID: 11, Platform: PlatformOpenAI, Status: StatusActive}}
			metaSizes := make(map[string]int64, len(tc.files))
			for key, data := range tc.files {
				metaSizes[key] = int64(len(data))
			}
			svc.storage = &fakeWebChatStorage{t: t, files: tc.files, metaSizes: metaSizes, expectedKeys: tc.keys}

			_, err := svc.dispatchChatCompletions(newTestGinContext(context.Background()), webChatDispatchInput{
				User:           &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true},
				ConversationID: 7, AssistantMessageID: 101, Model: "gpt-5.5", Provider: "openai",
				Capabilities: WebChatModelCapability{
					Provider: "openai", Platform: PlatformOpenAI, Model: "gpt-5.5",
					SupportsText: true, SupportsImageInput: true, SupportsFileContext: true, SupportsWebSearch: true,
				},
				Messages: tc.messages, Stream: true,
			})

			require.NoError(t, err)
			requireOrderedEvents(t, svc.events, "forward_openai_responses", "record_openai_usage", "usage_lookup")
			require.Equal(t, "/v1/responses", svc.openAIRecordUsageInput.UpstreamEndpoint)
			tc.assert(t, svc.forwardedBody)
		})
	}
}

func TestWebChatSend_AnthropicWebSearchAutoUsesResponsesAPI(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	user := &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}
	svc.gatewayForwardResult = &ForwardResult{
		RequestID:       "anthropic_req",
		Model:           "claude-sonnet-4",
		UpstreamModel:   "claude-sonnet-4",
		Stream:          true,
		Usage:           ClaudeUsage{InputTokens: 12, OutputTokens: 4},
		Duration:        100 * time.Millisecond,
		ReasoningEffort: nil,
	}

	result, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           user,
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "search today's AI news",
		Stream:         true,
		WebSearch:      WebChatWebSearchConfig{Configured: true},
		GinContext:     newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.Equal(t, result.AssistantMessageID, svc.finalizedAssistantMessageID)
	requireOrderedEvents(t, svc.events, "forward_responses", "record_usage", "usage_lookup")
	require.Equal(t, "/v1/responses", svc.recordUsageInput.UpstreamEndpoint)
	require.Equal(t, "web_search", gjson.GetBytes(svc.forwardedBody, "tools.0.type").String())
	require.Equal(t, "auto", gjson.GetBytes(svc.forwardedBody, "tool_choice").String())
	require.Equal(t, "search today's AI news", gjson.GetBytes(svc.forwardedBody, "input.1.content").String())
	require.Equal(t, "Done.", *svc.finalUpdate.ContentText)
}

func TestWebChatSend_UsesHiddenKeyAndSubscriptionFirstBilling(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	user := &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}
	svc.gatewayForwardResult = &ForwardResult{
		RequestID: "upstream_req",
		Model:     "claude-sonnet-4",
		Usage:     ClaudeUsage{InputTokens: 10, OutputTokens: 20},
		Duration:  100 * time.Millisecond,
	}

	result, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           user,
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "hello",
		Stream:         true,
		GinContext:     newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.Equal(t, int64(42), svc.ensureWebChatKeyUserID)
	require.Same(t, user, svc.ensureWebChatKeyUser)
	require.Same(t, user, svc.billingUser)
	require.Same(t, svc.hiddenKey, svc.recordUsageInput.APIKey)
	require.Equal(t, "/api/v1/chat/conversations/7/messages", svc.recordUsageInput.InboundEndpoint)
	require.NotNil(t, svc.recordUsageInput.Subscription)
	require.Equal(t, result.AssistantMessageID, svc.finalizedAssistantMessageID)
	require.Equal(t, "client:webchat-message-102", svc.usageLookupRequestID)
	require.Equal(t, svc.hiddenKey.ID, svc.usageLookupAPIKeyID)
	require.Equal(t, "webchat-message-102", svc.recordUsageClientRequestID)
	require.NotNil(t, svc.finalUpdate.UsageLogID)
	require.Equal(t, int64(88), *svc.finalUpdate.UsageLogID)
	requireOrderedEvents(t, svc.events, "resolve_subscription", "validate_subscription", "billing_eligibility", "forward", "record_usage")
}

func TestWebChatSend_BlocksUnsupportedContextBeforeBilling(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	user := &User{ID: 42, AllowedGroups: []int64{11}}

	_, err := svc.SendMessage(nil, WebChatSendInput{
		UserID:         42,
		User:           user,
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		AttachmentIDs:  []int64{99},
		GinContext:     newTestGinContext(context.Background()),
	})

	require.ErrorIs(t, err, ErrWebChatUnsupportedContext)
	require.False(t, svc.recordUsageCalled)
	require.NotContains(t, svc.events, "resolve_subscription")
	require.NotContains(t, svc.events, "billing_eligibility")
	require.NotContains(t, svc.events, "forward")
}

func TestWebChatSend_RequiresUserSnapshotOrResolver(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.userResolver = nil

	_, err := svc.SendMessage(nil, WebChatSendInput{
		UserID:         42,
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "hello",
		GinContext:     newTestGinContext(context.Background()),
	})

	require.ErrorIs(t, err, ErrUserNotFound)
	require.Empty(t, svc.events)
}

func TestWebChatSend_UsesInjectedUserResolver(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	resolvedUser := &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}
	svc.userResolver = webChatUserResolverStub{user: resolvedUser}

	_, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "hello",
		GinContext:     newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.Same(t, resolvedUser, svc.ensureWebChatKeyUser)
	require.Same(t, resolvedUser, svc.billingUser)
}

func TestWebChatSend_RejectsModelNotInDynamicCatalog(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)

	_, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           &User{ID: 42, AllowedGroups: []int64{11}},
		ConversationID: 7,
		Model:          "claude-opus-4-8",
		Provider:       "anthropic",
		Text:           "hello",
		GinContext:     newTestGinContext(context.Background()),
	})

	require.ErrorIs(t, err, ErrWebChatInvalidModel)
	require.Empty(t, svc.events)
}

func TestWebChatSend_RequiresGinContextBeforePersistence(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)

	_, err := svc.SendMessage(nil, WebChatSendInput{
		UserID:         42,
		User:           &User{ID: 42, AllowedGroups: []int64{11}},
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "hello",
	})

	require.ErrorIs(t, err, ErrWebChatContextRequired)
	require.Empty(t, svc.createdMessages)
	require.Empty(t, svc.events)
}

func TestWebChatSend_DoesNotForwardAssistantPlaceholder(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.repo.statefulMessages = true
	svc.repo.messages = []WebChatMessage{{
		ID:             1,
		ConversationID: 7,
		UserID:         42,
		Role:           WebChatRoleUser,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		ContentText:    "history",
		Status:         WebChatMessageStatusCompleted,
	}}

	_, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           &User{ID: 42, AllowedGroups: []int64{11}},
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "current",
		Stream:         true,
		GinContext:     newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	messages := forwardedChatCompletionMessages(t, svc.forwardedBody)
	require.Len(t, messages, 2)
	require.Equal(t, WebChatRoleUser, messages[0].Role)
	require.Equal(t, "history", messages[0].Content)
	require.Equal(t, WebChatRoleUser, messages[1].Role)
	require.Equal(t, "current", messages[1].Content)
}

func TestWebChatSend_UsesCreateMessageAttachmentsWithoutReattaching(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.repo.attachOnCreate = true
	svc.repo.attachUploadedErr = errors.New("reattach called")
	svc.availableGroups = []Group{{ID: 12, Platform: PlatformOpenAI, Status: StatusActive}}

	_, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           &User{ID: 42, AllowedGroups: []int64{11}},
		ConversationID: 7,
		Model:          "gpt-image-2",
		Provider:       "openai",
		Text:           "describe this",
		AttachmentIDs:  []int64{99},
		GinContext:     newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.False(t, svc.repo.attachUploadedCalled)
}

func TestWebChatSend_DoesNotForwardWhenSelectionNotAcquired(t *testing.T) {
	tests := []struct {
		name     string
		waitPlan *AccountWaitPlan
	}{
		{name: "with wait plan", waitPlan: &AccountWaitPlan{AccountID: 77, MaxConcurrency: 1}},
		{name: "without wait plan"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newWebChatServiceWithStubs(t)
			svc.selection = &AccountSelectionResult{
				Account:  &Account{ID: 77, Platform: PlatformAnthropic},
				WaitPlan: tt.waitPlan,
			}

			_, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
				UserID:         42,
				User:           &User{ID: 42, AllowedGroups: []int64{11}},
				ConversationID: 7,
				Model:          "claude-sonnet-4",
				Provider:       "anthropic",
				Text:           "hello",
				GinContext:     newTestGinContext(context.Background()),
			})

			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.NotContains(t, svc.events, "forward")
			require.NotContains(t, svc.events, "record_usage")
			require.False(t, svc.recordUsageCalled)
		})
	}
}

func TestWebChatSend_ReleasesAcquiredSelectionWhenAccountMissing(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.selection = &AccountSelectionResult{
		Acquired: true,
		ReleaseFunc: func() {
			svc.releaseCount++
		},
	}

	_, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           &User{ID: 42, AllowedGroups: []int64{11}},
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "hello",
		GinContext:     newTestGinContext(context.Background()),
	})

	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, 1, svc.releaseCount)
	require.NotContains(t, svc.events, "forward")
	require.NotContains(t, svc.events, "record_usage")
}

func TestWebChatSend_ValidatesHistoricalContextBeforeBilling(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.repo.statefulMessages = true
	svc.repo.messages = []WebChatMessage{{
		ID:             1,
		ConversationID: 7,
		UserID:         42,
		Role:           WebChatRoleUser,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		ContentText:    "previous image",
		Status:         WebChatMessageStatusCompleted,
		Attachments: []WebChatAttachment{{
			ID:          99,
			UserID:      42,
			Kind:        WebChatAttachmentKindImage,
			Filename:    "image.png",
			ContentType: "image/png",
			StorageKey:  "image.png",
			Status:      WebChatAttachmentStatusUploaded,
		}},
	}}

	_, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           &User{ID: 42, AllowedGroups: []int64{11}},
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "continue",
		GinContext:     newTestGinContext(context.Background()),
	})

	require.ErrorIs(t, err, ErrWebChatUnsupportedContext)
	require.NotContains(t, svc.events, "ensure_key")
	require.NotContains(t, svc.events, "resolve_subscription")
	require.NotContains(t, svc.events, "billing_eligibility")
	require.NotContains(t, svc.events, "forward")
	require.Empty(t, svc.createdMessages)
}

func TestWebChatCancelMessageRejectsNonCancelableMessages(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.repo.statefulMessages = true
	svc.repo.messages = []WebChatMessage{
		{ID: 11, ConversationID: 7, UserID: 42, Role: WebChatRoleUser, Status: WebChatMessageStatusCompleted},
		{ID: 12, ConversationID: 7, UserID: 42, Role: WebChatRoleAssistant, Status: WebChatMessageStatusCompleted},
	}

	err := svc.CancelMessage(context.Background(), 42, 7, 11)
	require.ErrorIs(t, err, ErrWebChatMessageNotCancelable)

	err = svc.CancelMessage(context.Background(), 42, 7, 12)
	require.ErrorIs(t, err, ErrWebChatMessageNotCancelable)
	require.Empty(t, svc.updatedMessages)
}

func TestWebChatCancelMessageCancelsActiveAssistantDispatch(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.repo.statefulMessages = true
	svc.repo.messages = []WebChatMessage{{
		ID:             12,
		ConversationID: 7,
		UserID:         42,
		Role:           WebChatRoleAssistant,
		Status:         WebChatMessageStatusStreaming,
	}}
	var canceled bool
	svc.registerWebChatAssistantCancel(42, 7, 12, func() { canceled = true })

	err := svc.CancelMessage(context.Background(), 42, 7, 12)

	require.NoError(t, err)
	require.True(t, canceled)
	require.Len(t, svc.updatedMessages, 1)
	require.NotNil(t, svc.updatedMessages[0].Status)
	require.Equal(t, WebChatMessageStatusCanceled, *svc.updatedMessages[0].Status)
	require.Equal(t, []string{WebChatMessageStatusPending, WebChatMessageStatusStreaming}, svc.updatedMessages[0].ExpectedStatuses)
	require.NotNil(t, svc.updatedMessages[0].ExpectedRole)
	require.Equal(t, WebChatRoleAssistant, *svc.updatedMessages[0].ExpectedRole)
}

func TestWebChatSend_RecordUsageFailureAfterForwardStillCompletesMessage(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.recordUsageErr = errors.New("usage write failed")

	result, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           &User{ID: 42, AllowedGroups: []int64{11}},
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "hello",
		GinContext:     newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.NotZero(t, result.AssistantMessageID)
	require.True(t, svc.recordUsageCalled)
	require.Equal(t, result.AssistantMessageID, svc.finalizedAssistantMessageID)
	require.NotNil(t, svc.finalUpdate.Status)
	require.Equal(t, WebChatMessageStatusCompleted, *svc.finalUpdate.Status)
	require.Empty(t, svc.usageLookupRequestID)
	require.NotContains(t, webChatUpdatedStatuses(svc.updatedMessages), WebChatMessageStatusFailed)
}

func TestWebChatSend_UsageLookupFailureAfterForwardStillCompletesMessage(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.usageLookupErr = errors.New("lookup failed")

	result, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           &User{ID: 42, AllowedGroups: []int64{11}},
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "hello",
		GinContext:     newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.NotZero(t, result.AssistantMessageID)
	require.Equal(t, result.AssistantMessageID, svc.finalizedAssistantMessageID)
	require.NotNil(t, svc.finalUpdate.Status)
	require.Equal(t, WebChatMessageStatusCompleted, *svc.finalUpdate.Status)
	require.Nil(t, svc.finalUpdate.UsageLogID)
	require.NotContains(t, webChatUpdatedStatuses(svc.updatedMessages), WebChatMessageStatusFailed)
}

func TestWebChatSend_FinalizesAssistantAfterRequestContextCanceled(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.repo.statefulMessages = true
	ctx, cancel := context.WithCancel(context.Background())
	svc.cancelRequestOnForward = cancel

	result, err := svc.SendMessage(newTestGinContext(ctx), WebChatSendInput{
		UserID:         42,
		User:           &User{ID: 42, AllowedGroups: []int64{11}},
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "hello",
		GinContext:     newTestGinContext(ctx),
	})

	require.NoError(t, err)
	require.NotZero(t, result.AssistantMessageID)
	require.Equal(t, result.AssistantMessageID, svc.finalizedAssistantMessageID)
	require.NotNil(t, svc.finalUpdate.Status)
	require.Equal(t, WebChatMessageStatusCompleted, *svc.finalUpdate.Status)
	require.NotNil(t, svc.finalUpdate.UsageLogID)
	require.Equal(t, int64(88), *svc.finalUpdate.UsageLogID)
}

func TestWebChatSend_DoesNotOverwriteCanceledAssistantWhenDispatchCancels(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	svc.repo.statefulMessages = true
	svc.forwardErr = context.Canceled
	svc.cancelAssistantOnForward = true

	result, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID:         42,
		User:           &User{ID: 42, AllowedGroups: []int64{11}},
		ConversationID: 7,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		Text:           "hello",
		GinContext:     newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.NotZero(t, result.AssistantMessageID)
	require.NotContains(t, webChatUpdatedStatuses(svc.updatedMessages), WebChatMessageStatusFailed)
	require.Contains(t, webChatUpdatedStatuses(svc.updatedMessages), WebChatMessageStatusCanceled)
}

func TestWebChatUpdateConversationRejectsInvalidStatus(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	status := WebChatConversationStatusDeleted

	_, err := svc.UpdateConversation(context.Background(), 42, 7, UpdateWebChatConversationInput{Status: &status})

	require.ErrorIs(t, err, ErrWebChatInvalidConversationStatus)
}

func TestWebChatUpdateConversationAllowsArchivedStatus(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	status := WebChatConversationStatusArchived

	conversation, err := svc.UpdateConversation(context.Background(), 42, 7, UpdateWebChatConversationInput{Status: &status})

	require.NoError(t, err)
	require.Equal(t, WebChatConversationStatusArchived, conversation.Status)
	require.NotNil(t, svc.repo.lastConversationUpdate.Status)
	require.Equal(t, WebChatConversationStatusArchived, *svc.repo.lastConversationUpdate.Status)
}

type webChatServiceTestDouble struct {
	*WebChatService

	ensureWebChatKeyUserID      int64
	ensureWebChatKeyUser        *User
	billingUser                 *User
	hiddenKey                   *APIKey
	recordUsageInput            *RecordUsageInput
	recordUsageCalled           bool
	recordUsageErr              error
	gatewayForwardResult        *ForwardResult
	openAIForwardResult         *OpenAIForwardResult
	forwardErr                  error
	cancelAssistantOnForward    bool
	finalizedAssistantMessageID int64
	finalUpdate                 UpdateWebChatMessageInput
	nextMessageID               int64
	createdMessages             []CreateWebChatMessageInput
	createdArtifacts            []CreateWebChatArtifactInput
	updatedMessages             []UpdateWebChatMessageInput
	savedFiles                  []webChatSavedTestFile
	usageLookupRequestID        string
	usageLookupAPIKeyID         int64
	usageLookupErr              error
	recordUsageClientRequestID  string
	openAIRecordUsageInput      *OpenAIRecordUsageInput
	forwardedBody               []byte
	selection                   *AccountSelectionResult
	openAISelection             *AccountSelectionResult
	openAISelections            []*AccountSelectionResult
	openAISelectionExclusions   []map[int64]struct{}
	openAISelectCalls           int
	openAIForwardAccountID      int64
	availableGroups             []Group
	releaseCount                int
	repo                        *webChatRepoStub
	events                      []string
	cancelRequestOnForward      context.CancelFunc
}

type webChatSavedTestFile struct {
	Filename    string
	ContentType string
	Body        []byte
}

func newWebChatServiceWithStubs(t *testing.T) *webChatServiceTestDouble {
	t.Helper()
	gin.SetMode(gin.TestMode)

	double := &webChatServiceTestDouble{nextMessageID: 100}
	repo := &webChatRepoStub{double: double}
	storage := webChatStorageStub{double: double}
	double.repo = repo
	double.selection = &AccountSelectionResult{Account: &Account{ID: 77, Platform: PlatformAnthropic}, Acquired: true}
	double.openAISelection = &AccountSelectionResult{Account: &Account{ID: 77, Platform: PlatformOpenAI}, Acquired: true}
	double.WebChatService = NewWebChatService(repo, storage, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	double.defaultGroups = stubGroupResolver{ids: map[string]int64{APIKeyTypeAnthropic: 1, APIKeyTypeOpenAI: 2}}
	double.accountLister = stubAccountLister{byGroup: map[int64][]Account{
		1: {acctWithMapping(PlatformAnthropic, "claude-sonnet-4")},
		2: {acctWithMapping(PlatformOpenAI, "gpt-5.5", "gpt-image-2")},
	}}
	double.apiKeyService = &webChatAPIKeyServiceStub{double: double}
	double.subscriptionService = &webChatSubscriptionServiceStub{double: double}
	double.billingCacheService = &webChatBillingCacheServiceStub{double: double}
	double.gatewayService = &webChatGatewayServiceStub{double: double}
	double.openAIGatewayService = &webChatOpenAIGatewayServiceStub{double: double}
	double.usageLogRepository = &webChatUsageLogRepoStub{double: double}
	return double
}

type webChatRepoStub struct {
	double                 *webChatServiceTestDouble
	statefulMessages       bool
	messages               []WebChatMessage
	attachOnCreate         bool
	attachUploadedCalled   bool
	attachUploadedErr      error
	lastConversationUpdate UpdateWebChatConversationInput
}

func (r *webChatRepoStub) CreateConversation(context.Context, CreateWebChatConversationInput) (*WebChatConversation, error) {
	panic("unexpected CreateConversation")
}

func (r *webChatRepoStub) ListConversations(context.Context, int64, pagination.PaginationParams) ([]WebChatConversation, *pagination.PaginationResult, error) {
	panic("unexpected ListConversations")
}

func (r *webChatRepoStub) GetConversationForUser(_ context.Context, userID, conversationID int64) (*WebChatConversation, error) {
	return &WebChatConversation{ID: conversationID, UserID: userID, Status: WebChatConversationStatusActive}, nil
}

func (r *webChatRepoStub) UpdateConversation(_ context.Context, userID, conversationID int64, in UpdateWebChatConversationInput) (*WebChatConversation, error) {
	r.lastConversationUpdate = in
	status := WebChatConversationStatusActive
	if in.Status != nil {
		status = *in.Status
	}
	return &WebChatConversation{ID: conversationID, UserID: userID, Status: status}, nil
}

func (r *webChatRepoStub) SoftDeleteConversation(context.Context, int64, int64) error {
	panic("unexpected SoftDeleteConversation")
}

func (r *webChatRepoStub) CreateMessage(_ context.Context, in CreateWebChatMessageInput) (*WebChatMessage, error) {
	r.double.nextMessageID++
	id := r.double.nextMessageID
	r.double.createdMessages = append(r.double.createdMessages, in)
	message := WebChatMessage{
		ID:             id,
		ConversationID: in.ConversationID,
		UserID:         in.UserID,
		Role:           in.Role,
		Model:          in.Model,
		Provider:       in.Provider,
		ContentText:    in.ContentText,
		Status:         in.Status,
	}
	if r.attachOnCreate && len(in.AttachmentIDs) > 0 {
		message.Attachments = webChatTestAttachments(in.ConversationID, id, in.AttachmentIDs)
	}
	if r.statefulMessages {
		r.messages = append(r.messages, message)
	}
	return &message, nil
}

func (r *webChatRepoStub) ListMessages(_ context.Context, userID, conversationID int64) ([]WebChatMessage, error) {
	if r.statefulMessages {
		return append([]WebChatMessage(nil), r.messages...), nil
	}
	return []WebChatMessage{{
		ID:             1,
		ConversationID: conversationID,
		UserID:         userID,
		Role:           WebChatRoleUser,
		Model:          "claude-sonnet-4",
		Provider:       "anthropic",
		ContentText:    "hello",
		Status:         WebChatMessageStatusCompleted,
	}}, nil
}

func (r *webChatRepoStub) UpdateMessage(ctx context.Context, _ int64, messageID int64, in UpdateWebChatMessageInput) (*WebChatMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.double.updatedMessages = append(r.double.updatedMessages, in)
	for i := range r.messages {
		if r.messages[i].ID != messageID {
			continue
		}
		if in.ExpectedConversationID != nil && r.messages[i].ConversationID != *in.ExpectedConversationID {
			return nil, ErrWebChatMessageNotFound
		}
		if in.ExpectedRole != nil && r.messages[i].Role != *in.ExpectedRole {
			return nil, ErrWebChatMessageNotFound
		}
		if len(in.ExpectedStatuses) > 0 {
			found := false
			for _, status := range in.ExpectedStatuses {
				if r.messages[i].Status == status {
					found = true
					break
				}
			}
			if !found {
				return nil, ErrWebChatMessageNotFound
			}
		}
		if in.Status != nil {
			r.messages[i].Status = *in.Status
		}
		if in.ContentText != nil {
			r.messages[i].ContentText = *in.ContentText
		}
	}
	if in.Status != nil && *in.Status == WebChatMessageStatusCompleted {
		r.double.finalizedAssistantMessageID = messageID
		r.double.finalUpdate = in
	}
	return &WebChatMessage{ID: messageID}, nil
}

func (r *webChatRepoStub) CreateAttachment(context.Context, CreateWebChatAttachmentInput) (*WebChatAttachment, error) {
	panic("unexpected CreateAttachment")
}

func (r *webChatRepoStub) AttachUploadedFilesToMessage(_ context.Context, _ int64, conversationID, messageID int64, attachmentIDs []int64) ([]WebChatAttachment, error) {
	r.attachUploadedCalled = true
	if r.attachUploadedErr != nil {
		return nil, r.attachUploadedErr
	}
	return webChatTestAttachments(conversationID, messageID, attachmentIDs), nil
}

func (r *webChatRepoStub) GetAttachmentForUser(context.Context, int64, int64) (*WebChatAttachment, error) {
	return &WebChatAttachment{
		ID:          99,
		UserID:      42,
		Kind:        WebChatAttachmentKindImage,
		Filename:    "image.png",
		ContentType: "image/png",
		StorageKey:  "image.png",
		Status:      WebChatAttachmentStatusUploaded,
	}, nil
}

func (r *webChatRepoStub) CreateArtifact(_ context.Context, in CreateWebChatArtifactInput) (*WebChatArtifact, error) {
	r.double.createdArtifacts = append(r.double.createdArtifacts, in)
	return &WebChatArtifact{
		ID:             int64(len(r.double.createdArtifacts)),
		MessageID:      in.MessageID,
		ConversationID: in.ConversationID,
		UserID:         in.UserID,
		Filename:       in.Filename,
		ContentType:    in.ContentType,
		SizeBytes:      in.SizeBytes,
		StorageKey:     in.StorageKey,
		SHA256:         in.SHA256,
		Source:         in.Source,
	}, nil
}

func (r *webChatRepoStub) GetArtifactForUser(context.Context, int64, int64) (*WebChatArtifact, error) {
	panic("unexpected GetArtifactForUser")
}

type webChatAPIKeyServiceStub struct {
	double *webChatServiceTestDouble
}

func (s *webChatAPIKeyServiceStub) GetAvailableGroups(context.Context, int64) ([]Group, error) {
	if len(s.double.availableGroups) > 0 {
		return append([]Group(nil), s.double.availableGroups...), nil
	}
	return []Group{{ID: 11, Platform: PlatformAnthropic, Status: StatusActive}}, nil
}

func (s *webChatAPIKeyServiceStub) EnsureWebChatKey(_ context.Context, user *User, group *Group) (*APIKey, error) {
	s.double.events = append(s.double.events, "ensure_key")
	s.double.ensureWebChatKeyUserID = user.ID
	s.double.ensureWebChatKeyUser = user
	groupID := group.ID
	s.double.hiddenKey = &APIKey{ID: 55, UserID: user.ID, Key: "wc_hidden", GroupID: &groupID, Group: group, User: user, Status: StatusActive}
	return s.double.hiddenKey, nil
}

func (s *webChatAPIKeyServiceStub) UpdateQuotaUsed(context.Context, int64, float64) error {
	return nil
}

func (s *webChatAPIKeyServiceStub) UpdateRateLimitUsage(context.Context, int64, float64) error {
	return nil
}

type webChatSubscriptionServiceStub struct {
	double *webChatServiceTestDouble
}

func (s *webChatSubscriptionServiceStub) ResolveActiveSubscriptionForRoutedGroup(context.Context, int64, int64) (*UserSubscription, error) {
	s.double.events = append(s.double.events, "resolve_subscription")
	return &UserSubscription{ID: 66, UserID: 42, GroupID: 11, Status: SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (s *webChatSubscriptionServiceStub) ValidateAndCheckLimits(*UserSubscription, *Group) (bool, error) {
	s.double.events = append(s.double.events, "validate_subscription")
	return false, nil
}

type webChatBillingCacheServiceStub struct {
	double *webChatServiceTestDouble
}

func (s *webChatBillingCacheServiceStub) CheckBillingEligibility(_ context.Context, user *User, _ *APIKey, _ *Group, _ *UserSubscription, _ string) error {
	s.double.events = append(s.double.events, "billing_eligibility")
	s.double.billingUser = user
	return nil
}

type webChatGatewayServiceStub struct {
	double *webChatServiceTestDouble
}

func (s *webChatGatewayServiceStub) SelectAccountWithLoadAwareness(context.Context, *int64, string, string, map[int64]struct{}, string, int64) (*AccountSelectionResult, error) {
	return s.double.selection, nil
}

func (s *webChatGatewayServiceStub) ForwardAsChatCompletions(_ context.Context, _ *gin.Context, _ *Account, body []byte, _ *ParsedRequest) (*ForwardResult, error) {
	s.double.events = append(s.double.events, "forward")
	s.double.forwardedBody = append([]byte(nil), body...)
	if s.double.cancelAssistantOnForward {
		_ = s.double.CancelMessage(context.Background(), 42, 7, s.double.nextMessageID)
	}
	if s.double.forwardErr != nil {
		return nil, s.double.forwardErr
	}
	if s.double.gatewayForwardResult == nil {
		s.double.gatewayForwardResult = &ForwardResult{RequestID: "upstream_req", Model: "claude-sonnet-4"}
	}
	if s.double.cancelRequestOnForward != nil {
		s.double.cancelRequestOnForward()
	}
	return s.double.gatewayForwardResult, nil
}

func (s *webChatGatewayServiceStub) ForwardAsResponses(_ context.Context, c *gin.Context, _ *Account, body []byte, _ *ParsedRequest) (*ForwardResult, error) {
	s.double.events = append(s.double.events, "forward_responses")
	s.double.forwardedBody = append([]byte(nil), body...)
	if _, err := c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Done.\"}\n\n"); err != nil {
		return nil, err
	}
	if _, err := c.Writer.WriteString("data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Done.\"}]}]}}\n\n"); err != nil {
		return nil, err
	}
	if s.double.gatewayForwardResult == nil {
		s.double.gatewayForwardResult = &ForwardResult{RequestID: "anthropic_req", Model: "claude-sonnet-4", Stream: true}
	}
	return s.double.gatewayForwardResult, nil
}

func (s *webChatGatewayServiceStub) RecordUsage(ctx context.Context, in *RecordUsageInput) error {
	s.double.events = append(s.double.events, "record_usage")
	s.double.recordUsageClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
	s.double.recordUsageCalled = true
	s.double.recordUsageInput = in
	return s.double.recordUsageErr
}

type webChatOpenAIGatewayServiceStub struct {
	double *webChatServiceTestDouble
}

func (s *webChatOpenAIGatewayServiceStub) SelectAccountWithLoadAwareness(_ context.Context, _ *int64, _ string, _ string, excluded map[int64]struct{}) (*AccountSelectionResult, error) {
	snapshot := make(map[int64]struct{}, len(excluded))
	for id := range excluded {
		snapshot[id] = struct{}{}
	}
	s.double.openAISelectionExclusions = append(s.double.openAISelectionExclusions, snapshot)
	selectionIndex := s.double.openAISelectCalls
	s.double.openAISelectCalls++
	if s.double.openAISelections != nil {
		if selectionIndex >= len(s.double.openAISelections) {
			return nil, ErrNoAvailableAccounts
		}
		return s.double.openAISelections[selectionIndex], nil
	}
	return s.double.openAISelection, nil
}

func (s *webChatOpenAIGatewayServiceStub) ForwardAsChatCompletions(_ context.Context, c *gin.Context, _ *Account, body []byte, _ string, _ string) (*OpenAIForwardResult, error) {
	s.double.events = append(s.double.events, "forward_openai")
	s.double.forwardedBody = append([]byte(nil), body...)
	if _, err := c.Writer.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"}}]}\n\n"); err != nil {
		return nil, err
	}
	if _, err := c.Writer.WriteString("data: [DONE]\n\n"); err != nil {
		return nil, err
	}
	if s.double.openAIForwardResult == nil {
		s.double.openAIForwardResult = &OpenAIForwardResult{RequestID: "openai_req", Model: "gpt-image-2"}
	}
	return s.double.openAIForwardResult, nil
}

func (s *webChatOpenAIGatewayServiceStub) Forward(_ context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	s.double.events = append(s.double.events, "forward_openai_responses")
	if account != nil {
		s.double.openAIForwardAccountID = account.ID
	}
	s.double.forwardedBody = append([]byte(nil), body...)
	if _, err := c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Done.\"}\n\n"); err != nil {
		return nil, err
	}
	if _, err := c.Writer.WriteString("data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Done.\"}]}]}}\n\n"); err != nil {
		return nil, err
	}
	if s.double.openAIForwardResult == nil {
		s.double.openAIForwardResult = &OpenAIForwardResult{RequestID: "openai_req", Model: "gpt-5.5", Stream: true}
	}
	return s.double.openAIForwardResult, nil
}

func (s *webChatOpenAIGatewayServiceStub) RecordUsage(ctx context.Context, in *OpenAIRecordUsageInput) error {
	s.double.events = append(s.double.events, "record_openai_usage")
	s.double.recordUsageClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
	s.double.openAIRecordUsageInput = in
	return s.double.recordUsageErr
}

type webChatUsageLogRepoStub struct {
	double *webChatServiceTestDouble
}

func (r *webChatUsageLogRepoStub) GetByRequestIDAndAPIKeyID(ctx context.Context, requestID string, apiKeyID int64) (*UsageLog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.double.events = append(r.double.events, "usage_lookup")
	r.double.usageLookupRequestID = requestID
	r.double.usageLookupAPIKeyID = apiKeyID
	if r.double.usageLookupErr != nil {
		return nil, r.double.usageLookupErr
	}
	return &UsageLog{ID: 88, RequestID: requestID, APIKeyID: apiKeyID}, nil
}

type webChatUserResolverStub struct {
	user *User
}

func (s webChatUserResolverStub) GetByID(context.Context, int64) (*User, error) {
	if s.user == nil {
		return nil, ErrUserNotFound
	}
	return s.user, nil
}

func requireOrderedEvents(t *testing.T, events []string, expected ...string) {
	t.Helper()
	last := -1
	for _, want := range expected {
		index := -1
		for i, event := range events {
			if event == want {
				index = i
				break
			}
		}
		require.NotEqual(t, -1, index, "missing event %q in %v", want, events)
		require.Greater(t, index, last, "event %q out of order in %v", want, events)
		last = index
	}
}

func webChatUpdatedStatuses(updates []UpdateWebChatMessageInput) []string {
	statuses := make([]string, 0, len(updates))
	for _, update := range updates {
		if update.Status != nil {
			statuses = append(statuses, *update.Status)
		}
	}
	return statuses
}

func webChatTestAttachments(conversationID, messageID int64, attachmentIDs []int64) []WebChatAttachment {
	attachments := make([]WebChatAttachment, 0, len(attachmentIDs))
	for _, id := range attachmentIDs {
		attachments = append(attachments, WebChatAttachment{
			ID:             id,
			MessageID:      &messageID,
			ConversationID: &conversationID,
			UserID:         42,
			Kind:           WebChatAttachmentKindImage,
			Filename:       "image.png",
			ContentType:    "image/png",
			StorageKey:     "image.png",
			Status:         WebChatAttachmentStatusUploaded,
		})
	}
	return attachments
}

func forwardedChatCompletionMessages(t *testing.T, body []byte) []struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
} {
	t.Helper()
	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &payload))
	return payload.Messages
}

type webChatStorageStub struct {
	double *webChatServiceTestDouble
}

func (s webChatStorageStub) Save(_ context.Context, in WebChatStorageSaveInput) (*WebChatStoredFile, error) {
	body, err := io.ReadAll(in.Reader)
	if err != nil {
		return nil, err
	}
	s.double.savedFiles = append(s.double.savedFiles, webChatSavedTestFile{
		Filename:    in.Filename,
		ContentType: in.ContentType,
		Body:        append([]byte(nil), body...),
	})
	return &WebChatStoredFile{
		StorageKey:  "generated/" + in.Filename,
		Filename:    in.Filename,
		ContentType: in.ContentType,
		SizeBytes:   int64(len(body)),
		SHA256:      "sha256",
	}, nil
}

func (webChatStorageStub) Open(context.Context, string) (io.ReadCloser, WebChatStoredFileMeta, error) {
	png := string([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	return io.NopCloser(strings.NewReader(png)), WebChatStoredFileMeta{SizeBytes: int64(len(png))}, nil
}

func (webChatStorageStub) Delete(context.Context, string) error {
	return nil
}

func newTestGinContext(ctx context.Context) *gin.Context {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/chat/conversations/7/messages", nil)
	c.Request = req
	return c
}
