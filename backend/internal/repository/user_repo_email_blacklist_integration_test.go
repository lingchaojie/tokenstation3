//go:build integration

package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func emailBlacklistProbeKeys(emails ...string) []string {
	keys := make([]string, 0, 2*len(emails))
	for _, email := range emails {
		for _, probe := range service.EmailAliasDedupProbes(email) {
			keys = append(keys, service.EmailAliasProbeKey(probe))
		}
	}
	return keys
}

func (s *UserRepoSuite) TestListActiveUsersByEmailAliasProbeKeys_MatchesInboxVariants() {
	dotted := s.mustCreateUser(&service.User{Email: "bl.target@gmail.com"})
	plusGooglemail := s.mustCreateUser(&service.User{Email: "bltarget+promo@googlemail.com"})
	upperRootDot := s.mustCreateUser(&service.User{Email: "BL.Tar.Get@Gmail.com."})
	nonGmailDotted := s.mustCreateUser(&service.User{Email: "bl.target@company.example"})
	s.mustCreateUser(&service.User{Email: "bltarget2@gmail.com"})
	s.mustCreateUser(&service.User{Email: "b.l.target@gmail.com", Status: service.StatusDisabled})
	s.mustCreateUser(&service.User{Email: "bltarget@gmail.com", Role: service.RoleAdmin})
	deleted := s.mustCreateUser(&service.User{Email: "bltar.get+x@gmail.com"})
	s.Require().NoError(s.repo.Delete(s.ctx, deleted.ID))

	users, err := s.repo.ListActiveUsersByEmailAliasProbeKeys(s.ctx,
		emailBlacklistProbeKeys("bltarget@googlemail.com", "bltarget@company.example"))
	s.Require().NoError(err)

	ids := make([]int64, 0, len(users))
	for _, user := range users {
		s.Require().Equal(service.StatusActive, user.Status)
		ids = append(ids, user.ID)
	}
	// 仓储层按去点形态过度匹配非 Gmail 域名（bl.target@company.example），由 service 层复核。
	s.Require().ElementsMatch([]int64{dotted.ID, plusGooglemail.ID, upperRootDot.ID, nonGmailDotted.ID}, ids)
}

func (s *UserRepoSuite) TestListActiveUsersByEmailAliasProbeKeys_EmptyKeys() {
	s.mustCreateUser(&service.User{Email: "bl-empty@gmail.com"})

	users, err := s.repo.ListActiveUsersByEmailAliasProbeKeys(s.ctx, nil)
	s.Require().NoError(err)
	s.Require().Empty(users)
}

func (s *UserRepoSuite) TestBatchDisableUsers_SkipsAdminDisabledAndDeleted() {
	active := s.mustCreateUser(&service.User{Email: "bl-disable-active@example.com"})
	admin := s.mustCreateUser(&service.User{Email: "bl-disable-admin@example.com", Role: service.RoleAdmin})
	already := s.mustCreateUser(&service.User{Email: "bl-disable-already@example.com", Status: service.StatusDisabled})
	deleted := s.mustCreateUser(&service.User{Email: "bl-disable-deleted@example.com"})
	s.Require().NoError(s.repo.Delete(s.ctx, deleted.ID))

	disabled, err := s.repo.BatchDisableUsers(s.ctx, []int64{active.ID, admin.ID, already.ID, deleted.ID, 99999999})
	s.Require().NoError(err)
	s.Require().Equal([]int64{active.ID}, disabled)

	got, err := s.repo.GetByID(s.ctx, active.ID)
	s.Require().NoError(err)
	s.Require().Equal(service.StatusDisabled, got.Status)

	gotAdmin, err := s.repo.GetByID(s.ctx, admin.ID)
	s.Require().NoError(err)
	s.Require().Equal(service.StatusActive, gotAdmin.Status)
}

func (s *UserRepoSuite) TestScopedAdminKeyRevokedAfterCreatorLosesEligibility() {
	for _, tc := range []struct {
		name   string
		fields service.UserUpdateFields
		role   string
		status string
	}{
		{"demotion", service.UserUpdateFields{Role: true}, service.RoleUser, service.StatusActive},
		{"disable", service.UserUpdateFields{Status: true}, service.RoleAdmin, service.StatusDisabled},
	} {
		s.Run(tc.name, func() {
			ctx := s.ctx
			creator := s.mustCreateUser(&service.User{Email: tc.name + "-scoped-admin@example.com", Role: service.RoleAdmin})
			keyRepo := NewScopedAdminKeyRepository(integrationDB)
			settings := service.NewSettingService(nil, nil)
			settings.SetScopedAdminKeyRepository(keyRepo)
			input := service.ScopedAdminKeyInput{Name: "integration key", Enabled: true}
			key, item, err := settings.CreateScopedAdminKey(ctx, input, creator.ID)
			s.Require().NoError(err)
			s.T().Cleanup(func() {
				_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scoped_admin_api_keys WHERE created_by=$1", creator.ID)
			})
			_, err = settings.AuthenticateScopedAdminKey(ctx, key, "192.0.2.1")
			s.Require().NoError(err)

			creator.Role, creator.Status = tc.role, tc.status
			s.Require().NoError(s.repo.Update(ctx, creator, tc.fields))
			_, _, err = settings.CreateScopedAdminKey(ctx, input, creator.ID)
			s.Require().ErrorIs(err, service.ErrScopedAdminKeyCreatorInactive)
			stored, err := keyRepo.GetScopedAdminKey(ctx, item.ID)
			s.Require().NoError(err)
			s.Require().NotNil(stored.RevokedAt)
			s.Require().False(stored.Enabled)

			creator.Role, creator.Status = service.RoleAdmin, service.StatusActive
			s.Require().NoError(s.repo.Update(ctx, creator, tc.fields))
			_, err = settings.AuthenticateScopedAdminKey(ctx, key, "192.0.2.1")
			s.Require().ErrorIs(err, service.ErrScopedAdminKeyInvalid)
			newKey, _, err := settings.CreateScopedAdminKey(ctx, input, creator.ID)
			s.Require().NoError(err)
			_, err = settings.AuthenticateScopedAdminKey(ctx, newKey, "192.0.2.1")
			s.Require().NoError(err)
		})
	}
}

