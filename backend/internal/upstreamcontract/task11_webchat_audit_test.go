//go:build unit

package upstreamcontract

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type task11AuditCaptureRepository struct {
	mu   sync.Mutex
	logs []*service.AuditLog
}

func (r *task11AuditCaptureRepository) BatchInsert(_ context.Context, logs []*service.AuditLog) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, logs...)
	return int64(len(logs)), nil
}

func (r *task11AuditCaptureRepository) Insert(_ context.Context, log *service.AuditLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, log)
	return nil
}

func (*task11AuditCaptureRepository) List(context.Context, *service.AuditLogFilter) (*service.AuditLogList, error) {
	return &service.AuditLogList{}, nil
}

func (*task11AuditCaptureRepository) GetByID(context.Context, int64) (*service.AuditLog, error) {
	return nil, service.ErrAuditLogNotFound
}

func (*task11AuditCaptureRepository) Count(context.Context) (int64, error) { return 0, nil }
func (*task11AuditCaptureRepository) TruncateAll(context.Context) error    { return nil }
func (*task11AuditCaptureRepository) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestTask11WebChatMessageAuditOmitsPlaintextButKeepsEndpointMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := &task11AuditCaptureRepository{}
	auditService := service.NewAuditLogService(repository, nil)
	auditService.Start()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 42})
		c.Set(string(servermiddleware.ContextKeyUserRole), "user")
		c.Next()
	})
	router.Use(gin.HandlerFunc(servermiddleware.NewAuditLogMiddleware(auditService)))
	router.POST("/api/v1/chat/conversations/:id/messages", func(c *gin.Context) {
		var request map[string]any
		require.NoError(t, json.NewDecoder(c.Request.Body).Decode(&request), "audit omission must preserve the handler body")
		require.Equal(t, "plaintext-prompt-audit-canary", request["content"])
		c.JSON(http.StatusAccepted, gin.H{"ok": true})
	})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/7/messages",
		bytes.NewBufferString(`{"model":"claude-sonnet-4","content":"plaintext-prompt-audit-canary","stream":true}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	auditService.Stop()

	repository.mu.Lock()
	logs := append([]*service.AuditLog(nil), repository.logs...)
	repository.mu.Unlock()
	require.Len(t, logs, 1, "endpoint audit metadata must remain enabled")
	require.Equal(t, http.MethodPost, logs[0].Method)
	require.Equal(t, "/api/v1/chat/conversations/:id/messages", logs[0].Path)
	require.Equal(t, "chat.conversations.messages.create", logs[0].Action)
	require.Equal(t, http.StatusAccepted, logs[0].StatusCode)
	require.Equal(t, "<prompt-bearing body omitted>", logs[0].RequestBody)
	require.NotContains(t, logs[0].RequestBody, "plaintext-prompt-audit-canary")
}
