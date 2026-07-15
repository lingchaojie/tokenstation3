package service

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func TestLocalWebChatStorage_StoresFileUnderDataDir(t *testing.T) {
	root := t.TempDir()
	storage := NewLocalWebChatStorage(root)
	saved, err := storage.Save(context.Background(), WebChatStorageSaveInput{
		UserID:      42,
		Filename:    "hello.txt",
		ContentType: "text/plain",
		Reader:      strings.NewReader("hello world"),
		MaxBytes:    1024,
	})
	require.NoError(t, err)
	require.Equal(t, int64(11), saved.SizeBytes)
	require.Len(t, saved.SHA256, 64)
	require.NotContains(t, saved.StorageKey, "..")

	rc, meta, err := storage.Open(context.Background(), saved.StorageKey)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "hello world", string(body))
	require.Equal(t, saved.SizeBytes, meta.SizeBytes)
}

func TestLocalWebChatStorage_RejectsTooLargeFile(t *testing.T) {
	storage := NewLocalWebChatStorage(t.TempDir())
	_, err := storage.Save(context.Background(), WebChatStorageSaveInput{
		UserID:      42,
		Filename:    "large.bin",
		ContentType: "application/octet-stream",
		Reader:      strings.NewReader("123456"),
		MaxBytes:    5,
	})
	require.ErrorIs(t, err, ErrWebChatUploadRejected)
}

func TestLocalWebChatStorage_RejectsPathTraversalOpen(t *testing.T) {
	storage := NewLocalWebChatStorage(t.TempDir())
	_, _, err := storage.Open(context.Background(), "../secret.txt")
	require.ErrorIs(t, err, ErrWebChatUploadRejected)
}

func TestNewLocalWebChatStorageFromConfig_UsesPricingDataDir(t *testing.T) {
	root := t.TempDir()
	storage := NewLocalWebChatStorageFromConfig(&config.Config{
		Pricing: config.PricingConfig{DataDir: root},
	})
	require.Equal(t, filepath.Join(root, "web-chat"), storage.root)
}

func TestNewLocalWebChatStorageFromConfig_DefaultsEmptyDataDir(t *testing.T) {
	require.Equal(t, filepath.Join(".", "data", "web-chat"), NewLocalWebChatStorageFromConfig(nil).root)
	require.Equal(t, filepath.Join(".", "data", "web-chat"), NewLocalWebChatStorageFromConfig(&config.Config{}).root)
}

func TestWebChatService_UploadAttachmentStoresImageKind(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	storage := NewLocalWebChatStorage(t.TempDir())
	svc := &WebChatService{attachmentRepo: repo, storage: storage}

	attachment, err := svc.uploadAttachmentFromReader(context.Background(), UploadWebChatAttachmentInput{
		UserID:      42,
		Filename:    "photo.png",
		ContentType: "image/png",
		Reader:      strings.NewReader(string([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})),
	})
	require.NoError(t, err)
	require.NotNil(t, attachment)
	require.Equal(t, WebChatAttachmentKindImage, attachment.Kind)
	require.Equal(t, "image/png", attachment.ContentType)
	require.Nil(t, attachment.TextPreview)
	require.NotEmpty(t, attachment.StorageKey)
	require.Equal(t, attachment.ID, repo.created[0].ID)
}

func TestWebChatService_UploadAttachmentStoresTextPreview(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	storage := NewLocalWebChatStorage(t.TempDir())
	svc := &WebChatService{attachmentRepo: repo, storage: storage}

	attachment, err := svc.uploadAttachmentFromReader(context.Background(), UploadWebChatAttachmentInput{
		UserID:      42,
		Filename:    "notes.md",
		ContentType: "text/markdown; charset=utf-8",
		Reader:      strings.NewReader("hello 世界"),
	})
	require.NoError(t, err)
	require.NotNil(t, attachment.TextPreview)
	require.Equal(t, "hello 世界", *attachment.TextPreview)
	require.Equal(t, WebChatAttachmentKindFile, attachment.Kind)
	require.Equal(t, "text/markdown", attachment.ContentType)
}

func TestWebChatService_UploadAttachmentRejectsUnknownArchive(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	storage := NewLocalWebChatStorage(t.TempDir())
	svc := &WebChatService{attachmentRepo: repo, storage: storage}

	_, err := svc.uploadAttachmentFromReader(context.Background(), UploadWebChatAttachmentInput{
		UserID:      42,
		Filename:    "archive.zip",
		ContentType: "application/zip",
		Reader:      strings.NewReader("PK\x03\x04"),
	})
	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Empty(t, repo.created)
}

func TestWebChatService_UploadAttachmentRejectsFilenameThatLosesExtensionWhenSanitized(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	root := t.TempDir()
	storage := NewLocalWebChatStorage(root)
	svc := &WebChatService{attachmentRepo: repo, storage: storage}

	_, err := svc.uploadAttachmentFromReader(context.Background(), UploadWebChatAttachmentInput{
		UserID:      42,
		Filename:    ".pdf",
		ContentType: "application/pdf",
		Reader:      strings.NewReader("%PDF-1.7\n%%EOF"),
	})

	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Empty(t, repo.created)
	require.Equal(t, 0, countRegularFiles(t, root))
}

