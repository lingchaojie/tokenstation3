package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
)

func webChatArtifactCandidatesFromOpenAIImageResults(results []openAIResponsesImageResult) []WebChatArtifactCandidate {
	if len(results) == 0 {
		return nil
	}
	candidates := make([]WebChatArtifactCandidate, 0, len(results))
	for _, result := range results {
		body, contentType, ok := decodeWebChatOpenAIImageResult(result)
		if !ok || len(body) == 0 {
			continue
		}
		ext := webChatImageArtifactExtension(result.OutputFormat, contentType)
		candidates = append(candidates, WebChatArtifactCandidate{
			Filename:    fmt.Sprintf("generated-image-%d.%s", len(candidates)+1, ext),
			ContentType: contentType,
			Body:        body,
			Source:      WebChatArtifactSourceImageOutput,
		})
	}
	return candidates
}

func decodeWebChatOpenAIImageResult(result openAIResponsesImageResult) ([]byte, string, bool) {
	if !openAIResponsesImageResultFitsDecodedLimit(result.Result, webChatMaxUploadBytes) {
		return nil, "", false
	}
	raw, dataURLContentType, ok := parseOpenAIResponsesImageResultBase64(result.Result)
	if !ok {
		return nil, "", false
	}
	contentType := openAIImageOutputMIMEType(result.OutputFormat)
	if dataURLContentType != "" {
		contentType = dataURLContentType
	}
	encoding := base64.StdEncoding
	if !strings.Contains(raw, "=") {
		encodedBytes := 0
		for i := 0; i < len(raw); i++ {
			if raw[i] != '\r' && raw[i] != '\n' {
				encodedBytes++
			}
		}
		if encodedBytes%4 != 0 {
			encoding = base64.RawStdEncoding
		}
	}
	body, err := encoding.DecodeString(raw)
	if err != nil {
		return nil, "", false
	}
	return body, contentType, true
}

func webChatImageArtifactExtension(outputFormat, contentType string) string {
	switch strings.ToLower(strings.TrimSpace(outputFormat)) {
	case "jpg", "jpeg":
		return "jpg"
	case "webp":
		return "webp"
	case "png":
		return "png"
	}
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func (s *WebChatService) saveWebChatArtifactCandidates(ctx context.Context, userID, conversationID, messageID int64, candidates []WebChatArtifactCandidate) {
	if s == nil || s.repo == nil || s.storage == nil || userID <= 0 || conversationID <= 0 || messageID <= 0 || len(candidates) == 0 {
		return
	}
	for _, candidate := range candidates {
		if len(candidate.Body) == 0 {
			continue
		}
		source := strings.TrimSpace(candidate.Source)
		if source == "" {
			source = WebChatArtifactSourceGeneratedFile
		}
		saved, err := s.storage.Save(ctx, WebChatStorageSaveInput{
			UserID:      userID,
			Filename:    candidate.Filename,
			ContentType: candidate.ContentType,
			Reader:      bytes.NewReader(candidate.Body),
			MaxBytes:    webChatMaxUploadBytes,
		})
		if err != nil {
			log.Printf("[WARN] web chat: save artifact failed: %v", err)
			continue
		}
		if _, err := s.repo.CreateArtifact(ctx, CreateWebChatArtifactInput{
			MessageID:      messageID,
			ConversationID: conversationID,
			UserID:         userID,
			Filename:       saved.Filename,
			ContentType:    saved.ContentType,
			SizeBytes:      saved.SizeBytes,
			StorageKey:     saved.StorageKey,
			SHA256:         saved.SHA256,
			Source:         source,
		}); err != nil {
			_ = s.storage.Delete(context.WithoutCancel(ctx), saved.StorageKey)
			log.Printf("[WARN] web chat: create artifact failed: %v", err)
		}
	}
}
