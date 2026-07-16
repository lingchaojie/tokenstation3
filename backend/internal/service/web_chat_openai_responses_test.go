package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildOpenAIWebChatResponsesPayload_MixedInputsAndAutomaticSearch(t *testing.T) {
	pdf := []byte("%PDF-1.7\n%%EOF")
	image := []byte("\x89PNG\r\n\x1a\n")
	storage := &fakeWebChatStorage{
		t:            t,
		files:        map[string][]byte{"diagram.png": image, "paper.pdf": pdf},
		metaSizes:    map[string]int64{"diagram.png": int64(len(image)), "paper.pdf": int64(len(pdf))},
		expectedKeys: []string{"diagram.png", "paper.pdf"},
	}
	messages := []WebChatMessage{{
		Role:        WebChatRoleUser,
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
	require.Equal(t, "gpt-5.5", gjson.GetBytes(payload, "model").String())
	require.True(t, gjson.GetBytes(payload, "stream").Bool())
	require.Equal(t, "reasoning.encrypted_content", gjson.GetBytes(payload, "include.0").String())
	require.True(t, gjson.GetBytes(payload, "store").Exists())
	require.False(t, gjson.GetBytes(payload, "store").Bool())
	require.Equal(t, "input_text", gjson.GetBytes(payload, "input.0.content.0.type").String())
	require.Equal(t, "data:image/png;base64,iVBORw0KGgo=", gjson.GetBytes(payload, "input.0.content.1.image_url").String())
	require.Equal(t, "data:application/pdf;base64,JVBERi0xLjcKJSVFT0Y=", gjson.GetBytes(payload, "input.0.content.2.file_data").String())
	require.Equal(t, "paper.pdf", gjson.GetBytes(payload, "input.0.content.2.filename").String())
	require.Equal(t, "auto", gjson.GetBytes(payload, "input.0.content.2.detail").String())
	require.Equal(t, "web_search", gjson.GetBytes(payload, "tools.0.type").String())
	require.Equal(t, "auto", gjson.GetBytes(payload, "tool_choice").String())
	require.False(t, gjson.GetBytes(payload, "instructions").Exists())
}

func TestBuildOpenAIWebChatResponsesPayload_CodexAddsDefaultInstructions(t *testing.T) {
	payload, err := BuildOpenAIWebChatResponsesPayload(context.Background(), fakeWebChatStorageWithoutOpens(t), WebChatModelCapability{
		Provider: "openai", Platform: PlatformOpenAI, Model: "gpt-5.1-codex", SupportsText: true,
	}, []WebChatMessage{{Role: WebChatRoleUser, ContentText: "review this"}}, true)

	require.NoError(t, err)
	require.Equal(t, defaultCodexSynthInstructions("gpt-5.1-codex"), gjson.GetBytes(payload, "instructions").String())
}

func TestBuildOpenAIWebChatResponsesPayload_DOCXOmitsDetail(t *testing.T) {
	docx := testWebChatDOCX(t)
	storage := fakeWebChatStorageWithFile(t, "notes.docx", docx)
	payload, err := BuildOpenAIWebChatResponsesPayload(context.Background(), storage, WebChatModelCapability{
		Provider: "openai", Platform: PlatformOpenAI, Model: "gpt-5.5",
		SupportsFileContext: true,
	}, []WebChatMessage{{Role: WebChatRoleUser, Attachments: []WebChatAttachment{{
		Kind:        WebChatAttachmentKindFile,
		Filename:    "notes.docx",
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		StorageKey:  "notes.docx",
	}}}}, false)

	require.NoError(t, err)
	require.Equal(t, "input_file", gjson.GetBytes(payload, "input.0.content.0.type").String())
	require.False(t, gjson.GetBytes(payload, "input.0.content.0.detail").Exists())
}

func TestBuildOpenAIWebChatResponsesPayload_OpenAIOnlyFilesUseNativeInput(t *testing.T) {
	cases := []struct {
		name, filename, contentType, dataURLPrefix string
		data                                       []byte
	}{
		{
			name:          "pptx",
			filename:      "slides.pptx",
			contentType:   "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			dataURLPrefix: "data:application/vnd.openxmlformats-officedocument.presentationml.presentation;base64,",
			data: testWebChatZIP(t, map[string]string{
				"[Content_Types].xml":  "<Types/>",
				"ppt/presentation.xml": "<p:presentation/>",
			}),
		},
		{
			name:          "python",
			filename:      "script.py",
			contentType:   "text/x-python",
			dataURLPrefix: "data:text/x-python;base64,",
			data:          []byte("print('ok')\n"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage := fakeWebChatStorageWithFile(t, tc.filename, tc.data)
			payload, err := BuildOpenAIWebChatResponsesPayload(context.Background(), storage, WebChatModelCapability{
				Provider: "openai", Platform: PlatformOpenAI, Model: "gpt-5.5",
				SupportsFileContext: true,
			}, []WebChatMessage{{Role: WebChatRoleUser, Attachments: []WebChatAttachment{{
				Kind: WebChatAttachmentKindFile, Filename: tc.filename,
				ContentType: tc.contentType, StorageKey: tc.filename,
			}}}}, false)

			require.NoError(t, err)
			require.Equal(t, "input_file", gjson.GetBytes(payload, "input.0.content.0.type").String())
			require.False(t, gjson.GetBytes(payload, "input.0.content.0.detail").Exists())
			fileData := gjson.GetBytes(payload, "input.0.content.0.file_data").String()
			require.NotEmpty(t, fileData)
			require.True(t, strings.HasPrefix(fileData, tc.dataURLPrefix))
			storage.requireOpened(tc.filename)
		})
	}
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

func TestBuildOpenAIWebChatResponsesPayload_RejectsMoreThanFiftyMiBOfActualFiles(t *testing.T) {
	largePDF := make([]byte, 18<<20)
	copy(largePDF, "%PDF-1.7\n")
	storage := &fakeWebChatStorage{
		t: t,
		files: map[string][]byte{
			"first.pdf":  largePDF,
			"second.pdf": largePDF,
			"third.pdf":  largePDF,
		},
		metaSizes: map[string]int64{
			"first.pdf":  int64(len(largePDF)),
			"second.pdf": int64(len(largePDF)),
			"third.pdf":  int64(len(largePDF)),
		},
		expectedKeys: []string{"first.pdf", "second.pdf", "third.pdf"},
	}
	attachments := []WebChatAttachment{
		{Kind: WebChatAttachmentKindFile, Filename: "first.pdf", ContentType: "application/pdf", StorageKey: "first.pdf"},
		{Kind: WebChatAttachmentKindFile, Filename: "second.pdf", ContentType: "application/pdf", StorageKey: "second.pdf"},
		{Kind: WebChatAttachmentKindFile, Filename: "third.pdf", ContentType: "application/pdf", StorageKey: "third.pdf"},
	}

	payload, err := BuildOpenAIWebChatResponsesPayload(context.Background(), storage, WebChatModelCapability{
		Provider: "openai", Platform: PlatformOpenAI, Model: "gpt-5.5",
		SupportsFileContext: true,
	}, []WebChatMessage{{Role: WebChatRoleUser, Attachments: attachments}}, false)

	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Nil(t, payload)
	storage.requireOpened("first.pdf", "second.pdf", "third.pdf")
}

func TestReadWebChatStoredAttachmentAcceptsExactlyTwentyMiB(t *testing.T) {
	content := bytes.Repeat([]byte{'x'}, webChatMaxUploadBytes)
	storage := fakeWebChatStorageWithFile(t, "exact.txt", content)

	data, contentType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile, Filename: "exact.txt", ContentType: "text/plain",
		SizeBytes: int64(len(content)), StorageKey: "exact.txt",
	})

	require.NoError(t, err)
	require.Equal(t, "text/plain", contentType)
	require.Len(t, data, webChatMaxUploadBytes)
	storage.requireOpened("exact.txt")
}

func TestBuildOpenAIWebChatResponsesInputAcceptsExactlyFiftyMiBOfFiles(t *testing.T) {
	twentyMiB := bytes.Repeat([]byte{'x'}, webChatMaxUploadBytes)
	tenMiB := twentyMiB[:10<<20]
	storage := &fakeWebChatStorage{
		t: t,
		files: map[string][]byte{
			"first.txt":  twentyMiB,
			"second.txt": twentyMiB,
			"third.txt":  tenMiB,
		},
		metaSizes: map[string]int64{
			"first.txt":  int64(len(twentyMiB)),
			"second.txt": int64(len(twentyMiB)),
			"third.txt":  int64(len(tenMiB)),
		},
		expectedKeys: []string{"first.txt", "second.txt", "third.txt"},
	}
	attachments := []WebChatAttachment{
		{Kind: WebChatAttachmentKindFile, Filename: "first.txt", ContentType: "text/plain", SizeBytes: int64(len(twentyMiB)), StorageKey: "first.txt"},
		{Kind: WebChatAttachmentKindFile, Filename: "second.txt", ContentType: "text/plain", SizeBytes: int64(len(twentyMiB)), StorageKey: "second.txt"},
		{Kind: WebChatAttachmentKindFile, Filename: "third.txt", ContentType: "text/plain", SizeBytes: int64(len(tenMiB)), StorageKey: "third.txt"},
	}

	items, err := buildOpenAIWebChatResponsesInput(context.Background(), storage, []WebChatMessage{{
		Role: WebChatRoleUser, Attachments: attachments,
	}})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Len(t, gjson.ParseBytes(items[0].Content).Array(), 3)
	storage.requireOpened("first.txt", "second.txt", "third.txt")
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

func TestReadWebChatStoredAttachmentAcceptsPDFAndDOCX(t *testing.T) {
	docx := testWebChatDOCX(t)
	pdf := []byte("%PDF-1.7\n%%EOF")
	storage := &fakeWebChatStorage{
		t:            t,
		files:        map[string][]byte{"paper.pdf": pdf, "notes.docx": docx},
		metaSizes:    map[string]int64{"paper.pdf": int64(len(pdf)), "notes.docx": int64(len(docx))},
		expectedKeys: []string{"paper.pdf", "notes.docx"},
	}

	gotPDF, pdfType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile, Filename: "paper.pdf", ContentType: "application/pdf", StorageKey: "paper.pdf",
	})
	require.NoError(t, err)
	require.Equal(t, pdf, gotPDF)
	require.Equal(t, "application/pdf", pdfType)

	gotDOCX, docxType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind:        WebChatAttachmentKindFile,
		Filename:    "notes.docx",
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		StorageKey:  "notes.docx",
	})
	require.NoError(t, err)
	require.Equal(t, docx, gotDOCX)
	require.Equal(t, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", docxType)
}

