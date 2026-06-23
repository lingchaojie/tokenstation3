package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyRepoSuiteFocused(t *testing.T) {
	t.Run("TestListByUserID_ExcludesWebChatKeys", func(t *testing.T) {
		repo, client := newAPIKeyRepoSQLite(t)
		ctx := context.Background()
		user := mustCreateAPIKeyRepoUser(t, ctx, client, "listbyuser-webchat-focused@test.com")
		group, err := client.Group.Create().
			SetName("g-list-webchat-focused").
			SetStatus(service.StatusActive).
			Save(ctx)
		require.NoError(t, err)

		visible := &service.APIKey{
			UserID: user.ID,
			Key:    "sk-list-visible-focused",
			Name:   "Visible Key",
			Status: service.StatusActive,
		}
		require.NoError(t, repo.Create(ctx, visible))

		webChat := &service.APIKey{
			UserID:           user.ID,
			Key:              "wc-list-hidden-focused",
			Name:             "Web Chat",
			KeyType:          service.APIKeyTypeWebChat,
			GroupID:          &group.ID,
			GroupBindingMode: service.APIKeyGroupBindingModeStatic,
			Status:           service.StatusActive,
		}
		require.NoError(t, repo.Create(ctx, webChat))

		keys, page, err := repo.ListByUserID(ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 10}, service.APIKeyListFilters{})
		require.NoError(t, err)
		require.Len(t, keys, 1)
		require.Equal(t, int64(1), page.Total)
		require.Equal(t, visible.ID, keys[0].ID)

		count, err := repo.CountByUserID(ctx, user.ID)
		require.NoError(t, err)
		require.Equal(t, int64(1), count)

		found, err := repo.SearchAPIKeys(ctx, user.ID, "Web", 10)
		require.NoError(t, err)
		require.Empty(t, found)

		got, err := repo.GetByKeyForAuth(ctx, webChat.Key)
		require.NoError(t, err)
		require.Equal(t, webChat.ID, got.ID)
		require.Equal(t, service.APIKeyTypeWebChat, got.KeyType)
	})

	t.Run("TestListByUserIDIncludingHidden_IncludesWebChatKeys", func(t *testing.T) {
		repo, client := newAPIKeyRepoSQLite(t)
		ctx := context.Background()
		user := mustCreateAPIKeyRepoUser(t, ctx, client, "listbyuser-webchat-internal@test.com")
		group, err := client.Group.Create().
			SetName("g-list-webchat-internal").
			SetStatus(service.StatusActive).
			Save(ctx)
		require.NoError(t, err)

		visible := &service.APIKey{
			UserID: user.ID,
			Key:    "sk-list-visible-internal",
			Name:   "Visible Key",
			Status: service.StatusActive,
		}
		require.NoError(t, repo.Create(ctx, visible))

		webChat := &service.APIKey{
			UserID:           user.ID,
			Key:              "wc-list-hidden-internal",
			Name:             "Web Chat",
			KeyType:          service.APIKeyTypeWebChat,
			GroupID:          &group.ID,
			GroupBindingMode: service.APIKeyGroupBindingModeStatic,
			Status:           service.StatusActive,
		}
		require.NoError(t, repo.Create(ctx, webChat))

		keys, page, err := repo.ListByUserIDIncludingHidden(ctx, user.ID, pagination.PaginationParams{
			Page:      1,
			PageSize:  10,
			SortBy:    "id",
			SortOrder: pagination.SortOrderAsc,
		})
		require.NoError(t, err)
		require.Len(t, keys, 2)
		require.Equal(t, int64(2), page.Total)
		require.Equal(t, visible.ID, keys[0].ID)
		require.Equal(t, webChat.ID, keys[1].ID)
		require.Equal(t, service.APIKeyTypeWebChat, keys[1].KeyType)
	})

	t.Run("TestListByGroupIDAndCountByGroupID_ExcludeWebChatKeys", func(t *testing.T) {
		repo, client := newAPIKeyRepoSQLite(t)
		ctx := context.Background()
		user := mustCreateAPIKeyRepoUser(t, ctx, client, "group-webchat-focused@test.com")
		group, err := client.Group.Create().
			SetName("g-group-webchat-focused").
			SetStatus(service.StatusActive).
			Save(ctx)
		require.NoError(t, err)

		visible := &service.APIKey{
			UserID:  user.ID,
			Key:     "sk-group-visible-focused",
			Name:    "Visible Key",
			GroupID: &group.ID,
			Status:  service.StatusActive,
		}
		require.NoError(t, repo.Create(ctx, visible))

		webChat := &service.APIKey{
			UserID:           user.ID,
			Key:              "wc-group-hidden-focused",
			Name:             "Web Chat",
			KeyType:          service.APIKeyTypeWebChat,
			GroupID:          &group.ID,
			GroupBindingMode: service.APIKeyGroupBindingModeStatic,
			Status:           service.StatusActive,
		}
		require.NoError(t, repo.Create(ctx, webChat))

		keys, page, err := repo.ListByGroupID(ctx, group.ID, pagination.PaginationParams{
			Page:      1,
			PageSize:  10,
			SortBy:    "id",
			SortOrder: pagination.SortOrderAsc,
		})
		require.NoError(t, err)
		require.Len(t, keys, 1)
		require.Equal(t, int64(1), page.Total)
		require.Equal(t, visible.ID, keys[0].ID)

		count, err := repo.CountByGroupID(ctx, group.ID)
		require.NoError(t, err)
		require.Equal(t, int64(1), count)
	})

	t.Run("TestVerifyOwnership_ExcludesWebChatKeys", func(t *testing.T) {
		repo, client := newAPIKeyRepoSQLite(t)
		ctx := context.Background()
		user := mustCreateAPIKeyRepoUser(t, ctx, client, "verify-webchat-focused@test.com")
		group, err := client.Group.Create().
			SetName("g-verify-webchat-focused").
			SetStatus(service.StatusActive).
			Save(ctx)
		require.NoError(t, err)

		visible := &service.APIKey{
			UserID: user.ID,
			Key:    "sk-verify-visible-focused",
			Name:   "Visible Key",
			Status: service.StatusActive,
		}
		require.NoError(t, repo.Create(ctx, visible))

		webChat := &service.APIKey{
			UserID:           user.ID,
			Key:              "wc-verify-hidden-focused",
			Name:             "Web Chat",
			KeyType:          service.APIKeyTypeWebChat,
			GroupID:          &group.ID,
			GroupBindingMode: service.APIKeyGroupBindingModeStatic,
			Status:           service.StatusActive,
		}
		require.NoError(t, repo.Create(ctx, webChat))

		ids, err := repo.VerifyOwnership(ctx, user.ID, []int64{visible.ID, webChat.ID})
		require.NoError(t, err)
		require.Equal(t, []int64{visible.ID}, ids)
	})

	t.Run("TestUpdateGroupIDAndKeyTypeByUserAndGroup_WebChatKeyRemainsHidden", func(t *testing.T) {
		repo, client := newAPIKeyRepoSQLite(t)
		ctx := context.Background()
		user := mustCreateAPIKeyRepoUser(t, ctx, client, "replace-webchat-focused@test.com")
		oldGroup, err := client.Group.Create().
			SetName("g-replace-webchat-old").
			SetStatus(service.StatusActive).
			Save(ctx)
		require.NoError(t, err)
		newGroup, err := client.Group.Create().
			SetName("g-replace-webchat-new").
			SetStatus(service.StatusActive).
			Save(ctx)
		require.NoError(t, err)

		visible := &service.APIKey{
			UserID:  user.ID,
			Key:     "sk-replace-visible-focused",
			Name:    "Visible Key",
			KeyType: service.APIKeyTypeAnthropic,
			GroupID: &oldGroup.ID,
			Status:  service.StatusActive,
		}
		require.NoError(t, repo.Create(ctx, visible))

		webChat := &service.APIKey{
			UserID:           user.ID,
			Key:              "wc-replace-hidden-focused",
			Name:             "Web Chat",
			KeyType:          service.APIKeyTypeWebChat,
			GroupID:          &oldGroup.ID,
			GroupBindingMode: service.APIKeyGroupBindingModeStatic,
			Status:           service.StatusActive,
		}
		require.NoError(t, repo.Create(ctx, webChat))

		affected, err := repo.UpdateGroupIDAndKeyTypeByUserAndGroup(ctx, user.ID, oldGroup.ID, newGroup.ID, service.APIKeyGroupKeyTypeUpdate{
			KeyType: service.APIKeyTypeOpenAI,
		})
		require.NoError(t, err)
		require.Equal(t, int64(1), affected)

		updatedVisible, err := repo.GetByID(ctx, visible.ID)
		require.NoError(t, err)
		require.NotNil(t, updatedVisible.GroupID)
		require.Equal(t, newGroup.ID, *updatedVisible.GroupID)
		require.Equal(t, service.APIKeyTypeOpenAI, updatedVisible.KeyType)

		hidden, err := repo.GetByID(ctx, webChat.ID)
		require.NoError(t, err)
		require.NotNil(t, hidden.GroupID)
		require.Equal(t, oldGroup.ID, *hidden.GroupID)
		require.Equal(t, service.APIKeyTypeWebChat, hidden.KeyType)
	})
}
