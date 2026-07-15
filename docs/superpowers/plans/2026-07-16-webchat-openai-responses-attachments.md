# WebChat OpenAI Responses Attachments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Make every GPT/OpenAI WebChat turn use /v1/responses, send images and supported files as native Responses inputs, expose automatic Web Search only from the backend, and remove GPT's frontend search control.

**Architecture:** Keep the current Chat Completions adapter and configurable search behavior for non-OpenAI providers. Add a focused OpenAI Responses builder that validates stored attachment bytes, emits typed input_text/input_image/input_file parts, and injects web_search with tool_choice "auto"; provider "openai" uses that builder unconditionally. The frontend retains configurable search state for non-OpenAI models but treats OpenAI search as server-managed.

**Tech Stack:** Go, Gin, OpenAI Responses compatibility types, Testify/GJSON, Vue 3, Pinia, TypeScript, Vitest, graphify.

## Global Constraints

- Scope is GPT/OpenAI WebChat input only; do not change Claude, Anthropic, Kiro, Gemini, or other provider attachment forwarding.
- This plan does not add ordinary GPT image generation or PPT/PDF output generation. Existing gpt-image-2 output remains supported.
- Every GPT/OpenAI WebChat input—text, image, file, or mixed—uses /v1/responses with store false.
- A searchable GPT model always receives tools [{"type":"web_search"}] and tool_choice "auto"; never force a search call.
- GPT/OpenAI renders no search toggle and no search explanation copy.
- Keep the 20 MiB per-attachment limit and reject more than 50 MiB of raw input_file data in one Responses request.
- PDF input_file uses detail "auto"; non-PDF input_file parts omit detail.
- Never omit an invalid attachment and continue as text-only.
- Preserve non-OpenAI search controls and legacy Chat Completions paths.
- Do not modify or deploy production. A real OpenAI OAuth input_file request needs separate user approval and must use the existing WebChat/OpenAI flow and configured proxy.
- Preserve unrelated dirty files; do not stage pre-existing graphify-out or documentation changes.

---

### Task 1: Correct GPT input and search capabilities

**Files:**
- Modify: backend/internal/service/web_chat_capabilities.go
- Modify: backend/internal/service/web_chat_catalog_dynamic.go
- Test: backend/internal/service/web_chat_capabilities_test.go

**Interfaces:**
- Consumes: resolveWebChatModelFamily(model string) webChatModelFamily.
- Produces: isOpenAIWebChatGPTTextModel(provider, model string, supportsImageGeneration bool) bool.

- [ ] **Step 1: Write failing catalog and fallback tests**

Extend the existing OpenAI catalog test:

~~~go
func TestWebChatCapabilities_DerivesOpenAIWebSearchForTextModel(t *testing.T) {
	caps, ok := WebChatModelCapabilityFromCatalogModel(WebChatCatalogModel{
		Provider:    "openai",
		ModelName:   "gpt-5.5",
		DisplayName: "GPT-5.5",
		Modalities:  []string{"text"},
		Features:    []string{"reasoning"},
		PriceStatus: "confirmed",
	})

	require.True(t, ok)
	require.True(t, caps.SupportsText)
	require.True(t, caps.SupportsImageInput)
	require.True(t, caps.SupportsFileContext)
	require.True(t, caps.SupportsThinking)
	require.True(t, caps.SupportsWebSearch)
	require.False(t, caps.SupportsImageGeneration)
}
~~~

Add the dynamic fallback case:

~~~go
func TestResolveWebChatCatalog_OpenAIGPTFallbackGetsNativeInputsAndSearch(t *testing.T) {
	gr := stubGroupResolver{ids: map[string]int64{APIKeyTypeOpenAI: 2}}
	al := stubAccountLister{byGroup: map[int64][]Account{
		2: {acctWithMapping(PlatformOpenAI, "gpt-5.6-custom")},
	}}

	got, err := resolveWebChatCatalog(context.Background(), gr, al)

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, got[0].SupportsImageInput)
	require.True(t, got[0].SupportsFileContext)
	require.True(t, got[0].SupportsWebSearch)
}
~~~

Run:

~~~bash
cd backend
go test ./internal/service -run 'Test(WebChatCapabilities_DerivesOpenAIWebSearchForTextModel|ResolveWebChatCatalog_OpenAIGPTFallbackGetsNativeInputsAndSearch)$' -count=1
~~~

Expected: FAIL because text-only GPT catalog entries lack image input and fallback GPT entries lack image/search flags.

- [ ] **Step 2: Add one GPT text-model predicate and apply it in both builders**

Add to web_chat_capabilities.go:

~~~go
func isOpenAIWebChatGPTTextModel(provider, model string, supportsImageGeneration bool) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "openai") &&
		resolveWebChatModelFamily(model) == webChatFamilyGPT &&
		!supportsImageGeneration
}
~~~

Calculate supportsImageGeneration before supportsImageInput, then augment the latter:

~~~go
supportsImageGeneration := hasImageModality || containsFold(model.Features, "image generation")
supportsImageInput := hasImageModality || containsFold(model.Features, "vision input")
if isOpenAIWebChatGPTTextModel(provider, model.ModelName, supportsImageGeneration) {
	supportsImageInput = true
}
~~~

