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
		Model:        "gpt-5",
		SupportsText: true,
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
