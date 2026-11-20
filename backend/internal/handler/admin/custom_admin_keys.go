package admin

import (
	"encoding/json"
	"io"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func requireAdminJWT(c *gin.Context) bool {
	if c.GetString("auth_method") != service.AuditAuthMethodJWT {
		response.Error(c, 403, "此操作需要管理员 JWT 会话")
		return false
	}
	role, ok := middleware.GetUserRoleFromContext(c)
	if !ok || role != "admin" {
		response.Error(c, 403, "需要管理员权限")
		return false
	}
	c.Header("Cache-Control", "no-store")
	return true
}
func bindScopedAdminKeyInput(c *gin.Context, in any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(in); err == nil && decoder.Decode(new(any)) == io.EOF {
		return true
	}
	response.BadRequest(c, "请求格式无效")
	return false
}

func (h *SettingHandler) ListScopedAdminKeys(c *gin.Context) {
	if !requireAdminJWT(c) {
		return
	}
	items, e := h.settingService.ListScopedAdminKeys(c.Request.Context())
	if e != nil {
		response.ErrorFrom(c, e)
		return
	}
	response.Success(c, gin.H{"items": items, "permissions": adminaccess.PermissionCatalog()})
}
func (h *SettingHandler) CreateScopedAdminKey(c *gin.Context) {
	if !requireAdminJWT(c) {
		return
	}
	var in service.ScopedAdminKeyInput
	if !bindScopedAdminKeyInput(c, &in) {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Error(c, 403, "需要管理员身份")
		return
	}
	key, item, e := h.settingService.CreateScopedAdminKey(c.Request.Context(), in, subject.UserID)
	if e != nil {
		response.ErrorFrom(c, e)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"admin_key_id": item.ID})
	response.Success(c, gin.H{"key": key, "item": item})
}
func (h *SettingHandler) UpdateScopedAdminKey(c *gin.Context) {
	if !requireAdminJWT(c) {
		return
	}
	var in service.ScopedAdminKeyUpdateInput
	if !bindScopedAdminKeyInput(c, &in) {
		return
	}
	item, e := h.settingService.UpdateScopedAdminKey(c.Request.Context(), c.Param("id"), in)
	if e != nil {
		response.ErrorFrom(c, e)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"admin_key_id": item.ID})
	response.Success(c, item)
}
func (h *SettingHandler) RotateScopedAdminKey(c *gin.Context) {
	if !requireAdminJWT(c) {
		return
	}
	key, item, e := h.settingService.RotateScopedAdminKey(c.Request.Context(), c.Param("id"))
	if e != nil {
		response.ErrorFrom(c, e)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"admin_key_id": item.ID})
	response.Success(c, gin.H{"key": key, "item": item})
}
func (h *SettingHandler) RevokeScopedAdminKey(c *gin.Context) {
	if !requireAdminJWT(c) {
		return
	}
	if e := h.settingService.RevokeScopedAdminKey(c.Request.Context(), c.Param("id")); e != nil {
		response.ErrorFrom(c, e)
		return
	}
	response.Success(c, gin.H{"message": "管理密钥已永久撤销"})
}

func (h *SettingHandler) DeleteScopedAdminKeyRecord(c *gin.Context) {
	if !requireAdminJWT(c) {
		return
	}
	if e := h.settingService.DeleteScopedAdminKeyRecord(c.Request.Context(), c.Param("id")); e != nil {
		response.ErrorFrom(c, e)
		return
	}
	response.Success(c, gin.H{"message": "密钥已从列表移除，历史操作记录仍会继续保留"})
}