func TestWebChatService_UploadAttachmentRejectsTooLargeFile(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	storage := NewLocalWebChatStorage(t.TempDir())
	svc := &WebChatService{attachmentRepo: repo, storage: storage}

	_, err := svc.uploadAttachmentFromReader(context.Background(), UploadWebChatAttachmentInput{
		UserID:      42,
		Filename:    "large.txt",
		ContentType: "text/plain",
		Reader:      strings.NewReader(strings.Repeat("x", webChatMaxUploadBytes+1)),
	})
	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Empty(t, repo.created)
}

func TestWebChatService_UploadAttachmentCleansUpStoredFileWhenRepoFails(t *testing.T) {
	repo := &webChatUploadRepoStub{err: errors.New("db down")}
	root := t.TempDir()
	storage := NewLocalWebChatStorage(root)
	svc := &WebChatService{attachmentRepo: repo, storage: storage}

	_, err := svc.uploadAttachmentFromReader(context.Background(), UploadWebChatAttachmentInput{
		UserID:      42,
		Filename:    "orphan.txt",
		ContentType: "text/plain",
		Reader:      strings.NewReader("orphan"),
	})
	require.Error(t, err)
	require.Equal(t, 0, countRegularFiles(t, root))
}

func TestWebChatService_UploadAttachmentCleansUpStoredFileWhenRepoCancelsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repo := &webChatUploadRepoStub{
		err:             context.Canceled,
		cancelBeforeErr: cancel,
	}
	root := t.TempDir()
	storage := NewLocalWebChatStorage(root)
	svc := &WebChatService{attachmentRepo: repo, storage: storage}

	_, err := svc.uploadAttachmentFromReader(ctx, UploadWebChatAttachmentInput{
		UserID:      42,
		Filename:    "orphan.txt",
		ContentType: "text/plain",
		Reader:      strings.NewReader("orphan"),
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0, countRegularFiles(t, root))
}

func TestWebChatService_UploadAttachmentSanitizesDisplayFilename(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	storage := NewLocalWebChatStorage(t.TempDir())
	svc := &WebChatService{attachmentRepo: repo, storage: storage}
	longName := "..\\bad\n" + strings.Repeat("a", 300) + ".txt"

	attachment, err := svc.uploadAttachmentFromReader(context.Background(), UploadWebChatAttachmentInput{
		UserID:      42,
		Filename:    longName,
		ContentType: "text/plain",
		Reader:      strings.NewReader("hello"),
	})
	require.NoError(t, err)
	require.NotContains(t, attachment.Filename, "..")
	require.NotContains(t, attachment.Filename, "\\")
	require.NotContains(t, attachment.Filename, "\n")
	require.LessOrEqual(t, len(attachment.Filename), 255)
	require.NotEmpty(t, attachment.Filename)
}

func TestWebChatService_UploadAttachmentNormalizesMIMECase(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	storage := NewLocalWebChatStorage(t.TempDir())
	svc := &WebChatService{attachmentRepo: repo, storage: storage}

	attachment, err := svc.uploadAttachmentFromReader(context.Background(), UploadWebChatAttachmentInput{
		UserID:      42,
		Filename:    "photo.png",
		ContentType: "IMAGE/PNG; charset=binary",
		Reader:      strings.NewReader(string([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})),
	})
	require.NoError(t, err)
	require.Equal(t, "image/png", attachment.ContentType)
	require.Equal(t, WebChatAttachmentKindImage, attachment.Kind)
}

func TestWebChatService_UploadAttachmentSniffsMissingContentTypeText(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	storage := NewLocalWebChatStorage(t.TempDir())
	svc := &WebChatService{attachmentRepo: repo, storage: storage}
	file := webChatMultipartFile{Reader: strings.NewReader("plain notes")}
	header := &multipart.FileHeader{Filename: "notes.txt"}

	attachment, err := svc.UploadAttachment(context.Background(), 42, file, header)

	require.NoError(t, err)
	require.Equal(t, "text/plain", attachment.ContentType)
	require.Equal(t, WebChatAttachmentKindFile, attachment.Kind)
	require.NotNil(t, attachment.TextPreview)
	require.Equal(t, "plain notes", *attachment.TextPreview)
}

