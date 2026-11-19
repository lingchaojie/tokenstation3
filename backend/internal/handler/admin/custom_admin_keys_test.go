//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestScopedAdminKeyHandlersRejectNonJWT(t *testing.T) {
	h := &SettingHandler{}
	for _, fn := range []gin.HandlerFunc{h.ListScopedAdminKeys, h.CreateScopedAdminKey, h.UpdateScopedAdminKey, h.RotateScopedAdminKey, h.RevokeScopedAdminKey, h.DeleteScopedAdminKeyRecord} {
		for _, method := range []string{"", "admin_api_key"} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("auth_method", method)
			fn(c)
			if w.Code != 403 {
				t.Fatalf("%s 应拒绝，返回 %d", method, w.Code)
			}
		}
	}
}

type scopedKeyRevisionRepo struct {
	service.ScopedAdminKeyRepository
	item    service.ScopedAdminKey
	updated bool
}

func (r *scopedKeyRevisionRepo) GetScopedAdminKey(context.Context, string) (*service.ScopedAdminKey, error) {
	copy := r.item
	return &copy, nil
}

func (r *scopedKeyRevisionRepo) UpdateScopedAdminKey(_ context.Context, item *service.ScopedAdminKey) error {
	if !item.ExpectedUpdatedAt.Equal(r.item.UpdatedAt) {
		return service.ErrScopedAdminKeyConflict
	}
	r.updated = true
	*item = service.ScopedAdminKey{ID: item.ID, UpdatedAt: r.item.UpdatedAt.Add(time.Microsecond), ScopedAdminKeyInput: item.ScopedAdminKeyInput}
	return nil
}

func TestScopedAdminKeyUpdateHandlerRequiresLoadedRevision(t *testing.T) {
	loadedAt := time.Date(2026, 11, 19, 10, 15, 20, 123456000, time.UTC)
	for _, tc := range []struct {
		name, payload string
		wantStatus    int
		wantBody      string
	}{
		{"missing", `{"name":"scope"}`, http.StatusBadRequest, "INVALID_ADMIN_KEY_REVISION"},
		{"malformed", `{"name":"scope","updated_at":"invalid"}`, http.StatusBadRequest, "请求格式无效"},
		{"null", `{"name":"scope","updated_at":null}`, http.StatusBadRequest, "INVALID_ADMIN_KEY_REVISION"},
		{"stale", `{"name":"scope","updated_at":"2026-11-19T10:15:20.123455Z"}`, http.StatusConflict, "ADMIN_KEY_CONFLICT"},
		{"rounded", `{"name":"scope","updated_at":"2026-11-19T10:15:20.123Z"}`, http.StatusConflict, "ADMIN_KEY_CONFLICT"},
		{"fresh", `{"name":"scope","updated_at":"2026-11-19T10:15:20.123456Z"}`, http.StatusOK, `"updated_at":"2026-11-19T10:15:20.123457Z"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &scopedKeyRevisionRepo{item: service.ScopedAdminKey{ID: "key-1", UpdatedAt: loadedAt}}
			settings := service.NewSettingService(nil, nil)
			settings.SetScopedAdminKeyRepository(repo)
			h := &SettingHandler{settingService: settings}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/admin-api-keys/key-1", strings.NewReader(tc.payload))
			c.Params = gin.Params{{Key: "id", Value: "key-1"}}
			c.Set("auth_method", service.AuditAuthMethodJWT)
			c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)

			h.UpdateScopedAdminKey(c)

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), tc.wantBody)
			require.Equal(t, tc.wantStatus == http.StatusOK, repo.updated)
		})
	}
}

type deleteScopedKeyRecordRepo struct {
	service.ScopedAdminKeyRepository
	err error
	id  string
}

func (r *deleteScopedKeyRecordRepo) DeleteScopedAdminKeyRecord(_ context.Context, id string, at time.Time) error {
	r.id = id
	if at.IsZero() {
		panic("删除时间不能为空")
	}
	return r.err
}

func TestScopedAdminKeyDeleteRecordHandler(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		err        error
		status     int
	}{
		{"仅列表移除", "admin", nil, 200},
		{"未撤销或已移除", "admin", service.ErrScopedAdminKeyNotFound, 404},
		{"普通用户拒绝", "user", nil, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &deleteScopedKeyRecordRepo{err: tc.err}
			settings := service.NewSettingService(nil, nil)
			settings.SetScopedAdminKeyRepository(repo)
			h := &SettingHandler{settingService: settings}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("DELETE", "/api/v1/admin/settings/admin-api-keys/key-1/record", nil)
			c.Params = gin.Params{{Key: "id", Value: "key-1"}}
			c.Set("auth_method", service.AuditAuthMethodJWT)
			c.Set("user_role", tc.role)
			h.DeleteScopedAdminKeyRecord(c)
			require.Equal(t, tc.status, w.Code)
			if tc.role != "admin" {
				require.Empty(t, repo.id)
			} else {
				require.Equal(t, "key-1", repo.id)
			}
			if tc.status == 200 {
				require.Contains(t, w.Body.String(), "历史操作记录仍会继续保留")
			}
		})
	}
}
