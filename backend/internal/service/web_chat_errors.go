package service

import infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

var (
	ErrWebChatConversationNotFound = infraerrors.NotFound("WEB_CHAT_CONVERSATION_NOT_FOUND", "conversation not found")
	ErrWebChatMessageNotFound      = infraerrors.NotFound("WEB_CHAT_MESSAGE_NOT_FOUND", "message not found")
	ErrWebChatAttachmentNotFound   = infraerrors.NotFound("WEB_CHAT_ATTACHMENT_NOT_FOUND", "attachment not found")
	ErrWebChatArtifactNotFound     = infraerrors.NotFound("WEB_CHAT_ARTIFACT_NOT_FOUND", "artifact not found")
	ErrWebChatInvalidModel         = infraerrors.BadRequest("WEB_CHAT_INVALID_MODEL", "model is not available for web chat")
	ErrWebChatUnsupportedContext   = infraerrors.BadRequest("WEB_CHAT_UNSUPPORTED_CONTEXT", "selected model does not support the current conversation context")
	ErrWebChatContextRequired      = infraerrors.BadRequest("WEB_CHAT_CONTEXT_REQUIRED", "web chat request context is required")
	ErrWebChatUploadRejected       = infraerrors.BadRequest("WEB_CHAT_UPLOAD_REJECTED", "file upload rejected")
)