At the end of buildWebChatCapability add:

~~~go
if isOpenAIWebChatGPTTextModel(provider, base, caps.SupportsImageGeneration) {
	caps.SupportsImageInput = true
	caps.SupportsFileContext = true
	caps.SupportsWebSearch = true
}
~~~

The supportsImageGeneration guard keeps gpt-image-* from inheriting thinking or search.

- [ ] **Step 3: Verify and commit**

~~~bash
cd backend
gofmt -w internal/service/web_chat_capabilities.go internal/service/web_chat_catalog_dynamic.go internal/service/web_chat_capabilities_test.go
go test ./internal/service -run 'Test(WebChatCapabilities|ResolveWebChatCatalog)' -count=1
cd ..
git add backend/internal/service/web_chat_capabilities.go backend/internal/service/web_chat_catalog_dynamic.go backend/internal/service/web_chat_capabilities_test.go
git commit -m "fix(webchat): expose GPT attachment capabilities"
~~~

Expected: PASS, including the existing gpt-image-2 non-inheritance test.

---

### Task 2: Add native Responses file fields and validated attachment reads

**Files:**
- Modify: backend/internal/pkg/apicompat/types.go
- Create: backend/internal/service/web_chat_openai_responses.go
- Create: backend/internal/service/web_chat_openai_responses_test.go
- Modify: backend/internal/service/web_chat_adapter.go
- Modify: backend/internal/service/web_chat_adapter_test.go

**Interfaces:**
- Consumes: WebChatStorage.Open, classifyWebChatUploadContentType, webChatMaxUploadBytes, and ErrWebChatUploadRejected.
- Produces: readWebChatStoredAttachment(ctx, storage, attachment) returning bytes plus normalized MIME type.

- [ ] **Step 1: Write failing attachment validation tests**

Create web_chat_openai_responses_test.go:

~~~go
package service

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadWebChatStoredAttachmentAcceptsPDFAndDOCX(t *testing.T) {
	docx := testWebChatDOCX(t)
	pdf := []byte("%PDF-1.7\n%%EOF")
	storage := &fakeWebChatStorage{
		t: t,
		files: map[string][]byte{"paper.pdf": pdf, "notes.docx": docx},
		metaSizes: map[string]int64{"paper.pdf": int64(len(pdf)), "notes.docx": int64(len(docx))},
		expectedKeys: []string{"paper.pdf", "notes.docx"},
	}

	gotPDF, pdfType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile, ContentType: "application/pdf", StorageKey: "paper.pdf",
	})
	require.NoError(t, err)
	require.Equal(t, pdf, gotPDF)
	require.Equal(t, "application/pdf", pdfType)

	gotDOCX, docxType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile,
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		StorageKey: "notes.docx",
	})
	require.NoError(t, err)
	require.Equal(t, docx, gotDOCX)
	require.Equal(t, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", docxType)
}

func TestReadWebChatStoredAttachmentRejectsDeclaredPDFWithNonPDFBytes(t *testing.T) {
	storage := fakeWebChatStorageWithFile(t, "paper.pdf", []byte("not a pdf"))

	data, contentType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile, ContentType: "application/pdf", StorageKey: "paper.pdf",
	})

	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Nil(t, data)
	require.Empty(t, contentType)
}

func testWebChatDOCX(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string]string{
		"[Content_Types].xml": "<Types/>",
		"word/document.xml": "<w:document/>",
	} {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}
~~~

Run:

~~~bash
cd backend
go test ./internal/service -run '^TestReadWebChatStoredAttachment' -count=1
~~~

Expected: FAIL to compile because readWebChatStoredAttachment does not exist.

- [ ] **Step 2: Extend ResponsesContentPart**

Replace the type with:

~~~go
type ResponsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	FileData string `json:"file_data,omitempty"`
	Filename string `json:"filename,omitempty"`
	Detail   string `json:"detail,omitempty"`
}
~~~

All new fields are omitempty, so existing output-event serialization remains unchanged.

- [ ] **Step 3: Implement bounded reads and byte/type checks**

Create web_chat_openai_responses.go with the following primitives:

~~~go
package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const webChatMaxResponsesFileBytes int64 = 50 << 20

