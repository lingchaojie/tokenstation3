package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const (
	WebChatConversationStatusActive   = "active"
	WebChatConversationStatusArchived = "archived"
	WebChatConversationStatusDeleted  = "deleted"

	WebChatRoleUser      = "user"
	WebChatRoleAssistant = "assistant"
	WebChatRoleSystem    = "system"

	WebChatMessageStatusPending   = "pending"
	WebChatMessageStatusStreaming = "streaming"
	WebChatMessageStatusCompleted = "completed"
	WebChatMessageStatusFailed    = "failed"
	WebChatMessageStatusCanceled  = "canceled"

	WebChatAttachmentKindImage = "image"
	WebChatAttachmentKindFile  = "file"

	WebChatAttachmentStatusUploaded    = "uploaded"
	WebChatAttachmentStatusProcessed   = "processed"
	WebChatAttachmentStatusUnsupported = "unsupported"
	WebChatAttachmentStatusDeleted     = "deleted"

	WebChatArtifactSourceModelOutput   = "model_output"
	WebChatArtifactSourceImageOutput   = "image_output"
	WebChatArtifactSourceGeneratedFile = "generated_file"
)

type WebChatConversation struct {
	ID              int64      `json:"id"`
	UserID          int64      `json:"user_id"`
	Title           string     `json:"title"`
	DefaultModel    string     `json:"default_model"`
	DefaultProvider string     `json:"default_provider"`
	LastModel       string     `json:"last_model"`
	LastProvider    string     `json:"last_provider"`
	Status          string     `json:"status"`
	MessageCount    int        `json:"message_count"`
	LastMessageAt   *time.Time `json:"last_message_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type WebChatConversationDetail struct {
	Conversation WebChatConversation `json:"conversation"`
	Messages     []WebChatMessage    `json:"messages"`
}

type WebChatMessage struct {
	ID             int64               `json:"id"`
	ConversationID int64               `json:"conversation_id"`
	UserID         int64               `json:"user_id"`
	Role           string              `json:"role"`
	Model          string              `json:"model"`
	Provider       string              `json:"provider"`
	ContentText    string              `json:"content_text"`
	ContentJSON    []map[string]any    `json:"content_json"`
	Status         string              `json:"status"`
	ErrorCode      *string             `json:"error_code,omitempty"`
	ErrorMessage   *string             `json:"error_message,omitempty"`
	UsageLogID     *int64              `json:"usage_log_id,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	Attachments    []WebChatAttachment `json:"attachments,omitempty"`
	Artifacts      []WebChatArtifact   `json:"artifacts,omitempty"`
}

type WebChatAttachment struct {
	ID             int64     `json:"id"`
	MessageID      *int64    `json:"message_id,omitempty"`
	ConversationID *int64    `json:"conversation_id,omitempty"`
	UserID         int64     `json:"user_id"`
	Kind           string    `json:"kind"`
	Filename       string    `json:"filename"`
	ContentType    string    `json:"content_type"`
	SizeBytes      int64     `json:"size_bytes"`
	StorageKey     string    `json:"storage_key"`
	SHA256         string    `json:"sha256"`
	TextPreview    *string   `json:"text_preview,omitempty"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

type WebChatArtifact struct {
	ID             int64     `json:"id"`
	MessageID      int64     `json:"message_id"`
	ConversationID int64     `json:"conversation_id"`
	UserID         int64     `json:"user_id"`
	Filename       string    `json:"filename"`
	ContentType    string    `json:"content_type"`
	SizeBytes      int64     `json:"size_bytes"`
	StorageKey     string    `json:"storage_key"`
	SHA256         string    `json:"sha256"`
	Source         string    `json:"source"`
	CreatedAt      time.Time `json:"created_at"`
}

type WebChatDownloadMeta struct {
	Filename    string
	ContentType string
	SizeBytes   int64
}

type WebChatThinkingConfig struct {
	Enabled bool   `json:"enabled"`
	Effort  string `json:"effort,omitempty"`
}

type WebChatImageGenerationConfig struct {
	Enabled      bool   `json:"enabled"`
	Size         string `json:"size,omitempty"`
	AspectRatio  string `json:"aspect_ratio,omitempty"`
	Quality      string `json:"quality,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
	Background   string `json:"background,omitempty"`
}

type CreateWebChatConversationInput struct {
	UserID          int64  `json:"user_id"`
	Title           string `json:"title"`
	DefaultModel    string `json:"default_model"`
	DefaultProvider string `json:"default_provider"`
}

