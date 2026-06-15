package dto

import (
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRedeemCodeFromServiceMapsPlanSummary(t *testing.T) {
	planID := int64(7)
	quota := 123.45

	out := RedeemCodeFromService(&service.RedeemCode{
		ID:     42,
		Code:   "PLAN-CODE",
		Type:   service.RedeemTypeSubscription,
		Status: service.StatusUnused,
		PlanID: &planID,
		Plan: &dbent.SubscriptionPlan{
			ID:               planID,
			Name:             "Pro monthly",
			ProductName:      "Pro",
			ValidityDays:     30,
			ValidityUnit:     "day",
			SevenDayQuotaUsd: &quota,
			ForSale:          true,
		},
	})

	require.NotNil(t, out)
	require.Equal(t, &planID, out.PlanID)
	require.NotNil(t, out.Plan)
	require.Equal(t, planID, out.Plan.ID)
	require.Equal(t, "Pro monthly", out.Plan.Name)
	require.Equal(t, "Pro", out.Plan.ProductName)
	require.Equal(t, 30, out.Plan.ValidityDays)
	require.Equal(t, "day", out.Plan.ValidityUnit)
	require.Equal(t, &quota, out.Plan.SevenDayQuotaUSD)
	require.True(t, out.Plan.ForSale)
}
