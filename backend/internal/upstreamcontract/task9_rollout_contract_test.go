package upstreamcontract

import (
	"strings"
	"testing"
)

func TestTask9AutomaticUpstreamBillingRateWritebackIsAbsent(t *testing.T) {
	for path, forbidden := range map[string][]string{
		"backend/internal/service/upstream_billing_probe.go": {
			"UpstreamBillingRateSyncEnabledExtraKey",
			"SyncedRateMultiplier",
			"upstreamBillingProbeSyncRate",
			"upstreamBillingRateSyncEnabled",
		},
		"backend/internal/service/admin_account.go": {
			"RateSyncEnabled",
			"ErrUpstreamBillingRateSync",
		},
		"backend/internal/repository/account_repo.go": {
			"upstream_billing_rate_sync_enabled",
			"rateMultiplier *float64",
		},
		"backend/internal/handler/admin/account_handler.go": {
			`json:"upstream_billing_rate_sync_enabled"`,
		},
		"frontend/src/types/index.ts": {
			"upstream_billing_rate_sync_enabled",
		},
		"frontend/src/components/account/EditAccountModal.vue": {
			"upstreamBillingRateSyncEnabled",
			"upstream-billing-rate-sync",
		},
		"frontend/src/views/admin/AccountsView.vue": {
			"upstream_billing_rate_sync_enabled",
			"account-rate-sync-indicator",
		},
	} {
		body := readRepoFile(t, path)
		for _, token := range forbidden {
			if strings.Contains(body, token) {
				t.Errorf("automatic upstream rate writeback surface %q remains in %s", token, path)
			}
		}
	}
}

func TestTask9ReadOnlyUpstreamBillingProbeRemains(t *testing.T) {
	for path, required := range map[string][]string{
		"backend/internal/service/upstream_billing_probe.go": {
			"UpstreamBillingProbeSnapshot",
			"ResolvedRateMultiplier",
			"EffectiveRateMultiplier",
			"ProbeAccount",
		},
		"backend/internal/handler/admin/account_upstream_billing_probe.go": {
			"ProbeAccount",
		},
		"frontend/src/components/account/UpstreamBillingRateCell.vue": {
			"resolved_rate_multiplier",
		},
	} {
		body := readRepoFile(t, path)
		for _, token := range required {
			if !strings.Contains(body, token) {
				t.Errorf("read-only upstream billing probe surface %q missing from %s", token, path)
			}
		}
	}
}

func TestTask9ReadOnlyProbeCopyDoesNotPromiseRateWriteback(t *testing.T) {
	for path, forbidden := range map[string][]string{
		"frontend/src/i18n/locales/en/admin/settings.ts": {
			"Account rates change only when the separate sync switch is enabled.",
		},
		"frontend/src/i18n/locales/zh/admin/settings.ts": {
			"只有另行开启“同步上游声明倍率”的账号才会更新账号倍率。",
		},
		"frontend/src/i18n/locales/en/admin/overview.ts": {
			"Account multipliers may be maintained manually or synchronized from probes",
		},
		"frontend/src/i18n/locales/zh/admin/overview.ts": {
			"账号倍率可手工维护或由探测同步",
		},
	} {
		body := readRepoFile(t, path)
		for _, token := range forbidden {
			if strings.Contains(body, token) {
				t.Errorf("read-only upstream billing copy still promises rate writeback %q in %s", token, path)
			}
		}
	}

	for path, required := range map[string][]string{
		"frontend/src/i18n/locales/en/admin/settings.ts": {
			"declared, resolved, and effective rates",
			"read-only and never changes the account rate multiplier",
		},
		"frontend/src/i18n/locales/zh/admin/settings.ts": {
			"声明、解析及生效倍率",
			"只读操作，绝不修改账号倍率",
		},
		"frontend/src/i18n/locales/en/admin/overview.ts": {
			"read-only upstream probes",
			"never change account multipliers",
		},
		"frontend/src/i18n/locales/zh/admin/overview.ts": {
			"只读上游探测",
			"绝不修改账号倍率",
		},
	} {
		body := readRepoFile(t, path)
		for _, token := range required {
			if !strings.Contains(body, token) {
				t.Errorf("read-only upstream billing contract %q missing from %s", token, path)
			}
		}
	}
}