func readWebChatStoredAttachment(ctx context.Context, storage WebChatStorage, attachment WebChatAttachment) ([]byte, string, error) {
	if storage == nil || strings.TrimSpace(attachment.StorageKey) == "" {
		return nil, "", ErrWebChatUploadRejected
	}
	if attachment.SizeBytes > webChatMaxUploadBytes {
		return nil, "", ErrWebChatUploadRejected
	}

	reader, meta, err := storage.Open(ctx, attachment.StorageKey)
	if err != nil {
		return nil, "", fmt.Errorf("%w: open stored attachment: %v", ErrWebChatUploadRejected, err)
	}
	defer func() { _ = reader.Close() }()
	if meta.SizeBytes > webChatMaxUploadBytes {
		return nil, "", ErrWebChatUploadRejected
	}

	data, err := io.ReadAll(io.LimitReader(reader, webChatMaxUploadBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: read stored attachment: %v", ErrWebChatUploadRejected, err)
	}
	if len(data) > webChatMaxUploadBytes {
		return nil, "", ErrWebChatUploadRejected
	}

	contentType, kind, _, err := classifyWebChatUploadContentType(attachment.ContentType, data)
	if err != nil || kind != attachment.Kind || !webChatStoredContentMatchesType(contentType, data) {
		return nil, "", ErrWebChatUploadRejected
	}
	return data, contentType, nil
}

func webChatStoredContentMatchesType(contentType string, data []byte) bool {
	switch contentType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		detected := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(data), ";", 2)[0]))
		return detected == contentType
	case "application/pdf":
		return bytes.HasPrefix(data, []byte("%PDF-"))
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return false
		}
		hasContentTypes := false
		hasDocument := false
		for _, file := range archive.File {
			if file.Name == "[Content_Types].xml" {
				hasContentTypes = true
			}
			if file.Name == "word/document.xml" {
				hasDocument = true
			}
		}
		return hasContentTypes && hasDocument
	case "text/plain", "text/markdown", "application/json", "text/csv":
		return webChatBodyLooksText(data)
	default:
		return false
	}
}

