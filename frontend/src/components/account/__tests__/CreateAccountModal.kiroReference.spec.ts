import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const currentDir = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(resolve(currentDir, "../CreateAccountModal.vue"), "utf8");
const editSource = readFileSync(resolve(currentDir, "../EditAccountModal.vue"), "utf8");
const reauthSource = readFileSync(resolve(currentDir, "../../admin/account/ReAuthAccountModal.vue"), "utf8");
const zhSource = readFileSync(resolve(currentDir, "../../../i18n/locales/zh/admin/accounts.ts"), "utf8");
const enSource = readFileSync(resolve(currentDir, "../../../i18n/locales/en/admin/accounts.ts"), "utf8");

describe("CreateAccountModal Kiro reference account modes", () => {
  it("exposes OAuth, direct API key, relay API key, and IDC organization sign-in controls", () => {
    expect(source).toContain("accountCategory = 'apikey'");
    expect(source).toContain("accountCategory = 'apikey-relay'");
    expect(source).toContain("accountCategory === 'apikey-relay'");
    expect(source).toContain("kiroAccountType = 'idc'");
    expect(source).toContain("kiroIDCStartUrl");
    expect(source).toContain("kiroIDCRegion");
    expect(source).toContain("generateIDCAuthUrl");
    expect(source).toContain("const startUrl = kiroIDCStartUrl.value.trim()");
    expect(source).toContain("startUrl,");
    expect(source).toContain("region: kiroIDCRegion.value.trim() || 'us-east-1'");
    expect(source).toContain("kiroOAuthProvider.value === 'github' ? 'Github' : 'Google'");
    expect(source).toContain("kiro_credit_unit_price_usd");
    expect(source).toContain("fetchKiroDefaultMappings");
    expect(source).toContain("kiroModelMappings");
    expect(source).toContain("KIRO_RELAY_DEFAULT_PRIORITY");
    expect(source).toContain("relayPriorityHint");
  });

  it("does not expose the old CodeWhisperer/Amazon Q Kiro OAuth endpoint controls", () => {
    expect(source).not.toContain("kiroPreferredEndpoint");
    expect(source).not.toContain("preferred_endpoint");
    expect(source).not.toContain("CodeWhisperer");
    expect(source).not.toContain("Amazon Q");
    expect(source).not.toContain("kiroBaseUrl");
  });

  it("keeps Kiro edit and reauth aligned with reference OAuth metadata", () => {
    expect(editSource).toContain("isKiroOrganizationAccount");
    expect(editSource).toContain("editKiroIDCStartUrl");
    expect(editSource).toContain("editKiroIDCRegion");
    expect(editSource).toContain("delete newCredentials.preferred_endpoint");
    expect(editSource).toContain("kiro_credit_unit_price_usd");
    expect(editSource).toContain("isKiroRelayAccount");
    expect(editSource).toContain("applyKiroModelMappings");
    expect(editSource).toContain("loadDefaultKiroModelMappings");
    expect(editSource).not.toContain("editKiroPreferredEndpoint");
    expect(editSource).not.toContain("CodeWhisperer");
    expect(reauthSource).toContain("kiroOAuthProvider");
    expect(reauthSource).toContain("callbackPath: oauthFlowRef.value?.oauthCallbackPath");
    expect(reauthSource).toContain("loginOption: oauthFlowRef.value?.oauthLoginOption");
  });

  it("uses Kiro-specific mixed scheduling copy", () => {
    expect(source).toContain("admin.accounts.kiroMixedScheduling");
    expect(source).toContain("admin.accounts.kiroMixedSchedulingTooltip");
    expect(editSource).toContain("admin.accounts.kiroMixedScheduling");
    expect(editSource).toContain("admin.accounts.kiroMixedSchedulingTooltip");
    expect(zhSource).toContain("kiroMixedScheduling: '加入 Anthropic /v1/messages 调度'");
    expect(zhSource).toContain("该 Kiro 账号可参与 Anthropic 分组的 /v1/messages 混合调度");
    expect(zhSource).not.toContain("该 Kiro 账号可参与 Anthropic/Gemini");
    expect(enSource).toContain("kiroMixedScheduling: 'Use in Anthropic /v1/messages'");
  });

  it("configures the Kiro API region for every direct account create flow", () => {
    const buildKiroCredentialsSource = source.slice(
      source.indexOf("const buildKiroCredentials"),
      source.indexOf("const handleSubmit"),
    );
    const resetFormSource = source.slice(
      source.indexOf("const resetForm"),
      source.indexOf("const handleClose"),
    );
    const nativeKiroAPIKeySource = source.slice(
      source.indexOf("if (form.platform === 'kiro' && accountCategory.value === 'apikey')"),
      source.indexOf("if (form.platform === 'kiro' && accountCategory.value === 'apikey-relay')"),
    );
    const relayKiroAPIKeySource = source.slice(
      source.indexOf("if (form.platform === 'kiro' && accountCategory.value === 'apikey-relay')"),
      source.indexOf("// For Bedrock type, create directly"),
    );
    const kiroOAuthExchangeSource = source.slice(
      source.indexOf("const handleKiroExchange"),
      source.indexOf("const handleGrokExchange"),
    );
    const kiroImportSource = source.slice(
      source.indexOf("const handleKiroImport"),
      source.indexOf("const handleCookieAuth"),
    );

    expect(source).toContain("DEFAULT_KIRO_API_REGION");
    expect(source).toContain("buildKiroAPIRegionOptions");
    expect(source).toContain("const kiroAPIRegion = ref(DEFAULT_KIRO_API_REGION)");
    expect(resetFormSource).toContain("kiroIDCRegion.value = 'us-east-1'");
    expect(resetFormSource).toContain("kiroAPIRegion.value = DEFAULT_KIRO_API_REGION");
    expect(source).toContain('data-testid="kiro-api-region-select-create"');
    expect(source).toContain('v-model="kiroAPIRegion"');
    expect(source).toContain("kiroAPIRegionOptions");
    expect(source).toContain("admin.accounts.oauth.kiro.apiRegionLabel");
    expect(source).toContain("admin.accounts.oauth.kiro.apiRegionHint");
    expect(source).toContain("admin.accounts.oauth.kiro.apiRegionLegacy");
    expect(source).toContain("admin.accounts.oauth.kiro.apiRegionUsEast");
    expect(source).toContain("admin.accounts.oauth.kiro.apiRegionEuCentral");
    expect(source).toContain("`${region} - ${t(regionLabelKey)}`");
    expect(buildKiroCredentialsSource).toContain(
      "credentials.api_region = kiroAPIRegion.value",
    );
    expect(kiroOAuthExchangeSource).toContain("buildKiroCredentials(tokenInfo)");
    expect(kiroImportSource).toContain("buildKiroCredentials(tokenInfo)");
    expect(nativeKiroAPIKeySource).toContain("api_region: kiroAPIRegion.value");
    expect(relayKiroAPIKeySource).not.toContain("api_region");
    expect(source).toMatch(
      /accountCategory === 'oauth-based' \|\|\s*accountCategory === 'apikey'/,
    );

    expect(enSource).toContain("apiRegionLabel: 'API Region'");
    expect(enSource).toContain(
      'apiRegionHint: "Select the region of this account\'s Kiro/Q Developer Profile. It can differ from the IAM Identity Center region."',
    );
    expect(enSource).toContain("apiRegionUsEast: 'US East (N. Virginia)'");
    expect(enSource).toContain("apiRegionEuCentral: 'Europe (Frankfurt)'");
    expect(enSource).toContain("apiRegionLegacy: '{region} (current legacy value)'");
    expect(zhSource).toContain("apiRegionLabel: 'API Region'");
    expect(zhSource).toContain(
      "apiRegionHint: '请选择该账号 Kiro/Q Developer Profile 所在区域，它可以与 IAM Identity Center Region 不同。'",
    );
    expect(zhSource).toContain("apiRegionUsEast: '美国东部（弗吉尼亚北部）'");
    expect(zhSource).toContain("apiRegionEuCentral: '欧洲（法兰克福）'");
    expect(zhSource).toContain("apiRegionLegacy: '{region}（当前历史值）'");
  });
});
