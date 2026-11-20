package admin

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ScopedAdminKeyGroups limits scoped basic reads to the groups in the key's policy.
func (h *GroupHandler) ScopedAdminKeyGroups(c *gin.Context) {
	principal := adminaccess.RestrictedFromContext(c.Request.Context())
	if principal == nil || c.Request.Method != http.MethodGet || slices.Contains(principal.Policy.GlobalPermissions, "groups.read") {
		return
	}
	switch c.FullPath() {
	case "/api/v1/admin/groups", "/api/v1/admin/groups/all":
		c.Abort()
		h.scopedAdminListGroups(c)
	case "/api/v1/admin/groups/:id":
		c.Abort()
		if h.rejectUnsupportedSimpleModeOperation(c, simpleModeGroupGet) {
			return
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id < 1 {
			response.BadRequest(c, "分组 ID 无效")
			return
		}
		group, err := h.adminService.GetGroup(c.Request.Context(), id)
		if err != nil || group == nil || !principal.Policy.AllowsGroupConfig("groups.list.read", group.ID, group.Platform) {
			response.Error(c, 404, "分组不存在")
			return
		}
		response.Success(c, scopedAdminGroupReadDTO(group))
	}
}

func (h *GroupHandler) scopedAdminListGroups(c *gin.Context) {
	api, ok := h.adminService.(interface {
		ScopedAdminListGroups(context.Context, pagination.PaginationParams, bool, string, string, string) ([]service.Group, int64, error)
	})
	if !ok {
		response.Error(c, 503, "受限分组模型服务不可用")
		return
	}
	page, size := response.ParsePagination(c)
	all := strings.HasSuffix(c.FullPath(), "/all")
	operation := simpleModeGroupList
	if all {
		operation = simpleModeGroupGetAll
	}
	if h.rejectUnsupportedSimpleModeOperation(c, operation) {
		return
	}
	status := c.Query("status")
	if all {
		status = service.StatusActive
		if c.Query("include_inactive") == "true" {
			status = ""
		}
	}
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 100 {
		search = search[:100]
	}
	groups, total, err := api.ScopedAdminListGroups(c.Request.Context(), pagination.PaginationParams{Page: page, PageSize: size}, all, c.Query("platform"), status, search)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]gin.H, 0, len(groups))
	for i := range groups {
		out = append(out, scopedAdminGroupReadDTO(&groups[i]))
	}
	if all {
		response.Success(c, out)
	} else {
		response.Paginated(c, out, total, page, size)
	}
}

func scopedAdminGroupReadDTO(g *service.Group) gin.H {
	return gin.H{"id": g.ID, "name": g.Name, "platform": g.Platform, "status": g.Status}
}