func webChatAttachmentDataURL(contentType string, data []byte) string {
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func webChatResponsesFilename(attachment WebChatAttachment, contentType string) string {
	if strings.TrimSpace(attachment.Filename) != "" {
		return sanitizeWebChatDisplayFilename(attachment.Filename)
	}
	fallbacks := map[string]string{
		"application/pdf": "attachment.pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "attachment.docx",
		"text/plain": "attachment.txt",
		"text/markdown": "attachment.md",
		"application/json": "attachment.json",
		"text/csv": "attachment.csv",
	}
	if filename := fallbacks[contentType]; filename != "" {
		return filename
	}
	return "attachment"
}
~~~

- [ ] **Step 4: Count every file during capability validation**

In validateWebChatAdapterContext use:

~~~go
case WebChatAttachmentKindFile:
	summary.FileAttachmentCount++
~~~

Update TestBuildWebChatCompletionsPayload_OmitsBinaryFilesAndStreamOptionsWhenNotStreaming to set SupportsFileContext true. This keeps the compatibility-path test valid while preventing a previewless PDF from bypassing capability checks.

- [ ] **Step 5: Verify and commit**

~~~bash
cd backend
gofmt -w internal/pkg/apicompat/types.go internal/service/web_chat_openai_responses.go internal/service/web_chat_openai_responses_test.go internal/service/web_chat_adapter.go internal/service/web_chat_adapter_test.go
go test ./internal/service -run 'Test(ReadWebChatStoredAttachment|BuildWebChatCompletionsPayload)' -count=1
go test ./internal/pkg/apicompat -count=1
cd ..
git add backend/internal/pkg/apicompat/types.go backend/internal/service/web_chat_openai_responses.go backend/internal/service/web_chat_openai_responses_test.go backend/internal/service/web_chat_adapter.go backend/internal/service/web_chat_adapter_test.go
git commit -m "feat(webchat): validate native Responses attachments"
~~~

Expected: PASS and no apicompat regression.

---

### Task 3: Build the OpenAI-only native Responses request

**Files:**
- Modify: backend/internal/service/web_chat_openai_responses.go
- Modify: backend/internal/service/web_chat_openai_responses_test.go
- Modify: backend/internal/service/web_chat_adapter_test.go

**Interfaces:**
- Consumes: Task 2 attachment primitives and apicompat Responses request types.
- Produces: BuildOpenAIWebChatResponsesPayload(ctx, storage, caps, messages, stream, options) ([]byte, error).

- [ ] **Step 1: Add failing mixed-input and tool tests**

Add a test whose expected request is:

~~~json
{
  "model": "gpt-5.5",
  "stream": true,
  "include": ["reasoning.encrypted_content"],
  "store": false,
  "tools": [{"type": "web_search"}],
  "tool_choice": "auto",
  "input": [{
    "role": "user",
    "content": [
      {"type": "input_text", "text": "Compare these"},
      {"type": "input_image", "image_url": "data:image/png;base64,iVBORw0KGgo="},
      {"type": "input_file", "file_data": "data:application/pdf;base64,JVBERi0xLjcKJSVFT0Y=", "filename": "paper.pdf", "detail": "auto"}
    ]
  }]
}
~~~

Use an image byte slice containing the eight-byte PNG signature and a PDF containing %PDF-1.7. Pass WebSearch configured false to prove the OpenAI builder ignores the legacy switch.

Add github.com/tidwall/gjson to the test imports and implement the mixed-input test:

~~~go
func TestBuildOpenAIWebChatResponsesPayload_MixedInputsAndAutomaticSearch(t *testing.T) {
	pdf := []byte("%PDF-1.7\n%%EOF")
	image := []byte("\x89PNG\r\n\x1a\n")
	storage := &fakeWebChatStorage{
		t: t,
		files: map[string][]byte{"diagram.png": image, "paper.pdf": pdf},
		metaSizes: map[string]int64{"diagram.png": int64(len(image)), "paper.pdf": int64(len(pdf))},
		expectedKeys: []string{"diagram.png", "paper.pdf"},
	}
	messages := []WebChatMessage{{
		Role: WebChatRoleUser,
		ContentText: "Compare these",
		Attachments: []WebChatAttachment{
			{Kind: WebChatAttachmentKindImage, Filename: "diagram.png", ContentType: "image/png", StorageKey: "diagram.png"},
			{Kind: WebChatAttachmentKindFile, Filename: "paper.pdf", ContentType: "application/pdf", StorageKey: "paper.pdf"},
		},
	}}

	payload, err := BuildOpenAIWebChatResponsesPayload(context.Background(), storage, WebChatModelCapability{
		Provider: "openai", Platform: PlatformOpenAI, Model: "gpt-5.5",
		SupportsText: true, SupportsImageInput: true, SupportsFileContext: true, SupportsWebSearch: true,
	}, messages, true, WebChatCompletionsPayloadOptions{
		WebSearch: WebChatWebSearchConfig{Configured: true, Enabled: false},
	})

	require.NoError(t, err)
	require.Equal(t, "input_text", gjson.GetBytes(payload, "input.0.content.0.type").String())
	require.Equal(t, "data:image/png;base64,iVBORw0KGgo=", gjson.GetBytes(payload, "input.0.content.1.image_url").String())
	require.Equal(t, "data:application/pdf;base64,JVBERi0xLjcKJSVFT0Y=", gjson.GetBytes(payload, "input.0.content.2.file_data").String())
	require.Equal(t, "paper.pdf", gjson.GetBytes(payload, "input.0.content.2.filename").String())
	require.Equal(t, "auto", gjson.GetBytes(payload, "input.0.content.2.detail").String())
	require.Equal(t, "web_search", gjson.GetBytes(payload, "tools.0.type").String())
	require.Equal(t, "auto", gjson.GetBytes(payload, "tool_choice").String())
}
~~~

Add the remaining edge tests:

~~~go
func TestBuildOpenAIWebChatResponsesPayload_DOCXOmitsDetail(t *testing.T) {
	docx := testWebChatDOCX(t)
	storage := fakeWebChatStorageWithFile(t, "notes.docx", docx)
	payload, err := BuildOpenAIWebChatResponsesPayload(context.Background(), storage, WebChatModelCapability{
		Provider: "openai", Platform: PlatformOpenAI, Model: "gpt-5.5",
		SupportsFileContext: true,
	}, []WebChatMessage{{Role: WebChatRoleUser, Attachments: []WebChatAttachment{{
		Kind: WebChatAttachmentKindFile,
		Filename: "notes.docx",
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		StorageKey: "notes.docx",
	}}}}, false)

	require.NoError(t, err)
	require.Equal(t, "input_file", gjson.GetBytes(payload, "input.0.content.0.type").String())
	require.False(t, gjson.GetBytes(payload, "input.0.content.0.detail").Exists())
}

func TestBuildOpenAIWebChatResponsesPayload_RejectsMoreThanFiftyMiBOfFiles(t *testing.T) {
	attachments := make([]WebChatAttachment, 0, 3)
	for i := int64(1); i <= 3; i++ {
		attachments = append(attachments, WebChatAttachment{
			ID: i, Kind: WebChatAttachmentKindFile, Filename: "large.pdf",
			ContentType: "application/pdf", SizeBytes: 18 << 20, StorageKey: "large.pdf",
		})
	}
	storage := fakeWebChatStorageWithoutOpens(t)

	payload, err := BuildOpenAIWebChatResponsesPayload(context.Background(), storage, WebChatModelCapability{
		Provider: "openai", Platform: PlatformOpenAI, Model: "gpt-5.5",
		SupportsFileContext: true,
	}, []WebChatMessage{{Role: WebChatRoleUser, Attachments: attachments}}, false)

	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Nil(t, payload)
	storage.requireOpened()
}

func TestBuildOpenAIWebChatResponsesPayload_PreservesRoles(t *testing.T) {
	payload, err := BuildOpenAIWebChatResponsesPayload(context.Background(), fakeWebChatStorageWithoutOpens(t), WebChatModelCapability{
		Provider: "openai", Platform: PlatformOpenAI, Model: "gpt-5.5", SupportsText: true,
	}, []WebChatMessage{
		{Role: WebChatRoleSystem, ContentText: "Follow policy"},
		{Role: WebChatRoleAssistant, ContentText: "Previous answer"},
	}, false)

	require.NoError(t, err)
	require.Equal(t, "system", gjson.GetBytes(payload, "input.0.role").String())
	require.Equal(t, "input_text", gjson.GetBytes(payload, "input.0.content.0.type").String())
	require.Equal(t, "assistant", gjson.GetBytes(payload, "input.1.role").String())
	require.Equal(t, "output_text", gjson.GetBytes(payload, "input.1.content.0.type").String())
}

func TestBuildOpenAIWebChatResponsesPayload_ImageGenerationModelHasNoSearchTool(t *testing.T) {
	payload, err := BuildOpenAIWebChatResponsesPayload(context.Background(), fakeWebChatStorageWithoutOpens(t), WebChatModelCapability{
		Provider: "openai", Platform: PlatformOpenAI, Model: "gpt-image-2",
		SupportsText: true, SupportsImageGeneration: true,
		ImageGenerationSizes: []string{"1024x1024"},
	}, []WebChatMessage{{Role: WebChatRoleUser, ContentText: "Draw a cat"}}, true,
		WebChatCompletionsPayloadOptions{ImageGeneration: WebChatImageGenerationConfig{Enabled: true}},
	)

	require.NoError(t, err)
	require.Equal(t, "image_generation", gjson.GetBytes(payload, "tools.0.type").String())
	require.Equal(t, "image_generation", gjson.GetBytes(payload, "tool_choice.type").String())
	require.NotContains(t, string(payload), "web_search")
}
~~~

Run:

~~~bash
cd backend
go test ./internal/service -run '^TestBuildOpenAIWebChatResponsesPayload' -count=1
~~~

Expected: FAIL to compile because the builder does not exist.

- [ ] **Step 2: Construct typed input items**

Add encoding/json and the apicompat package import, then add:

~~~go
func buildOpenAIWebChatResponsesInput(ctx context.Context, storage WebChatStorage, messages []WebChatMessage) ([]apicompat.ResponsesInputItem, error) {
	items := make([]apicompat.ResponsesInputItem, 0, len(messages))
	var declaredFileBytes int64
	for _, message := range messages {
		if message.Role != WebChatRoleUser {
			continue
		}
		for _, attachment := range message.Attachments {
			if attachment.Kind != WebChatAttachmentKindFile {
				continue
			}
			declaredFileBytes += attachment.SizeBytes
			if declaredFileBytes > webChatMaxResponsesFileBytes {
				return nil, ErrWebChatUploadRejected
			}
		}
	}
	var actualFileBytes int64

	for _, message := range messages {
		parts := make([]apicompat.ResponsesContentPart, 0, 1+len(message.Attachments))
		if message.ContentText != "" {
			partType := "input_text"
			if message.Role == WebChatRoleAssistant {
				partType = "output_text"
			}
			parts = append(parts, apicompat.ResponsesContentPart{Type: partType, Text: message.ContentText})
		}

		if message.Role == WebChatRoleUser {
			for _, attachment := range message.Attachments {
				data, contentType, err := readWebChatStoredAttachment(ctx, storage, attachment)
				if err != nil {
					return nil, err
				}
				switch attachment.Kind {
				case WebChatAttachmentKindImage:
					parts = append(parts, apicompat.ResponsesContentPart{
						Type: "input_image", ImageURL: webChatAttachmentDataURL(contentType, data),
					})
				case WebChatAttachmentKindFile:
					actualFileBytes += int64(len(data))
					if actualFileBytes > webChatMaxResponsesFileBytes {
						return nil, ErrWebChatUploadRejected
					}
					part := apicompat.ResponsesContentPart{
						Type: "input_file",
						FileData: webChatAttachmentDataURL(contentType, data),
						Filename: webChatResponsesFilename(attachment, contentType),
					}
					if contentType == "application/pdf" {
						part.Detail = "auto"
					}
					parts = append(parts, part)
				default:
					return nil, ErrWebChatUploadRejected
				}
			}
		}

		if len(parts) == 0 {
			continue
		}
		content, err := json.Marshal(parts)
		if err != nil {
			return nil, fmt.Errorf("marshal web chat Responses content: %w", err)
		}
		items = append(items, apicompat.ResponsesInputItem{Role: message.Role, Content: content})
	}
	return items, nil
}
~~~

This preserves the stored attachment slice order and rejects both declared and actual aggregate file sizes.

- [ ] **Step 3: Add the OpenAI request and tool builder**

~~~go
func BuildOpenAIWebChatResponsesPayload(ctx context.Context, storage WebChatStorage, caps WebChatModelCapability, messages []WebChatMessage, stream bool, options ...WebChatCompletionsPayloadOptions) ([]byte, error) {
	if err := validateWebChatAdapterContext(caps, messages); err != nil {
		return nil, err
	}
	input, err := buildOpenAIWebChatResponsesInput(ctx, storage, messages)
	if err != nil {
		return nil, err
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal web chat Responses input: %w", err)
	}

	store := false
	request := apicompat.ResponsesRequest{
		Model: caps.Model,
		Input: inputJSON,
		Stream: stream,
		Include: []string{"reasoning.encrypted_content"},
		Store: &store,
	}
	payloadOptions := firstWebChatPayloadOptions(options)
	if effort, ok := normalizeWebChatThinkingEffort(caps, payloadOptions.Thinking); ok {
		request.Reasoning = &apicompat.ResponsesReasoning{Effort: effort, Summary: "auto"}
	}
	if tool, ok := buildWebChatImageGenerationTool(caps, payloadOptions.ImageGeneration); ok {
		request.Tools = append(request.Tools, webChatResponsesImageGenerationTool(tool))
		request.ToolChoice = json.RawMessage("{"type":"image_generation"}")
	}
	if caps.SupportsWebSearch {
		request.Tools = appendWebChatResponsesTool(request.Tools, apicompat.ResponsesTool{Type: "web_search"})
		request.ToolChoice = json.RawMessage(""auto"")
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal OpenAI web chat Responses payload: %w", err)
	}
	return payload, nil
}

func webChatResponsesImageGenerationTool(tool webChatCCTool) apicompat.ResponsesTool {
	return apicompat.ResponsesTool{
		Type: tool.Type,
		Model: tool.Model,
		Size: tool.Size,
		AspectRatio: tool.AspectRatio,
		Quality: tool.Quality,
		OutputFormat: tool.OutputFormat,
		Background: tool.Background,
	}
}
~~~

The OpenAI builder intentionally never reads payloadOptions.WebSearch.

- [ ] **Step 4: Preserve the old builder as the non-OpenAI configurable-search path**

Change TestBuildWebChatResponsesPayload_IncludesWebSearchToolChoice to an Anthropic capability:

~~~go
caps := WebChatModelCapability{
	Provider: "anthropic",
	Platform: PlatformAnthropic,
	Model: "claude-sonnet-4",
	SupportsText: true,
	SupportsWebSearch: true,
}
~~~

Keep its enabled-true forced choice and enabled-false auto choice assertions.
Update both expected payloads' model field to claude-sonnet-4.

- [ ] **Step 5: Verify and commit**

~~~bash
cd backend
gofmt -w internal/service/web_chat_openai_responses.go internal/service/web_chat_openai_responses_test.go internal/service/web_chat_adapter_test.go
go test ./internal/service -run 'Test(BuildOpenAIWebChatResponsesPayload|BuildWebChatResponsesPayload)' -count=1
go test ./internal/pkg/apicompat -count=1
cd ..
git add backend/internal/service/web_chat_openai_responses.go backend/internal/service/web_chat_openai_responses_test.go backend/internal/service/web_chat_adapter_test.go
git commit -m "feat(webchat): build native OpenAI Responses inputs"
~~~

Expected: PASS for mixed inputs, role mapping, aggregate limits, automatic search, and existing Anthropic behavior.

---

### Task 4: Route every OpenAI WebChat message through Responses

**Files:**
- Modify: backend/internal/service/web_chat_dispatch.go
- Modify: backend/internal/service/web_chat_service_test.go

**Interfaces:**
- Consumes: BuildOpenAIWebChatResponsesPayload.
- Produces: webChatUseResponsesPayload returning true for provider openai regardless of content or search config.

- [ ] **Step 1: Rewrite service regressions to fail on current routing**

Rename the OpenAI search service test to TestWebChatSend_OpenAIAlwaysUsesResponsesWithAutomaticSearch, omit WebSearch from its input, and assert:

~~~go
requireOrderedEvents(t, svc.events, "forward_openai_responses", "record_openai_usage", "usage_lookup")
require.Equal(t, "/v1/responses", svc.openAIRecordUsageInput.UpstreamEndpoint)
require.Equal(t, "web_search", gjson.GetBytes(svc.forwardedBody, "tools.0.type").String())
require.Equal(t, "auto", gjson.GetBytes(svc.forwardedBody, "tool_choice").String())
require.Equal(t, "search today's AI news", gjson.GetBytes(svc.forwardedBody, "input.1.content.0.text").String())
~~~

Add the legacy-client regression:

~~~go
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
		WebSearch: WebChatWebSearchConfig{Configured: true, Enabled: false},
		GinContext: newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.Equal(t, "web_search", gjson.GetBytes(svc.forwardedBody, "tools.0.type").String())
	require.Equal(t, "auto", gjson.GetBytes(svc.forwardedBody, "tool_choice").String())
}
~~~

