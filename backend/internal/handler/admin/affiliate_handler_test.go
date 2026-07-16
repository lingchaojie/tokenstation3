package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type affiliateAuditRepoStub struct {
	service.AffiliateRepository
	records    []service.AffiliateRebateRecord
	lastFilter service.AffiliateRecordFilter
}

func (s *affiliateAuditRepoStub) ListAffiliateRebateRecords(_ context.Context, filter service.AffiliateRecordFilter) ([]service.AffiliateRebateRecord, int64, error) {
	s.lastFilter = filter
	return s.records, int64(len(s.records)), nil
}

func TestAffiliateHandler_ListRebateRecordsPreservesUnifiedAuditFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	expiresAt := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	remaining := 8.0
	role := "inviter"
	repo := &affiliateAuditRepoStub{records: []service.AffiliateRebateRecord{{
		InviterID: 1, InviteeID: 2, RebateAmount: 10,
		RecordSource: "reward_credit", RewardRole: &role,
		ExpiresAt: &expiresAt, RemainingAmount: &remaining,
		CreatedAt: time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC),
	}}}
	handler := NewAffiliateHandler(service.NewAffiliateService(repo, nil, nil, nil, nil), nil)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/api/v1/admin/affiliates/rebates?page=2&page_size=10&search=inviter&sort_by=created_at&sort_order=asc", nil)

	handler.ListRebateRecords(ctx)

	require.Equal(t, 200, recorder.Code)
	require.Equal(t, 2, repo.lastFilter.Page)
	require.Equal(t, 10, repo.lastFilter.PageSize)
	require.Equal(t, "inviter", repo.lastFilter.Search)
	require.False(t, repo.lastFilter.SortDesc)
	var body struct {
		Data struct {
			Items []service.AffiliateRebateRecord `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Len(t, body.Data.Items, 1)
	require.Equal(t, "reward_credit", body.Data.Items[0].RecordSource)
	require.Equal(t, "inviter", *body.Data.Items[0].RewardRole)
	require.Equal(t, expiresAt, *body.Data.Items[0].ExpiresAt)
	require.InDelta(t, 8, *body.Data.Items[0].RemainingAmount, 1e-9)
}
