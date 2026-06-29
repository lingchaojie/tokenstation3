//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/suite"
)

type GroupMixedKiroSuite struct {
	suite.Suite
}

func TestGroupMixedKiroSuite(t *testing.T) {
	suite.Run(t, new(GroupMixedKiroSuite))
}

// TestHasSchedulableMixedKiroStickyAccount_True: kiro account with both flags true,
// schedulable + active → must return true.
func (s *GroupMixedKiroSuite) TestHasSchedulableMixedKiroStickyAccount_True() {
	ctx := context.Background()
	tx := testEntTx(s.T())
	client := tx.Client()
	repo := newGroupRepositoryWithSQL(client, tx)

	grp := mustCreateGroup(s.T(), client, &service.Group{
		Name:     "kiro-mixed-sticky-true",
		Platform: service.PlatformAnthropic,
	})
	acc := mustCreateAccount(s.T(), client, &service.Account{
		Name:        "kiro-mixed-sticky-acc",
		Platform:    service.PlatformKiro,
		Schedulable: true,
		Status:      service.StatusActive,
		Extra: map[string]any{
			"mixed_scheduling":         true,
			"kiro_auto_sticky_enabled": true,
		},
	})
	mustBindAccountToGroup(s.T(), client, acc.ID, grp.ID, 50)

	got, err := repo.HasSchedulableMixedKiroStickyAccount(ctx, grp.ID)
	s.Require().NoError(err)
	s.Require().True(got, "expected true: kiro account with both flags set")
}

// TestHasSchedulableMixedKiroStickyAccount_MissingAutoSticky: mixed_scheduling=true but
// kiro_auto_sticky_enabled absent → must return false.
func (s *GroupMixedKiroSuite) TestHasSchedulableMixedKiroStickyAccount_MissingAutoSticky() {
	ctx := context.Background()
	tx := testEntTx(s.T())
	client := tx.Client()
	repo := newGroupRepositoryWithSQL(client, tx)

	grp := mustCreateGroup(s.T(), client, &service.Group{
		Name:     "kiro-mixed-no-sticky",
		Platform: service.PlatformAnthropic,
	})
	acc := mustCreateAccount(s.T(), client, &service.Account{
		Name:        "kiro-mixed-no-sticky-acc",
		Platform:    service.PlatformKiro,
		Schedulable: true,
		Status:      service.StatusActive,
		Extra: map[string]any{
			"mixed_scheduling": true,
			// kiro_auto_sticky_enabled intentionally absent
		},
	})
	mustBindAccountToGroup(s.T(), client, acc.ID, grp.ID, 50)

	got, err := repo.HasSchedulableMixedKiroStickyAccount(ctx, grp.ID)
	s.Require().NoError(err)
	s.Require().False(got, "expected false: kiro_auto_sticky_enabled not set")
}

// TestHasSchedulableMixedKiroStickyAccount_NotSchedulable: both flags true but
// schedulable=false (set via direct ent update after creation) → must return false.
// Note: mustCreateAccount forces Schedulable=true, so we patch it after insert.
func (s *GroupMixedKiroSuite) TestHasSchedulableMixedKiroStickyAccount_NotSchedulable() {
	ctx := context.Background()
	tx := testEntTx(s.T())
	client := tx.Client()
	repo := newGroupRepositoryWithSQL(client, tx)

	grp := mustCreateGroup(s.T(), client, &service.Group{
		Name:     "kiro-mixed-unschedulable",
		Platform: service.PlatformAnthropic,
	})
	// mustCreateAccount forces Schedulable=true; patch to false after creation.
	acc := mustCreateAccount(s.T(), client, &service.Account{
		Name:     "kiro-mixed-unschedulable-acc",
		Platform: service.PlatformKiro,
		Status:   service.StatusActive,
		Extra: map[string]any{
			"mixed_scheduling":         true,
			"kiro_auto_sticky_enabled": true,
		},
	})
	_, err := client.Account.UpdateOneID(acc.ID).SetSchedulable(false).Save(ctx)
	s.Require().NoError(err, "patch schedulable=false")
	mustBindAccountToGroup(s.T(), client, acc.ID, grp.ID, 50)

	got, err := repo.HasSchedulableMixedKiroStickyAccount(ctx, grp.ID)
	s.Require().NoError(err)
	s.Require().False(got, "expected false: account not schedulable")
}

// TestHasSchedulableMixedKiroStickyAccount_AnthropicOnly: group has only an anthropic
// account (no kiro) → must return false.
func (s *GroupMixedKiroSuite) TestHasSchedulableMixedKiroStickyAccount_AnthropicOnly() {
	ctx := context.Background()
	tx := testEntTx(s.T())
	client := tx.Client()
	repo := newGroupRepositoryWithSQL(client, tx)

	grp := mustCreateGroup(s.T(), client, &service.Group{
		Name:     "anthropic-only-group",
		Platform: service.PlatformAnthropic,
	})
	acc := mustCreateAccount(s.T(), client, &service.Account{
		Name:        "anthropic-acc",
		Platform:    service.PlatformAnthropic,
		Schedulable: true,
		Status:      service.StatusActive,
	})
	mustBindAccountToGroup(s.T(), client, acc.ID, grp.ID, 50)

	got, err := repo.HasSchedulableMixedKiroStickyAccount(ctx, grp.ID)
	s.Require().NoError(err)
	s.Require().False(got, "expected false: no kiro accounts in group")
}
