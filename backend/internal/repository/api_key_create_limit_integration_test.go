//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// A count-then-insert without a shared user-row lock lets concurrent requests
// all consume the final slot. Use separate transactions and a real PostgreSQL.
func TestB8deSecurityCreateLimitConcurrentLastSlot(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewAPIKeyRepository(client, integrationDB)
	limited, ok := repo.(interface {
		CreateWithLimit(context.Context, *service.APIKey, int) error
	})
	require.True(t, ok, "production repository must enforce the count atomically")
	u, err := client.User.Create().SetEmail(fmt.Sprintf("key-cap-%d@test.com", time.Now().UnixNano())).
		SetPasswordHash("test-hash").SetStatus(service.StatusActive).SetRole(service.RoleUser).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = client.APIKey.Delete().Where(apikey.UserIDEQ(u.ID)).Exec(ctx)
		_ = client.User.DeleteOneID(u.ID).Exec(ctx)
	})
	key := func(i int) *service.APIKey {
		return &service.APIKey{UserID: u.ID, Key: fmt.Sprintf("key-cap-%d-%d", u.ID, i), Name: "cap", Status: service.StatusActive}
	}
	// Disabled keys still occupy slots; hidden WebChat keys keep their existing
	// separation from the user's visible-key count.
	first := key(0)
	first.Status = service.StatusAPIKeyDisabled
	require.NoError(t, repo.Create(ctx, first))
	hidden := key(-1)
	hidden.KeyType = service.APIKeyTypeWebChat
	require.NoError(t, repo.Create(ctx, hidden))
	const contenders = 12
	start := make(chan struct{})
	errs := make([]error, contenders)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = limited.CreateWithLimit(ctx, key(i+1), 2)
		}(i)
	}
	close(start)
	wg.Wait()
	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, service.ErrAPIKeyCountExceeded)
		}
	}
	require.Equal(t, 1, succeeded)
	count, err := repo.CountByUserID(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	require.NoError(t, repo.Delete(ctx, first.ID))
	require.NoError(t, limited.CreateWithLimit(ctx, key(100), 2), "soft deletion releases one slot")
	require.NoError(t, limited.CreateWithLimit(ctx, key(101), 0), "zero disables the cap")
}
