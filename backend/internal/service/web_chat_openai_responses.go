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