func TestReadWebChatStoredAttachmentRejectsDeclaredPDFWithNonPDFBytes(t *testing.T) {
	storage := fakeWebChatStorageWithFile(t, "paper.pdf", []byte("not a pdf"))

	data, contentType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile, Filename: "paper.pdf", ContentType: "application/pdf", StorageKey: "paper.pdf",
	})

	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Nil(t, data)
	require.Empty(t, contentType)
}

func TestReadWebChatStoredAttachmentRejectsDeclaredSizeBeforeOpen(t *testing.T) {
	storage := fakeWebChatStorageWithoutOpens(t)
	data, contentType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile, Filename: "paper.pdf", ContentType: "application/pdf",
		SizeBytes: webChatMaxUploadBytes + 1, StorageKey: "paper.pdf",
	})
	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Nil(t, data)
	require.Empty(t, contentType)
	storage.requireOpened()
}

func TestReadWebChatStoredAttachmentRejectsMetadataAndActualSize(t *testing.T) {
	pdf := []byte("%PDF-1.7\n%%EOF")
	actualOversize := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte{'x'}, webChatMaxUploadBytes)...)
	cases := []struct {
		name     string
		data     []byte
		metaSize int64
	}{
		{"metadata", pdf, webChatMaxUploadBytes + 1},
		{"actual", actualOversize, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage := fakeWebChatStorageWithFileMeta(t, "paper.pdf", tc.data, tc.metaSize)
			data, contentType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
				Kind: WebChatAttachmentKindFile, Filename: "paper.pdf", ContentType: "application/pdf", StorageKey: "paper.pdf",
			})
			require.ErrorIs(t, err, ErrWebChatUploadRejected)
			require.Nil(t, data)
			require.Empty(t, contentType)
			storage.requireOpened("paper.pdf")
		})
	}
}

func TestReadWebChatStoredAttachmentWrapsOpenError(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	storage := &fakeWebChatStorage{
		t: t, expectedKeys: []string{"paper.pdf"},
		openErrors: map[string]error{"paper.pdf": storageErr},
	}
	data, contentType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile, Filename: "paper.pdf", ContentType: "application/pdf", StorageKey: "paper.pdf",
	})
	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.ErrorContains(t, err, storageErr.Error())
	require.Nil(t, data)
	require.Empty(t, contentType)
	storage.requireOpened("paper.pdf")
}

func testWebChatDOCX(t *testing.T) []byte {
	t.Helper()
	return testWebChatZIP(t, map[string]string{
		"[Content_Types].xml": "<Types/>",
		"word/document.xml":   "<w:document/>",
	})
}
