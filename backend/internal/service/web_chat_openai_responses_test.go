package service

import (
	"archive/zip"
	"bytes"
	"context"
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
		Kind: WebChatAttachmentKindFile, ContentType: "application/pdf", StorageKey: "paper.pdf",
	})
	require.NoError(t, err)
	require.Equal(t, pdf, gotPDF)
	require.Equal(t, "application/pdf", pdfType)

	gotDOCX, docxType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind:        WebChatAttachmentKindFile,
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
		"word/document.xml":   "<w:document/>",
	} {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}