func TestWebChatService_UploadAttachmentSniffsMissingContentTypeImage(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	storage := NewLocalWebChatStorage(t.TempDir())
	svc := &WebChatService{attachmentRepo: repo, storage: storage}
	png := string([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	file := webChatMultipartFile{Reader: strings.NewReader(png)}
	header := &multipart.FileHeader{Filename: "photo.png"}

	attachment, err := svc.UploadAttachment(context.Background(), 42, file, header)

	require.NoError(t, err)
	require.Equal(t, "image/png", attachment.ContentType)
	require.Equal(t, WebChatAttachmentKindImage, attachment.Kind)
}

func TestWebChatService_UploadAttachmentUsesExtensionForGenericCodeMIME(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	storage := NewLocalWebChatStorage(t.TempDir())
	svc := &WebChatService{attachmentRepo: repo, storage: storage}
	attachment, err := svc.uploadAttachmentFromReader(context.Background(), UploadWebChatAttachmentInput{
		UserID: 42, Filename: "script.py", ContentType: "application/octet-stream",
		Reader: strings.NewReader("print('ok')\n"),
	})
	require.NoError(t, err)
	require.Equal(t, "text/x-python", attachment.ContentType)
	require.NotNil(t, attachment.TextPreview)
	require.Len(t, repo.created, 1)
	require.Equal(t, attachment.ID, repo.created[0].ID)
}

func TestWebChatService_UploadAttachmentRejectsMissingContentTypeBinary(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	storage := NewLocalWebChatStorage(t.TempDir())
	svc := &WebChatService{attachmentRepo: repo, storage: storage}
	file := webChatMultipartFile{Reader: strings.NewReader(string([]byte{0x00, 0x01, 0x02, 0x03, 0x04}))}
	header := &multipart.FileHeader{Filename: "payload.bin"}

	_, err := svc.UploadAttachment(context.Background(), 42, file, header)

	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Empty(t, repo.created)
}

type webChatMultipartFile struct {
	*strings.Reader
}

func (webChatMultipartFile) Close() error {
	return nil
}

type webChatUploadRepoStub struct {
	mu              sync.Mutex
	created         []WebChatAttachment
	err             error
	cancelBeforeErr context.CancelFunc
}

func (s *webChatUploadRepoStub) CreateAttachment(ctx context.Context, in CreateWebChatAttachmentInput) (*WebChatAttachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		if s.cancelBeforeErr != nil {
			s.cancelBeforeErr()
		}
		return nil, s.err
	}
	attachment := WebChatAttachment{
		ID:             int64(len(s.created) + 1),
		UserID:         in.UserID,
		Kind:           in.Kind,
		Filename:       in.Filename,
		ContentType:    in.ContentType,
		SizeBytes:      in.SizeBytes,
		StorageKey:     in.StorageKey,
		SHA256:         in.SHA256,
		TextPreview:    in.TextPreview,
		Status:         in.Status,
		MessageID:      in.MessageID,
		ConversationID: in.ConversationID,
	}
	s.created = append(s.created, attachment)
	return &attachment, nil
}

func (s *webChatUploadRepoStub) CreateConversation(ctx context.Context, in CreateWebChatConversationInput) (*WebChatConversation, error) {
	return nil, errors.New("not implemented")
}

func (s *webChatUploadRepoStub) ListConversations(ctx context.Context, userID int64, params pagination.PaginationParams) ([]WebChatConversation, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *webChatUploadRepoStub) GetConversationForUser(ctx context.Context, userID, conversationID int64) (*WebChatConversation, error) {
	return nil, errors.New("not implemented")
}

func (s *webChatUploadRepoStub) UpdateConversation(ctx context.Context, userID, conversationID int64, in UpdateWebChatConversationInput) (*WebChatConversation, error) {
	return nil, errors.New("not implemented")
}

func (s *webChatUploadRepoStub) SoftDeleteConversation(ctx context.Context, userID, conversationID int64) error {
	return errors.New("not implemented")
}

func (s *webChatUploadRepoStub) CreateMessage(ctx context.Context, in CreateWebChatMessageInput) (*WebChatMessage, error) {
	return nil, errors.New("not implemented")
}

func (s *webChatUploadRepoStub) ListMessages(ctx context.Context, userID, conversationID int64) ([]WebChatMessage, error) {
	return nil, errors.New("not implemented")
}

func (s *webChatUploadRepoStub) UpdateMessage(ctx context.Context, userID, messageID int64, in UpdateWebChatMessageInput) (*WebChatMessage, error) {
	return nil, errors.New("not implemented")
}

func (s *webChatUploadRepoStub) AttachUploadedFilesToMessage(ctx context.Context, userID, conversationID, messageID int64, attachmentIDs []int64) ([]WebChatAttachment, error) {
	return nil, errors.New("not implemented")
}

func (s *webChatUploadRepoStub) GetAttachmentForUser(ctx context.Context, userID, attachmentID int64) (*WebChatAttachment, error) {
	return nil, errors.New("not implemented")
}

func (s *webChatUploadRepoStub) CreateArtifact(ctx context.Context, in CreateWebChatArtifactInput) (*WebChatArtifact, error) {
	return nil, errors.New("not implemented")
}

func (s *webChatUploadRepoStub) GetArtifactForUser(ctx context.Context, userID, artifactID int64) (*WebChatArtifact, error) {
	return nil, errors.New("not implemented")
}

var _ WebChatRepository = (*webChatUploadRepoStub)(nil)

func countRegularFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			count++
		}
		return nil
	})
	require.NoError(t, err)
	return count
}
