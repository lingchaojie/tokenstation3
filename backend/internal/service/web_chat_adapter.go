package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

type webChatCCRequest struct {
	Model           string              `json:"model"`
	Stream          bool                `json:"stream"`
	StreamOptions   *webChatStreamUsage `json:"stream_options,omitempty"`
	Messages        []webChatCCMessage  `json:"messages"`
	ReasoningEffort string              `json:"reasoning_effort,omitempty"`
	Tools           []webChatCCTool     `json:"tools,omitempty"`
	ToolChoice      any                 `json:"tool_choice,omitempty"`
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

type webChatCCTool struct {
	Type         string `json:"type"`
	Model        string `json:"model,omitempty"`
	Size         string `json:"size,omitempty"`
	AspectRatio  string `json:"aspect_ratio,omitempty"`
	Quality      string `json:"quality,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
	Background   string `json:"background,omitempty"`
}

type WebChatCompletionsPayloadOptions struct {
	Thinking        WebChatThinkingConfig
	ImageGeneration WebChatImageGenerationConfig
	WebSearch       WebChatWebSearchConfig
}

func BuildWebChatCompletionsPayload(ctx context.Context, storage WebChatStorage, caps WebChatModelCapability, messages []WebChatMessage, stream bool, options ...WebChatCompletionsPayloadOptions) ([]byte, error) {
	if err := validateWebChatAdapterContext(caps, messages); err != nil {
		return nil, err
	}
	payloadOptions := firstWebChatPayloadOptions(options)

	request := webChatCCRequest{
		Model:    caps.Model,
		Stream:   stream,
		Messages: make([]webChatCCMessage, 0, len(messages)),
	}
	if stream {
		request.StreamOptions = &webChatStreamUsage{IncludeUsage: true}
	}
	if effort, ok := normalizeWebChatThinkingEffort(caps, payloadOptions.Thinking); ok {
		request.ReasoningEffort = effort
	}
	if tool, ok := buildWebChatImageGenerationTool(caps, payloadOptions.ImageGeneration); ok {
		request.Tools = []webChatCCTool{tool}
		if caps.Platform == PlatformOpenAI {
			request.ToolChoice = map[string]string{"type": "image_generation"}
		}
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

func BuildWebChatResponsesPayload(ctx context.Context, storage WebChatStorage, caps WebChatModelCapability, messages []WebChatMessage, stream bool, options ...WebChatCompletionsPayloadOptions) ([]byte, error) {
	chatPayload, err := BuildWebChatCompletionsPayload(ctx, storage, caps, messages, stream, options...)
	if err != nil {
		return nil, err
	}

	var chatRequest apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(chatPayload, &chatRequest); err != nil {
		return nil, fmt.Errorf("parse web chat completions payload for responses: %w", err)
	}
	responsesRequest, err := apicompat.ChatCompletionsToResponses(&chatRequest)
	if err != nil {
		return nil, fmt.Errorf("convert web chat payload to responses: %w", err)
	}
	responsesRequest.Stream = stream

	payloadOptions := firstWebChatPayloadOptions(options)
	if caps.SupportsWebSearch {
		responsesRequest.Tools = appendWebChatResponsesTool(responsesRequest.Tools, apicompat.ResponsesTool{Type: "web_search"})
		if payloadOptions.WebSearch.Enabled {
			responsesRequest.ToolChoice = json.RawMessage(`{"type":"web_search"}`)
		} else {
			responsesRequest.ToolChoice = json.RawMessage(`"auto"`)
		}
	}

	payload, err := json.Marshal(responsesRequest)
	if err != nil {
		return nil, fmt.Errorf("marshal web chat responses payload: %w", err)
	}
	return payload, nil
}

func appendWebChatResponsesTool(tools []apicompat.ResponsesTool, tool apicompat.ResponsesTool) []apicompat.ResponsesTool {
	for _, existing := range tools {
		if strings.EqualFold(existing.Type, tool.Type) {
			return tools
		}
	}
	return append(tools, tool)
}

func firstWebChatPayloadOptions(values []WebChatCompletionsPayloadOptions) WebChatCompletionsPayloadOptions {
	if len(values) == 0 {
		return WebChatCompletionsPayloadOptions{}
	}
	return values[0]
}

func normalizeWebChatThinkingEffort(caps WebChatModelCapability, config WebChatThinkingConfig) (string, bool) {
	if !config.Enabled || !caps.SupportsThinking {
		return "", false
	}

	// On/off-toggle families (e.g. GLM) advertise no effort tiers. When thinking
	// is enabled we emit "high": GLM's native scale is high/max, and the
	// downstream NormalizeGLMOpenAIReasoningEffort maps high→high accordingly.
	if len(caps.ThinkingEfforts) == 0 {
		return "high", true
	}

	effort := strings.ToLower(strings.TrimSpace(config.Effort))
	switch effort {
	case "":
		effort = "medium"
	case "max":
		effort = "xhigh"
	case "x-high", "x_high":
		effort = "xhigh"
	}

	allowed := caps.ThinkingEfforts
	for _, allowedEffort := range allowed {
		if effort == strings.ToLower(strings.TrimSpace(allowedEffort)) {
			return effort, true
		}
	}
	return "", false
}

func buildWebChatImageGenerationTool(caps WebChatModelCapability, config WebChatImageGenerationConfig) (webChatCCTool, bool) {
	if !config.Enabled || !caps.SupportsImageGeneration {
		return webChatCCTool{}, false
	}
	tool := webChatCCTool{Type: "image_generation"}
	if value := normalizeWebChatAllowedOption(config.Size, caps.ImageGenerationSizes, true); value != "" {
		tool.Size = value
	}
	if caps.Platform == PlatformGemini {
		if value := normalizeWebChatAllowedOption(config.AspectRatio, caps.ImageGenerationAspectRatios, true); value != "" {
			tool.AspectRatio = value
		}
	}
	if value := normalizeWebChatAllowedOption(config.Quality, caps.ImageGenerationQualities, true); value != "" {
		tool.Quality = value
	}
	if value := normalizeWebChatAllowedOption(config.OutputFormat, caps.ImageGenerationOutputFormats, true); value != "" {
		tool.OutputFormat = value
	}
	if value := normalizeWebChatAllowedOption(config.Background, caps.ImageGenerationBackgrounds, true); value != "" {
		tool.Background = value
	}
	return tool, true
}

func normalizeWebChatAllowedOption(value string, allowed []string, defaultFirst bool) string {
	value = strings.TrimSpace(value)
	if len(allowed) == 0 {
		return ""
	}
	for _, option := range allowed {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		if value != "" && strings.EqualFold(value, option) {
			return option
		}
	}
	if defaultFirst {
		for _, option := range allowed {
			if option = strings.TrimSpace(option); option != "" {
				return option
			}
		}
	}
	return ""
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
				summary.FileAttachmentCount++
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
	defer func() { _ = reader.Close() }()
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
	contentType, kind, _, err := classifyWebChatUploadContentType(attachment.Filename, attachment.ContentType, data)
	if err != nil || kind != WebChatAttachmentKindImage {
		return "", ErrWebChatUploadRejected
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
