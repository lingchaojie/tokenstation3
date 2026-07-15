package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildWebChatCompletionsPayload_IncludesTextImageAndFilePreview(t *testing.T) {
	messages := []WebChatMessage{{
		Role:        WebChatRoleUser,
		ContentText: "Explain this image and notes",
		Attachments: []WebChatAttachment{
			{Kind: WebChatAttachmentKindImage, ContentType: "image/png", StorageKey: "u/1/image.png"},
			{Kind: WebChatAttachmentKindFile, ContentType: "text/plain", TextPreview: webChatStringPtr("notes")},
		},
	}}

	storage := fakeStorageWithImage(t, "u/1/image.png", "iVBORw0KGgo=")
	payload, err := BuildWebChatCompletionsPayload(context.Background(), storage, WebChatModelCapability{
		Model:               "gpt-5",
		SupportsText:        true,
		SupportsImageInput:  true,
		SupportsFileContext: true,
	}, messages, true)

	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"gpt-5",
		"stream":true,
		"stream_options":{"include_usage":true},
		"messages":[{"role":"user","content":[
			{"type":"text","text":"Explain this image and notes\n\nAttached file notes:\nnotes"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}
		]}]
	}`, string(payload))
	storage.requireOpened("u/1/image.png")
}

func TestBuildWebChatCompletionsPayload_IncludesThinkingEffortWhenEnabled(t *testing.T) {
	messages := []WebChatMessage{{
		Role:        WebChatRoleUser,
		ContentText: "Think through the tradeoffs",
	}}

	payload, err := BuildWebChatCompletionsPayload(context.Background(), fakeWebChatStorageWithoutOpens(t), WebChatModelCapability{
		Model:            "gpt-5.4",
		SupportsText:     true,
		SupportsThinking: true,
		ThinkingEfforts:  []string{"medium", "high", "xhigh"},
	}, messages, true, WebChatCompletionsPayloadOptions{
		Thinking: WebChatThinkingConfig{Enabled: true, Effort: "high"},
	})

	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"gpt-5.4",
		"stream":true,
		"stream_options":{"include_usage":true},
		"reasoning_effort":"high",
		"messages":[{"role":"user","content":"Think through the tradeoffs"}]
	}`, string(payload))
}

func TestBuildWebChatCompletionsPayload_OnOffThinkingEmitsHigh(t *testing.T) {
	messages := []WebChatMessage{{
		Role:        WebChatRoleUser,
		ContentText: "Think through the tradeoffs",
	}}

	// GLM-style on/off toggle: SupportsThinking with no effort tiers. Enabling
	// thinking must emit reasoning_effort "high" (not the old "medium" default).
	payload, err := BuildWebChatCompletionsPayload(context.Background(), fakeWebChatStorageWithoutOpens(t), WebChatModelCapability{
		Model:            "glm-4.7",
		SupportsText:     true,
		SupportsThinking: true,
		ThinkingEfforts:  nil,
	}, messages, true, WebChatCompletionsPayloadOptions{
		Thinking: WebChatThinkingConfig{Enabled: true},
	})

	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"glm-4.7",
		"stream":true,
		"stream_options":{"include_usage":true},
		"reasoning_effort":"high",
		"messages":[{"role":"user","content":"Think through the tradeoffs"}]
	}`, string(payload))
}

func TestBuildWebChatCompletionsPayload_IncludesImageGenerationToolWhenEnabled(t *testing.T) {
	messages := []WebChatMessage{{
		Role:        WebChatRoleUser,
		ContentText: "Generate a wide hero image",
	}}

	payload, err := BuildWebChatCompletionsPayload(context.Background(), fakeWebChatStorageWithoutOpens(t), WebChatModelCapability{
		Provider:                     "openai",
		Platform:                     PlatformOpenAI,
		Model:                        "gpt-image-2",
		SupportsText:                 true,
		SupportsImageGeneration:      true,
		ImageGenerationSizes:         []string{"1024x1024", "1536x1024"},
		ImageGenerationAspectRatios:  []string{"1:1", "3:2"},
		ImageGenerationQualities:     []string{"low", "medium", "high"},
		ImageGenerationOutputFormats: []string{"png", "webp"},
		ImageGenerationBackgrounds:   []string{"opaque", "auto"},
	}, messages, true, WebChatCompletionsPayloadOptions{
		ImageGeneration: WebChatImageGenerationConfig{
			Enabled:      true,
			Size:         "1536x1024",
			AspectRatio:  "3:2",
			Quality:      "high",
			OutputFormat: "webp",
			Background:   "auto",
		},
	})

	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"gpt-image-2",
		"stream":true,
		"stream_options":{"include_usage":true},
		"tool_choice":{"type":"image_generation"},
		"tools":[{
			"type":"image_generation",
			"size":"1536x1024",
			"quality":"high",
			"output_format":"webp",
			"background":"auto"
		}],
		"messages":[{"role":"user","content":"Generate a wide hero image"}]
	}`, string(payload))
}

