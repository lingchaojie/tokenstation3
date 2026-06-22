package service

import (
	"bytes"
	"context"
	"io"
	"mime"
	"strings"
	"unicode/utf8"
)

const (
	webChatMaxUploadBytes      = 20 << 20
	webChatMaxTextPreviewBytes = 64 << 10
)

type webChatAttachmentCreator interface {
	CreateAttachment(ctx context.Context, in CreateWebChatAttachmentInput) (*WebChatAttachment, error)
}

type WebChatService struct {
	attachmentRepo webChatAttachmentCreator
	storage        WebChatStorage
}

func NewWebChatService(repo WebChatRepository, storage WebChatStorage) *WebChatService {
	return &WebChatService{attachmentRepo: repo, storage: storage}
}

type UploadWebChatAttachmentInput struct {
	UserID      int64
	Filename    string
	ContentType string
	Reader      io.Reader
}

func (s *WebChatService) UploadAttachment(ctx context.Context, in UploadWebChatAttachmentInput) (*WebChatAttachment, error) {
	if s == nil || s.attachmentRepo == nil || s.storage == nil || in.UserID <= 0 || in.Reader == nil {
		return nil, ErrWebChatUploadRejected
	}

	contentType, kind, textPreviewEnabled, err := classifyWebChatUploadContentType(in.ContentType)
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(io.LimitReader(in.Reader, webChatMaxUploadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > webChatMaxUploadBytes {
		return nil, ErrWebChatUploadRejected
	}

	var textPreview *string
	if textPreviewEnabled {
		preview := boundedUTF8Preview(body, webChatMaxTextPreviewBytes)
		textPreview = &preview
	}

	saved, err := s.storage.Save(ctx, WebChatStorageSaveInput{
		UserID:      in.UserID,
		Filename:    in.Filename,
		ContentType: contentType,
		Reader:      bytes.NewReader(body),
		MaxBytes:    webChatMaxUploadBytes,
	})
	if err != nil {
		return nil, err
	}

	attachment, err := s.attachmentRepo.CreateAttachment(ctx, CreateWebChatAttachmentInput{
		UserID:      in.UserID,
		Kind:        kind,
		Filename:    saved.Filename,
		ContentType: contentType,
		SizeBytes:   saved.SizeBytes,
		StorageKey:  saved.StorageKey,
		SHA256:      saved.SHA256,
		TextPreview: textPreview,
		Status:      WebChatAttachmentStatusUploaded,
	})
	if err != nil {
		_ = s.storage.Delete(context.WithoutCancel(ctx), saved.StorageKey)
		return nil, err
	}
	return attachment, nil
}

func classifyWebChatUploadContentType(raw string) (string, string, bool, error) {
	contentType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil || contentType == "" {
		return "", "", false, ErrWebChatUploadRejected
	}
	contentType = strings.ToLower(contentType)

	switch contentType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return contentType, WebChatAttachmentKindImage, false, nil
	case "text/plain", "text/markdown", "application/json", "text/csv":
		return contentType, WebChatAttachmentKindFile, true, nil
	case "application/pdf", "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return contentType, WebChatAttachmentKindFile, false, nil
	default:
		return "", "", false, ErrWebChatUploadRejected
	}
}

func boundedUTF8Preview(body []byte, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(body) > maxBytes {
		body = body[:maxBytes]
	}
	for len(body) > 0 {
		r, size := utf8.DecodeLastRune(body)
		if r != utf8.RuneError || size != 1 {
			break
		}
		body = body[:len(body)-1]
	}
	return strings.ToValidUTF8(string(body), "")
}
