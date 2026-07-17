# KIRO API Region Admin Configuration Design

## Goal

Expose the existing KIRO `credentials.api_region` setting in the administrator
account workflows so the KIRO/Q runtime region is configured independently from
the IAM Identity Center login region.

## Scope

The configuration is available in all administrator workflows that create or
replace KIRO direct-account credentials:

- Create a KIRO OAuth, imported-token, or native API key account.
- Edit an existing KIRO OAuth or native API key account.
- Reauthorize an existing KIRO OAuth account.

KIRO relay API key accounts with a non-empty `base_url` do not use the native
AWS Q endpoint and therefore do not expose this setting.

This change does not alter backend routing semantics. The backend already reads
`credentials.api_region` before falling back to `credentials.region`.

## Region Semantics

The two credential keys remain independent:

- `region` is the IAM Identity Center OIDC region used for registration, token
  exchange, and token refresh. It can be a region such as `eu-north-1`.
- `api_region` is the commercial KIRO Profile and Q runtime region used to build
  `q.{region}.amazonaws.com` requests.

For commercial KIRO accounts, the administrator can select:

- `us-east-1` - US East (N. Virginia)
- `eu-central-1` - Europe (Frankfurt)

New accounts default to `us-east-1`. The UI must never derive `api_region` from
the IDC `region` value.

## Administrator UI

Add a compact `API Region` select control to the KIRO sections of:

- `CreateAccountModal.vue`
- `EditAccountModal.vue`
- `ReAuthAccountModal.vue`

The select uses the two commercial KIRO regions above. Supporting text explains
that the value must match the region of the KIRO/Q Developer Profile and can
differ from the IAM Identity Center region.

Use the existing administrator form styling and i18n structure. Add English and
Chinese labels, option labels, and hint text. The control receives a stable
`data-testid` in each workflow.

## Data Flow

### Create

Initialize local API-region state to `us-east-1`. When creating any direct KIRO
account, add the selected value to the credential payload as `api_region`.
OAuth token results must not overwrite the administrator selection.

### Edit

Load `credentials.api_region` when the modal opens. Missing values default to
`us-east-1`; the IDC `credentials.region` value is not used as a fallback. Save
the selected value back to `credentials.api_region` while preserving
`credentials.region` and all sensitive credentials through the existing merge
path.

If a historical account contains an API region outside the two commercial
choices, expose that current value as a disabled legacy option and preserve it
until the administrator explicitly selects a supported commercial region. This
prevents an unrelated edit from silently changing runtime routing.

### Reauthorization

Load the existing account's `credentials.api_region`, defaulting to
`us-east-1` when absent. Merge the selected value into the replacement OAuth
credentials after token exchange. Reauthorization must not copy the IDC
`region` into `api_region`.

## Validation And Errors

New selections are constrained by the select control, so no free-form region is
submitted. Existing legacy values are preserved but cannot be newly selected.
No new backend validation or API endpoint is required for this UI-only exposure
of the existing credential key.

## Tests

Add focused frontend regression coverage for these behaviors:

- Create initializes `api_region` to `us-east-1` and includes the selected value
  in direct KIRO credentials.
- Edit loads and submits `api_region` independently from the IDC `region`.
- Reauthorization preserves or updates `api_region` when replacing OAuth
  credentials.
- Missing values default to `us-east-1` rather than the IDC region.
- The English and Chinese i18n keys and stable test selectors are present.

Run the focused Vitest files first, then the frontend type check and the broader
KIRO-related frontend test set.