func TestBuildWebChatResponsesPayload_IncludesWebSearchToolChoice(t *testing.T) {
	messages := []WebChatMessage{{
		Role:        WebChatRoleUser,
		ContentText: "What is new in AI today?",
	}}
	caps := WebChatModelCapability{
		Provider:          "anthropic",
		Platform:          PlatformAnthropic,
		Model:             "claude-sonnet-4",
		SupportsText:      true,
		SupportsWebSearch: true,
	}

	forcedPayload, err := BuildWebChatResponsesPayload(context.Background(), fakeWebChatStorageWithoutOpens(t), caps, messages, true, WebChatCompletionsPayloadOptions{
		WebSearch: WebChatWebSearchConfig{Enabled: true},
	})

	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"claude-sonnet-4",
		"stream":true,
		"include":["reasoning.encrypted_content"],
		"store":false,
		"tools":[{"type":"web_search"}],
		"tool_choice":{"type":"web_search"},
		"input":[{"role":"user","content":"What is new in AI today?"}]
	}`, string(forcedPayload))

	autoPayload, err := BuildWebChatResponsesPayload(context.Background(), fakeWebChatStorageWithoutOpens(t), caps, messages, true, WebChatCompletionsPayloadOptions{
		WebSearch: WebChatWebSearchConfig{Enabled: false},
	})

	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"claude-sonnet-4",
		"stream":true,
		"include":["reasoning.encrypted_content"],
		"store":false,
		"tools":[{"type":"web_search"}],
		"tool_choice":"auto",
		"input":[{"role":"user","content":"What is new in AI today?"}]
	}`, string(autoPayload))
}

type fakeWebChatStorage struct {
	t            *testing.T
	files        map[string][]byte
	metaSizes    map[string]int64
	expectedKeys []string
	openedKeys   []string
}

func TestBuildWebChatCompletionsPayload_UnsupportedImageContextDoesNotOpenStorage(t *testing.T) {
	messages := []WebChatMessage{{
		Role:        WebChatRoleUser,
		ContentText: "Explain this image",
		Attachments: []WebChatAttachment{{
			Kind:        WebChatAttachmentKindImage,
			ContentType: "image/png",
			StorageKey:  "u/1/image.png",
		}},
	}}
	storage := fakeWebChatStorageWithoutOpens(t)

	payload, err := BuildWebChatCompletionsPayload(context.Background(), storage, WebChatModelCapability{
		Model:              "text-only",
		SupportsText:       true,
		SupportsImageInput: false,
	}, messages, true)

	require.ErrorIs(t, err, ErrWebChatUnsupportedContext)
	require.Nil(t, payload)
	storage.requireOpened()
}

func TestBuildWebChatCompletionsPayload_OmitsBinaryFilesAndStreamOptionsWhenNotStreaming(t *testing.T) {
	messages := []WebChatMessage{{
		Role:        WebChatRoleUser,
		ContentText: "Summarize the attachment",
		Attachments: []WebChatAttachment{{
			Kind:        WebChatAttachmentKindFile,
			Filename:    "paper.pdf",
			ContentType: "application/pdf",
			StorageKey:  "u/1/paper.pdf",
		}},
	}}
	storage := fakeWebChatStorageWithoutOpens(t)

	payload, err := BuildWebChatCompletionsPayload(context.Background(), storage, WebChatModelCapability{
		Model:               "gpt-5",
		SupportsText:        true,
		SupportsFileContext: true,
	}, messages, false)

	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"gpt-5",
		"stream":false,
		"messages":[{"role":"user","content":"Summarize the attachment"}]
	}`, string(payload))
	require.NotContains(t, string(payload), "stream_options")
	storage.requireOpened()
}

func TestBuildWebChatCompletionsPayload_AssistantAndSystemTextMessages(t *testing.T) {
	messages := []WebChatMessage{
		{Role: WebChatRoleSystem, ContentText: "Follow policy"},
		{Role: WebChatRoleAssistant, ContentText: "Previous answer"},
	}
	storage := fakeWebChatStorageWithoutOpens(t)

	payload, err := BuildWebChatCompletionsPayload(context.Background(), storage, WebChatModelCapability{
		Model:        "gpt-5",
		SupportsText: true,
	}, messages, false)

	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"gpt-5",
		"stream":false,
		"messages":[
			{"role":"system","content":"Follow policy"},
			{"role":"assistant","content":"Previous answer"}
		]
	}`, string(payload))
	storage.requireOpened()
}

