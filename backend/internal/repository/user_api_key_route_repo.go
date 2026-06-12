package repository

import (
	"context"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/userapikeyroute"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type userAPIKeyRouteRepository struct {
	client *dbent.Client
}

func NewUserAPIKeyRouteRepository(client *dbent.Client) service.UserAPIKeyRouteRepository {
	return &userAPIKeyRouteRepository{client: client}
}

func (r *userAPIKeyRouteRepository) GetByUserID(ctx context.Context, userID int64) ([]service.UserAPIKeyRoute, error) {
	rows, err := r.client.UserAPIKeyRoute.Query().
		Where(userapikeyroute.UserIDEQ(userID)).
		WithGroup().
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.UserAPIKeyRoute, 0, len(rows))
	for _, row := range rows {
		out = append(out, *userAPIKeyRouteEntityToService(row))
	}
	return out, nil
}

func (r *userAPIKeyRouteRepository) GetByUserIDAndKeyType(ctx context.Context, userID int64, keyType string) (*service.UserAPIKeyRoute, error) {
	row, err := r.client.UserAPIKeyRoute.Query().
		Where(userapikeyroute.UserIDEQ(userID), userapikeyroute.KeyTypeEQ(keyType)).
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return userAPIKeyRouteEntityToService(row), nil
}

func (r *userAPIKeyRouteRepository) Upsert(ctx context.Context, route service.UserAPIKeyRoute) (*service.UserAPIKeyRoute, error) {
	rowID, err := r.client.UserAPIKeyRoute.Create().
		SetUserID(route.UserID).
		SetKeyType(route.KeyType).
		SetGroupID(route.GroupID).
		OnConflictColumns(userapikeyroute.FieldUserID, userapikeyroute.FieldKeyType).
		UpdateNewValues().
		ID(ctx)
	if err != nil {
		return nil, err
	}
	created, err := r.client.UserAPIKeyRoute.Query().Where(userapikeyroute.IDEQ(rowID)).WithGroup().Only(ctx)
	if err != nil {
		return nil, err
	}
	return userAPIKeyRouteEntityToService(created), nil
}

func (r *userAPIKeyRouteRepository) DeleteByUserIDAndKeyType(ctx context.Context, userID int64, keyType string) error {
	_, err := r.client.UserAPIKeyRoute.Delete().
		Where(userapikeyroute.UserIDEQ(userID), userapikeyroute.KeyTypeEQ(keyType)).
		Exec(ctx)
	return err
}

func userAPIKeyRouteEntityToService(row *dbent.UserAPIKeyRoute) *service.UserAPIKeyRoute {
	if row == nil {
		return nil
	}
	out := &service.UserAPIKeyRoute{
		ID:        row.ID,
		UserID:    row.UserID,
		KeyType:   row.KeyType,
		GroupID:   row.GroupID,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if row.Edges.Group != nil {
		out.Group = groupEntityToService(row.Edges.Group)
	}
	return out
}