Update TestWebChatSend_SavesOpenAIImageResultsAsArtifacts with these assertions:

~~~go
requireOrderedEvents(t, svc.events, "forward_openai_responses", "record_openai_usage", "usage_lookup")
require.Equal(t, "/v1/responses", svc.openAIRecordUsageInput.UpstreamEndpoint)
require.Equal(t, "image_generation", gjson.GetBytes(svc.forwardedBody, "tools.0.type").String())
require.Equal(t, "image_generation", gjson.GetBytes(svc.forwardedBody, "tool_choice.type").String())
require.NotContains(t, string(svc.forwardedBody), "web_search")
~~~

Run:

~~~bash
cd backend
go test ./internal/service -run 'TestWebChatSend_(OpenAI|SavesOpenAIImageResults)' -count=1
~~~

Expected: FAIL because unconfigured OpenAI text still uses Chat Completions and enabled search currently forces the tool.

- [ ] **Step 2: Select the native builder only for provider openai**

Add strings to web_chat_dispatch.go and replace body construction with:

~~~go
useResponsesPayload := webChatUseResponsesPayload(input)
var body []byte
switch {
case strings.EqualFold(strings.TrimSpace(input.Capabilities.Provider), "openai"):
	body, err = BuildOpenAIWebChatResponsesPayload(ctx, s.storage, input.Capabilities, input.Messages, input.Stream, payloadOptions)
case useResponsesPayload:
	body, err = BuildWebChatResponsesPayload(ctx, s.storage, input.Capabilities, input.Messages, input.Stream, payloadOptions)
default:
	body, err = BuildWebChatCompletionsPayload(ctx, s.storage, input.Capabilities, input.Messages, input.Stream, payloadOptions)
}
~~~