func TestBuildWebChatCompletionsPayload_RejectsOversizedImageRead(t *testing.T) {
	messages := []WebChatMessage{{
		Role: WebChatRoleUser,
		Attachments: []WebChatAttachment{{
			Kind:        WebChatAttachmentKindImage,
			ContentType: "image/png",
			StorageKey:  "u/1/large.png",
		}},
	}}
	storage := fakeWebChatStorageWithFileMeta(t, "u/1/large.png", bytes.Repeat([]byte("x"), webChatMaxUploadBytes+1), 0)

	payload, err := BuildWebChatCompletionsPayload(context.Background(), storage, WebChatModelCapability{
		Model:              "gpt-5",
		SupportsImageInput: true,
	}, messages, false)

	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Nil(t, payload)
	storage.requireOpened("u/1/large.png")
}

func TestBuildWebChatCompletionsPayload_SanitizesFilePreviewFilename(t *testing.T) {
	messages := []WebChatMessage{{
		Role:        WebChatRoleUser,
		ContentText: "Use the notes",
		Attachments: []WebChatAttachment{{
			Kind:        WebChatAttachmentKindFile,
			Filename:    "bad\nname.txt",
			ContentType: "text/plain",
			TextPreview: webChatStringPtr("hello"),
		}},
	}}

	payload, err := BuildWebChatCompletionsPayload(context.Background(), fakeWebChatStorageWithoutOpens(t), WebChatModelCapability{
		Model:               "gpt-5",
		SupportsText:        true,
		SupportsFileContext: true,
	}, messages, false)

	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"gpt-5",
		"stream":false,
		"messages":[{"role":"user","content":"Use the notes\n\nAttached file bad_name.txt:\nhello"}]
	}`, string(payload))
}

func fakeWebChatStorageWithoutOpens(t *testing.T) *fakeWebChatStorage {
	t.Helper()
	return &fakeWebChatStorage{t: t, files: map[string][]byte{}}
}

func fakeStorageWithImage(t *testing.T, key string, image string) *fakeWebChatStorage {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(image)
	require.NoError(t, err)
	return fakeWebChatStorageWithFile(t, key, decoded)
}

func fakeWebChatStorageWithFile(t *testing.T, key string, data []byte) *fakeWebChatStorage {
	t.Helper()
	return fakeWebChatStorageWithFileMeta(t, key, data, int64(len(data)))
}

func fakeWebChatStorageWithFileMeta(t *testing.T, key string, data []byte, metaSize int64) *fakeWebChatStorage {
	t.Helper()
	return &fakeWebChatStorage{
		t:            t,
		files:        map[string][]byte{key: data},
		metaSizes:    map[string]int64{key: metaSize},
		expectedKeys: []string{key},
	}
}

func (s *fakeWebChatStorage) requireOpened(keys ...string) {
	s.t.Helper()
	require.Equal(s.t, keys, s.openedKeys)
}

func webChatStringPtr(v string) *string {
	return &v
}

func (s *fakeWebChatStorage) Save(context.Context, WebChatStorageSaveInput) (*WebChatStoredFile, error) {
	return nil, nil
}

func (s *fakeWebChatStorage) Open(_ context.Context, key string) (io.ReadCloser, WebChatStoredFileMeta, error) {
	s.t.Helper()
	if len(s.openedKeys) >= len(s.expectedKeys) {
		s.t.Fatalf("unexpected storage open for key %q", key)
	}
	expectedKey := s.expectedKeys[len(s.openedKeys)]
	require.Equal(s.t, expectedKey, key)
	data, ok := s.files[key]
	require.Truef(s.t, ok, "missing fake storage data for key %q", key)
	s.openedKeys = append(s.openedKeys, key)
	return io.NopCloser(bytes.NewReader(data)), WebChatStoredFileMeta{
		StorageKey: key,
		SizeBytes:  s.metaSizes[key],
	}, nil
}

func (s *fakeWebChatStorage) Delete(context.Context, string) error {
	return nil
}
