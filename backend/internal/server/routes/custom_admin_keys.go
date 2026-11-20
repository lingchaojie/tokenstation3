package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

// 管理密钥生命周期只接受管理员会话。
func registerScopedAdminKeyRoutes(settings *gin.RouterGroup, h *handler.Handlers) {
	keys := settings.Group("/admin-api-keys")
	keys.GET("", h.Admin.Setting.ListScopedAdminKeys)
	keys.POST("", h.Admin.Setting.CreateScopedAdminKey)
	keys.PUT("/:id", h.Admin.Setting.UpdateScopedAdminKey)
	keys.POST("/:id/rotate", h.Admin.Setting.RotateScopedAdminKey)
	keys.DELETE("/:id", h.Admin.Setting.RevokeScopedAdminKey)
	keys.DELETE("/:id/record", h.Admin.Setting.DeleteScopedAdminKeyRecord)
}
