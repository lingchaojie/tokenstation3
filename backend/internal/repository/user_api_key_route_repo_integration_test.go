//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/userapikeyroute"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserAPIKeyRouteRepository_ReconcileGroupReplacementMovesMatchingRoute(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := &userAPIKeyRouteRepository{client: client}
	user := mustCreateUser(t, client, &service.User{Email: "route-reconcile-move@test.com"})
	oldGroup := mustCreateGroup(t, client, &service.Group{Name: "route-reconcile-move-old", Platform: service.PlatformAnthropic})
	newGroup := mustCreateGroup(t, client, &service.Group{Name: "route-reconcile-move-new", Platform: service.PlatformOpenAI})

	_, err := repo.Upsert(ctx, service.UserAPIKeyRoute{UserID: user.ID, KeyType: service.APIKeyTypeOpenAI, GroupID: oldGroup.ID})
	require.NoError(t, err)

	err = repo.ReconcileGroupReplacement(ctx, user.ID, oldGroup.ID, newGroup.ID, service.APIKeyTypeOpenAI)

	require.NoError(t, err)
	got, err := repo.GetByUserIDAndKeyType(ctx, user.ID, service.APIKeyTypeOpenAI)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, newGroup.ID, got.GroupID)
}

func TestUserAPIKeyRouteRepository_ReconcileGroupReplacementDeletesMismatchedRoute(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := &userAPIKeyRouteRepository{client: client}
	user := mustCreateUser(t, client, &service.User{Email: "route-reconcile-delete@test.com"})
	oldGroup := mustCreateGroup(t, client, &service.Group{Name: "route-reconcile-delete-old", Platform: service.PlatformAnthropic})
	newGroup := mustCreateGroup(t, client, &service.Group{Name: "route-reconcile-delete-new", Platform: service.PlatformOpenAI})

	_, err := repo.Upsert(ctx, service.UserAPIKeyRoute{UserID: user.ID, KeyType: service.APIKeyTypeAnthropic, GroupID: oldGroup.ID})
	require.NoError(t, err)

	err = repo.ReconcileGroupReplacement(ctx, user.ID, oldGroup.ID, newGroup.ID, service.APIKeyTypeOpenAI)

	require.NoError(t, err)
	got, err := repo.GetByUserIDAndKeyType(ctx, user.ID, service.APIKeyTypeAnthropic)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestUserAPIKeyRouteRepository_ReconcileGroupReplacementUsesTxContext(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := &userAPIKeyRouteRepository{client: client}
	suffix := time.Now().UnixNano()
	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("route-reconcile-tx-%d@test.com", suffix)})
	oldGroup := mustCreateGroup(t, client, &service.Group{Name: fmt.Sprintf("route-reconcile-tx-old-%d", suffix), Platform: service.PlatformAnthropic})
	newGroup := mustCreateGroup(t, client, &service.Group{Name: fmt.Sprintf("route-reconcile-tx-new-%d", suffix), Platform: service.PlatformOpenAI})

	_, err := repo.Upsert(ctx, service.UserAPIKeyRoute{UserID: user.ID, KeyType: service.APIKeyTypeOpenAI, GroupID: oldGroup.ID})
	require.NoError(t, err)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, tx)
	require.NoError(t, repo.ReconcileGroupReplacement(txCtx, user.ID, oldGroup.ID, newGroup.ID, service.APIKeyTypeOpenAI))
	require.NoError(t, tx.Rollback())

	count, err := client.UserAPIKeyRoute.Query().
		Where(userapikeyroute.UserIDEQ(user.ID), userapikeyroute.GroupIDEQ(oldGroup.ID)).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count, "rollback should preserve the original route if repository uses the transaction client from context")
}