func (s *UserRepoSuite) TestBatchDisableUsersRevokesKeysOnlyForChangedUsers() {
	ctx := s.ctx
	formerAdmin := s.mustCreateUser(&service.User{Email: "scope-batch-former@example.com", Role: service.RoleAdmin})
	admin := s.mustCreateUser(&service.User{Email: "scope-batch-admin@example.com", Role: service.RoleAdmin})
	keyRepo := NewScopedAdminKeyRepository(integrationDB)
	settings := service.NewSettingService(nil, nil)
	settings.SetScopedAdminKeyRepository(keyRepo)
	s.T().Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scoped_admin_api_keys WHERE created_by IN ($1,$2)", formerAdmin.ID, admin.ID)
	})
	input := service.ScopedAdminKeyInput{Name: "integration key", Enabled: true}
	_, formerKey, err := settings.CreateScopedAdminKey(ctx, input, formerAdmin.ID)
	s.Require().NoError(err)
	_, adminKey, err := settings.CreateScopedAdminKey(ctx, input, admin.ID)
	s.Require().NoError(err)
	// Simulate a role change from a database writer that predates the revocation hook.
	_, err = integrationDB.ExecContext(ctx, "UPDATE users SET role=$2 WHERE id=$1", formerAdmin.ID, service.RoleUser)
	s.Require().NoError(err)

	affected, err := s.repo.BatchDisableUsers(ctx, []int64{formerAdmin.ID, admin.ID})
	s.Require().NoError(err)
	s.Require().Equal([]int64{formerAdmin.ID}, affected)
	changed, err := keyRepo.GetScopedAdminKey(ctx, formerKey.ID)
	s.Require().NoError(err)
	s.Require().NotNil(changed.RevokedAt)
	unchanged, err := keyRepo.GetScopedAdminKey(ctx, adminKey.ID)
	s.Require().NoError(err)
	s.Require().Nil(unchanged.RevokedAt)
}

func (s *UserRepoSuite) TestScopedAdminIssuanceAndAuthenticationWaitForDemotion() {
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	creator := s.mustCreateUser(&service.User{Email: "scoped-concurrent-creator@example.com", Role: service.RoleAdmin})
	settings := service.NewSettingService(nil, nil)
	settings.SetScopedAdminKeyRepository(NewScopedAdminKeyRepository(integrationDB))
	s.T().Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM scoped_admin_api_keys WHERE created_by=$1", creator.ID)
	})
	input := service.ScopedAdminKeyInput{Name: "issuer race", Enabled: true}
	key, _, err := settings.CreateScopedAdminKey(ctx, input, creator.ID)
	s.Require().NoError(err)

	tx, err := integrationDB.BeginTx(ctx, nil)
	s.Require().NoError(err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, "UPDATE users SET role=$2 WHERE id=$1", creator.ID, service.RoleUser)
	s.Require().NoError(err)

	createResult := make(chan error, 1)
	authResult := make(chan error, 1)
	go func() {
		_, _, err := settings.CreateScopedAdminKey(ctx, input, creator.ID)
		createResult <- err
	}()
	go func() {
		_, err := settings.AuthenticateScopedAdminKey(ctx, key, "192.0.2.1")
		authResult <- err
	}()
	s.Require().Eventually(func() bool {
		var blocked int
		err := integrationDB.QueryRowContext(ctx, `
SELECT count(*) FROM pg_stat_activity
WHERE pid <> pg_backend_pid() AND datname=current_database() AND wait_event_type='Lock'
  AND query LIKE '%FOR SHARE OF u%' AND query NOT LIKE '%pg_stat_activity%'
`).Scan(&blocked)
		return err == nil && blocked == 2
	}, 5*time.Second, 10*time.Millisecond, "both creation and authentication wait for the issuer transition")

	s.Require().NoError(revokeScopedKeysByCreator(ctx, tx, []int64{creator.ID}, time.Now().UTC()))
	s.Require().NoError(tx.Commit())
	s.Require().ErrorIs(<-createResult, service.ErrScopedAdminKeyCreatorInactive)
	s.Require().ErrorIs(<-authResult, service.ErrScopedAdminKeyInvalid)
}