type UpdateWebChatConversationInput struct {
	Title           *string `json:"title,omitempty"`
	DefaultModel    *string `json:"default_model,omitempty"`
	DefaultProvider *string `json:"default_provider,omitempty"`
	Status          *string `json:"status,omitempty"`
}

type CreateWebChatMessageInput struct {
	ConversationID int64            `json:"conversation_id"`
	UserID         int64            `json:"user_id"`
	Role           string           `json:"role"`
	Model          string           `json:"model"`
	Provider       string           `json:"provider"`
	ContentText    string           `json:"content_text"`
	ContentJSON    []map[string]any `json:"content_json"`
	Status         string           `json:"status"`
	ErrorCode      *string          `json:"error_code,omitempty"`
	ErrorMessage   *string          `json:"error_message,omitempty"`
	UsageLogID     *int64           `json:"usage_log_id,omitempty"`
	AttachmentIDs  []int64          `json:"attachment_ids,omitempty"`
}

type UpdateWebChatMessageInput struct {
	Model                  *string           `json:"model,omitempty"`
	Provider               *string           `json:"provider,omitempty"`
	ContentText            *string           `json:"content_text,omitempty"`
	ContentJSON            *[]map[string]any `json:"content_json,omitempty"`
	Status                 *string           `json:"status,omitempty"`
	ErrorCode              *string           `json:"error_code,omitempty"`
	ErrorMessage           *string           `json:"error_message,omitempty"`
	UsageLogID             *int64            `json:"usage_log_id,omitempty"`
	ExpectedConversationID *int64            `json:"-"`
	ExpectedRole           *string           `json:"-"`
	ExpectedStatuses       []string          `json:"-"`
}

type CreateWebChatAttachmentInput struct {
	MessageID      *int64  `json:"message_id,omitempty"`
	ConversationID *int64  `json:"conversation_id,omitempty"`
	UserID         int64   `json:"user_id"`
	Kind           string  `json:"kind"`
	Filename       string  `json:"filename"`
	ContentType    string  `json:"content_type"`
	SizeBytes      int64   `json:"size_bytes"`
	StorageKey     string  `json:"storage_key"`
	SHA256         string  `json:"sha256"`
	TextPreview    *string `json:"text_preview,omitempty"`
	Status         string  `json:"status"`
}

type CreateWebChatArtifactInput struct {
	MessageID      int64  `json:"message_id"`
	ConversationID int64  `json:"conversation_id"`
	UserID         int64  `json:"user_id"`
	Filename       string `json:"filename"`
	ContentType    string `json:"content_type"`
	SizeBytes      int64  `json:"size_bytes"`
	StorageKey     string `json:"storage_key"`
	SHA256         string `json:"sha256"`
	Source         string `json:"source"`
}

type WebChatRepository interface {
	CreateConversation(ctx context.Context, in CreateWebChatConversationInput) (*WebChatConversation, error)
	ListConversations(ctx context.Context, userID int64, params pagination.PaginationParams) ([]WebChatConversation, *pagination.PaginationResult, error)
	GetConversationForUser(ctx context.Context, userID, conversationID int64) (*WebChatConversation, error)
	UpdateConversation(ctx context.Context, userID, conversationID int64, in UpdateWebChatConversationInput) (*WebChatConversation, error)
	SoftDeleteConversation(ctx context.Context, userID, conversationID int64) error
	CreateMessage(ctx context.Context, in CreateWebChatMessageInput) (*WebChatMessage, error)
	ListMessages(ctx context.Context, userID, conversationID int64) ([]WebChatMessage, error)
	UpdateMessage(ctx context.Context, userID, messageID int64, in UpdateWebChatMessageInput) (*WebChatMessage, error)
	CreateAttachment(ctx context.Context, in CreateWebChatAttachmentInput) (*WebChatAttachment, error)
	AttachUploadedFilesToMessage(ctx context.Context, userID, conversationID, messageID int64, attachmentIDs []int64) ([]WebChatAttachment, error)
	GetAttachmentForUser(ctx context.Context, userID, attachmentID int64) (*WebChatAttachment, error)
	CreateArtifact(ctx context.Context, in CreateWebChatArtifactInput) (*WebChatArtifact, error)
	GetArtifactForUser(ctx context.Context, userID, artifactID int64) (*WebChatArtifact, error)
}