Replace the routing predicate with:

~~~go
func webChatUseResponsesPayload(input webChatDispatchInput) bool {
	if strings.EqualFold(strings.TrimSpace(input.Capabilities.Provider), "openai") {
		return true
	}
	if !input.Capabilities.SupportsWebSearch {
		return false
	}
	if !input.WebSearch.Configured && !input.WebSearch.Enabled {
		return false
	}
	return input.Capabilities.Platform == PlatformAnthropic
}
~~~

Do not remove forwardWebChatOpenAI or public Chat Completions compatibility code.

- [ ] **Step 3: Make attachment service fixtures contain real PNG bytes**

Replace webChatStorageStub.Open with:

~~~go
func (s webChatStorageStub) Open(context.Context, string) (io.ReadCloser, WebChatStoredFileMeta, error) {
	data := []byte("PNG

")
	return io.NopCloser(bytes.NewReader(data)), WebChatStoredFileMeta{SizeBytes: int64(len(data))}, nil
}
~~~

Add bytes to the service test imports.

- [ ] **Step 4: Verify OpenAI and Anthropic dispatch, then commit**

~~~bash
cd backend
gofmt -w internal/service/web_chat_dispatch.go internal/service/web_chat_service_test.go
go test ./internal/service -run 'TestWebChatSend_(OpenAI|AnthropicWebSearch|SavesOpenAIImageResults|UsesCreateMessageAttachments)' -count=1
go test ./internal/service -count=1
cd ..
git add backend/internal/service/web_chat_dispatch.go backend/internal/service/web_chat_service_test.go
git commit -m "fix(webchat): route OpenAI turns through Responses"
~~~

Expected: OpenAI calls only the Responses forwarder; configured Anthropic search still uses its Responses compatibility path; ordinary Anthropic behavior remains unchanged.

---

### Task 5: Remove GPT's search control without changing non-OpenAI controls

**Files:**
- Modify: frontend/src/stores/chat.ts
- Modify: frontend/src/components/chat/Composer.vue
- Modify: frontend/src/components/chat/__tests__/chatStore.spec.ts
- Modify: frontend/src/components/chat/__tests__/ChatView.spec.ts

**Interfaces:**
- Consumes: WebChatModel.provider and supports_web_search.
- Produces: selectedModelHasConfigurableWebSearch, true only for search-capable non-OpenAI models.

- [ ] **Step 1: Add failing GPT and Anthropic frontend tests**

Add this fixture to chatStore.spec.ts:

~~~ts
const anthropicWebSearchModel: WebChatModel = {
  ...webSearchModel,
  provider: 'anthropic',
  platform: 'anthropic',
  key_type: 'anthropic',
  model: 'claude-sonnet-4',
  display_name: 'Claude Sonnet 4',
}
~~~

Import WebChatConversationDetail, then add these helpers and replace the current search request test with two complete cases:

~~~ts
function emptyWebSearchConversation(model: WebChatModel): WebChatConversationDetail {
  return {
    conversation: {
      id: 7,
      title: 'Search',
      default_model: model.model,
      default_provider: model.provider,
      last_model: model.model,
      last_provider: model.provider,
      status: 'active',
      message_count: 0,
      created_at: '2026-06-22T00:00:00Z',
      updated_at: '2026-06-22T00:00:00Z',
    },
    messages: [],
  }
}

function mockCompletedWebChatStream() {
  return vi.spyOn(chatAPI, 'sendMessageStream').mockResolvedValue({
    response: new Response('data: [DONE]\n\n', {
      status: 200,
      headers: {
        'X-Web-Chat-User-Message-ID': '100',
        'X-Web-Chat-Assistant-Message-ID': '101',
      },
    }),
    userMessageId: 100,
    assistantMessageId: 101,
  })
}

it('never sends a frontend search config for OpenAI', async () => {
  const streamSpy = mockCompletedWebChatStream()
  const store = useChatStore()
  store.selectedModel = webSearchModel
  store.currentConversation = emptyWebSearchConversation(webSearchModel)
  store.webSearchEnabled = true

  await store.sendMessage('Use server-managed search')

  expect(streamSpy.mock.calls[0][1]).not.toHaveProperty('web_search')
})

it('keeps configurable search for a non-OpenAI model', async () => {
  const streamSpy = mockCompletedWebChatStream()
  const store = useChatStore()
  store.selectedModel = anthropicWebSearchModel
  store.currentConversation = emptyWebSearchConversation(anthropicWebSearchModel)
  store.webSearchEnabled = true

  await store.sendMessage('Search with Claude')

  expect(streamSpy.mock.calls[0][1]).toMatchObject({
    model: 'claude-sonnet-4',
    provider: 'anthropic',
    web_search: { enabled: true },
  })
})
~~~

In ChatView.spec.ts add:

~~~ts
it('does not render a web search control or explanation for GPT', async () => {
  const store = useChatStore()
  store.selectedModel = chatModel

  const wrapper = mount(Composer)
  await wrapper.get('[data-testid="chat-options-toggle"]').trigger('click')

  expect(wrapper.find('[data-testid="chat-web-search-toggle"]').exists()).toBe(false)
  expect(wrapper.text()).not.toContain('Web search')
  expect(wrapper.text()).not.toContain('联网')
})
~~~

Add the companion non-OpenAI control test:

~~~ts
it('keeps the web search control for a searchable non-OpenAI model', async () => {
  const store = useChatStore()
  store.selectedModel = {
    ...anthropicModel,
    supports_web_search: true,
  }

  const wrapper = mount(Composer)
  await wrapper.get('[data-testid="chat-options-toggle"]').trigger('click')

  expect(wrapper.get('[data-testid="chat-web-search-toggle"]').exists()).toBe(true)
})
~~~

Run:

~~~bash
pnpm --dir frontend exec vitest run src/components/chat/__tests__/chatStore.spec.ts src/components/chat/__tests__/ChatView.spec.ts
~~~

Expected: FAIL because GPT still renders and sends the local search configuration.

- [ ] **Step 2: Add the provider-aware computed and update request construction**

Replace selectedModelSupportsWebSearch in chat.ts with:

~~~ts
const selectedModelHasConfigurableWebSearch = computed(() => {
  const model = selectedModel.value
  return Boolean(model?.supports_web_search && model.provider.trim().toLowerCase() !== 'openai')
})
~~~

Use selectedModelHasConfigurableWebSearch in reconcileWebSearchSettings, sendMessage, and the returned store API:

~~~ts
if (selectedModelHasConfigurableWebSearch.value && webSearchEnabled.value) {
  request.web_search = { enabled: true }
}
~~~

Keep webSearchEnabled because non-OpenAI providers still use it.

- [ ] **Step 3: Render and toggle only configurable non-OpenAI search**

Use selectedModelHasConfigurableWebSearch in the Composer template, hasModelOptions, and toggleWebSearch:

~~~ts
const hasModelOptions = computed(() =>
  chatStore.selectedModelSupportsThinking ||
  chatStore.selectedModelHasConfigurableWebSearch ||
  chatStore.selectedModelSupportsImageGeneration
)

function toggleWebSearch(): void {
  if (!chatStore.selectedModelHasConfigurableWebSearch) return
  chatStore.webSearchEnabled = !chatStore.webSearchEnabled
}
~~~

Template condition:

~~~vue
<div v-if="chatStore.selectedModelHasConfigurableWebSearch" class="flex min-w-0 items-center gap-2">
~~~

Do not add a replacement GPT label, tooltip, banner, or explanation. Retain chat.webSearch translations for non-OpenAI models.

- [ ] **Step 4: Verify and commit**

~~~bash
pnpm --dir frontend exec vitest run src/components/chat/__tests__/chatStore.spec.ts src/components/chat/__tests__/ChatView.spec.ts
pnpm --dir frontend run typecheck
pnpm --dir frontend run lint:check
git add frontend/src/stores/chat.ts frontend/src/components/chat/Composer.vue frontend/src/components/chat/__tests__/chatStore.spec.ts frontend/src/components/chat/__tests__/ChatView.spec.ts
git commit -m "fix(webchat): hide GPT search controls"
~~~

Expected: PASS with no TypeScript or lint error.

---

### Task 6: Integrated verification and production boundary

**Files:**
- Verify: files committed in Tasks 1-5
- Refresh locally: graphify-out via graphify update; do not stage pre-existing dirty graph files

**Interfaces:**
- Consumes: all earlier task outputs.
- Produces: local backend/frontend verification evidence and an explicit production validation gate.

- [ ] **Step 1: Run backend verification**

~~~bash
cd backend
go test ./internal/pkg/apicompat ./internal/service -count=1
go test ./... -count=1
cd ..
~~~

Expected: all packages PASS.

- [ ] **Step 2: Run frontend verification**

~~~bash
make test-frontend-webchat
pnpm --dir frontend run typecheck
pnpm --dir frontend run lint:check
~~~

Expected: WebChat Vitest, Vue type checking, and ESLint exit zero.

- [ ] **Step 3: Refresh graphify and inspect scope**

~~~bash
graphify update .
git diff --check
git status --short
~~~

Expected: graphify completes without an integrity error. Leave unrelated dirty/untracked graphify-out and documentation files unstaged.

- [ ] **Step 4: Stop before a production OAuth request**

Report local results and request separate authorization for one real upstream file request. After approval, send it through the normal authenticated production WebChat conversation/message endpoint so account selection, the configured proxy, OpenAIGatewayService.Forward, usage recording, and logs are exercised together. If OAuth rejects Base64 input_file, report it as a release blocker; do not fall back to filename-only text, text_preview, local PDF extraction, OCR, or a direct unproxied provider request.
