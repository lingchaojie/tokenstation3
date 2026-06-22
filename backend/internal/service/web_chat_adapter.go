package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type webChatCCRequest struct {
	Model         string              `json:"model"`
	Stream        bool                `json:"stream"`
	StreamOptions *webChatStreamUsage `json:"stream_options,omitempty"`
	Messages      []webChatCCMessage  `json:"messages"`
}

type webChatStreamUsage struct {
	IncludeUsage bool `json:"include_usage"`
}

type webChatCCMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type webChatCCContentPart struct {
	Type     string                 `json:"type"`
	Text     string                 `json:"text,omitempty"`
	ImageURL *webChatCCImageURLPart `json:"image_url,omitempty"`
}

type webChatCCImageURLPart struct {
	URL string `json:"url"`
}

func BuildWebChatCompletionsPayload(ctx context.Context, storage WebChatStorage, caps WebChatModelCapability, messages []WebChatMessage, stream bool) ([]byte, error) {
	if err := validateWebChatAdapterContext(caps, messages); err != nil {
		return nil, err
	}

	request := webChatCCRequest{
		Model:    caps.Model,
		Stream:   stream,
		Messages: make([]webChatCCMessage, 0, len(messages)),
	}
	if stream {
		request.StreamOptions = &webChatStreamUsage{IncludeUsage: true}
	}

	for _, message := range messages {
		contentText := buildWebChatMessageText(message.ContentText, message.Attachments)
		imageAttachments := webChatImageAttachments(message.Attachments)
		if message.Role == WebChatRoleUser && len(imageAttachments) > 0 {
			parts := make([]webChatCCContentPart, 0, 1+len(imageAttachments))
			if contentText != "" {
				parts = append(parts, webChatCCContentPart{Type: "text", Text: contentText})
			}
			for _, attachment := range imageAttachments {
				imageURL, err := buildWebChatImageDataURL(ctx, storage, attachment)
				if err != nil {
					return nil, err
				}
				parts = append(parts, webChatCCContentPart{
					Type:     "image_url",
					ImageURL: &webChatCCImageURLPart{URL: imageURL},
				})
			}
			request.Messages = append(request.Messages, webChatCCMessage{Role: message.Role, Content: parts})
			continue
		}

		request.Messages = append(request.Messages, webChatCCMessage{Role: message.Role, Content: contentText})
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal web chat completions payload: %w", err)
	}
	return payload, nil
}

func validateWebChatAdapterContext(caps WebChatModelCapability, messages []WebChatMessage) error {
	summary := WebChatContextSummary{}
	for _, message := range messages {
		if strings.TrimSpace(message.ContentText) != "" {
			summary.TextMessageCount++
		}
		for _, attachment := range message.Attachments {
			switch attachment.Kind {
			case WebChatAttachmentKindImage:
				summary.ImageAttachmentCount++
			case WebChatAttachmentKindFile:
				if attachment.TextPreview != nil && strings.TrimSpace(*attachment.TextPreview) != "" {
					summary.FileAttachmentCount++
				}
			}
		}
	}
	return ValidateWebChatContextForModel(caps, summary)
}

func buildWebChatMessageText(contentText string, attachments []WebChatAttachment) string {
	text := contentText
	for _, attachment := range attachments {
		if attachment.Kind != WebChatAttachmentKindFile || attachment.TextPreview == nil {
			continue
		}
		preview := strings.TrimSpace(*attachment.TextPreview)
		if preview == "" {
			continue
		}
		if text != "" {
			text += "\n\n"
		}
		text += fmt.Sprintf("Attached file %s:\n%s", webChatAttachmentDisplayName(attachment), preview)
	}
	return text
}

func webChatAttachmentDisplayName(attachment WebChatAttachment) string {
	if filename := strings.TrimSpace(attachment.Filename); filename != "" {
		return sanitizeWebChatDisplayFilename(filename)
	}
	if attachment.TextPreview != nil {
		if firstLine := strings.TrimSpace(strings.SplitN(*attachment.TextPreview, "\n", 2)[0]); firstLine != "" {
			return sanitizeWebChatDisplayFilename(firstLine)
		}
	}
	return "file"
}

func webChatImageAttachments(attachments []WebChatAttachment) []WebChatAttachment {
	images := make([]WebChatAttachment, 0)
	for _, attachment := range attachments {
		if attachment.Kind == WebChatAttachmentKindImage {
			images = append(images, attachment)
		}
	}
	return images
}

func buildWebChatImageDataURL(ctx context.Context, storage WebChatStorage, attachment WebChatAttachment) (string, error) {
	if storage == nil {
		return "", fmt.Errorf("web chat image storage is required")
	}
	if attachment.SizeBytes > webChatMaxUploadBytes {
		return "", ErrWebChatUploadRejected
	}

	reader, meta, err := storage.Open(ctx, attachment.StorageKey)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	if meta.SizeBytes > webChatMaxUploadBytes {
		return "", ErrWebChatUploadRejected
	}

	data, err := io.ReadAll(io.LimitReader(reader, webChatMaxUploadBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > webChatMaxUploadBytes {
		return "", ErrWebChatUploadRejected
	}
	contentType, kind, _, err := classifyWebChatUploadContentType(attachment.ContentType)
	if err != nil || kind != WebChatAttachmentKindImage {
		return "", ErrWebChatUploadRejected
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
