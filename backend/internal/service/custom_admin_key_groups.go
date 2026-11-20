package service

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

func (s *adminServiceImpl) ScopedAdminListGroups(ctx context.Context, params pagination.PaginationParams, all bool, platform, status, search string) ([]Group, int64, error) {
	repo, ok := s.groupRepo.(interface {
		ScopedAdminListGroups(context.Context, pagination.PaginationParams, bool, string, string, string) ([]Group, int64, error)
	})
	if !ok {
		return nil, 0, infraerrors.ServiceUnavailable("SCOPED_GROUPS_UNAVAILABLE", "受限分组服务不可用")
	}
	return repo.ScopedAdminListGroups(ctx, params, all, platform, status, search)
}
