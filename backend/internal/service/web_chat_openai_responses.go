package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

const webChatMaxResponsesFileBytes int64 = 50 << 20

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
						Type:     "input_file",
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
		Model:   caps.Model,
		Input:   inputJSON,
		Stream:  stream,
		Include: []string{"reasoning.encrypted_content"},
		Store:   &store,
	}
	payloadOptions := firstWebChatPayloadOptions(options)
	if effort, ok := normalizeWebChatThinkingEffort(caps, payloadOptions.Thinking); ok {
		request.Reasoning = &apicompat.ResponsesReasoning{Effort: effort, Summary: "auto"}
	}
	if tool, ok := buildWebChatImageGenerationTool(caps, payloadOptions.ImageGeneration); ok {
		request.Tools = append(request.Tools, webChatResponsesImageGenerationTool(tool))
		request.ToolChoice = json.RawMessage([]byte("{\"type\":\"image_generation\"}"))
	}
	if caps.SupportsWebSearch {
		request.Tools = appendWebChatResponsesTool(request.Tools, apicompat.ResponsesTool{Type: "web_search"})
		request.ToolChoice = json.RawMessage([]byte("\"auto\""))
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal OpenAI web chat Responses payload: %w", err)
	}
	return payload, nil
}

func webChatResponsesImageGenerationTool(tool webChatCCTool) apicompat.ResponsesTool {
	return apicompat.ResponsesTool{
		Type:         tool.Type,
		Model:        tool.Model,
		Size:         tool.Size,
		AspectRatio:  tool.AspectRatio,
		Quality:      tool.Quality,
		OutputFormat: tool.OutputFormat,
		Background:   tool.Background,
	}
}

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
		"text/plain":       "attachment.txt",
		"text/markdown":    "attachment.md",
		"application/json": "attachment.json",
		"text/csv":         "attachment.csv",
	}
	if filename := fallbacks[contentType]; filename != "" {
		return filename
	}
	return "attachment"
}
