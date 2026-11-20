package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ScopedAdminListGroups 在数据库内过滤范围，分页和总数均只包含已授权分组。
func (r *groupRepository) ScopedAdminListGroups(ctx context.Context, params pagination.PaginationParams, all bool, platform, status, search string) ([]service.Group, int64, error) {
	p := adminaccess.FromContext(ctx)
	if p == nil {
		return nil, 0, infraerrors.Forbidden("SCOPED_GROUPS_FORBIDDEN", "缺少受限身份")
	}
	alternatives := []predicate.Group{}
	rules := p.Policy.RulesFor("groups.list.read")
	for _, rule := range rules {
		terms := []predicate.Group{group.IDGT(0)}
		if !rule.AllGroups {
			if len(rule.GroupIDs) == 0 {
				continue
			}
			terms = append(terms, group.IDIn(rule.GroupIDs...))
		}
		if len(rule.Platforms) > 0 {
			terms = append(terms, group.PlatformIn(rule.Platforms...))
		}
		alternatives = append(alternatives, group.And(terms...))
	}
	if len(alternatives) == 0 {
		return []service.Group{}, 0, nil
	}
	q := r.client.Group.Query().Where(group.DeletedAtIsNil(), group.Or(alternatives...))
	if platform != "" {
		q.Where(group.PlatformEQ(platform))
	}
	if status != "" {
		q.Where(group.StatusEQ(status))
	}
	if search != "" {
		q.Where(group.NameContainsFold(search))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	q.Select(group.FieldID, group.FieldName, group.FieldPlatform, group.FieldStatus).Order(group.ByID())
	if !all {
		q.Offset(params.Offset()).Limit(params.Limit())
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, 0, err
	}
	result := make([]service.Group, 0, len(rows))
	for _, row := range rows {
		result = append(result, service.Group{ID: row.ID, Name: row.Name, Platform: row.Platform, Status: row.Status})
	}
	return result, int64(total), nil
}
