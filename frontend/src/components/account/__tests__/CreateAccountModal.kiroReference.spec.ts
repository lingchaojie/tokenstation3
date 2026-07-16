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
    const apiRegionSelectorOpeningTag =
      source.match(/<div\b[^>]*data-testid="kiro-api-region-select-create"[^>]*>/s)?.[0] ?? "";
    const apiRegionSelectorVisibility =
      apiRegionSelectorOpeningTag.match(/v-if="([^"]*)"/s)?.[1] ?? "";

    expect(source).toContain("DEFAULT_KIRO_API_REGION");
    expect(source).toContain("buildKiroAPIRegionOptions");
    expect(source).toContain("const kiroAPIRegion = ref(DEFAULT_KIRO_API_REGION)");
    expect(resetFormSource).toContain("kiroIDCRegion.value = 'us-east-1'");
    expect(resetFormSource).toContain("kiroAPIRegion.value = DEFAULT_KIRO_API_REGION");
    expect(apiRegionSelectorOpeningTag).not.toBe("");
    expect(apiRegionSelectorVisibility.replace(/\s+/g, " ").trim()).toBe(
      "form.platform === 'kiro' && (accountCategory === 'oauth-based' || accountCategory === 'apikey')",
    );
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

  it("preserves the selected Kiro API region across every reauthorization mode", () => {
    const kiroTemplateSource = reauthSource.slice(
      reauthSource.indexOf('<div v-if="isKiro"'),
      reauthSource.indexOf("<OAuthAuthorizationFlow"),
    );
    const selectorOpeningTag =
      kiroTemplateSource.match(
        /<div\b[^>]*data-testid="kiro-api-region-select-reauth"[^>]*>/s,
      )?.[0] ?? "";
    const selectorIndex = kiroTemplateSource.indexOf(selectorOpeningTag);
    const idcOnlyIndex = kiroTemplateSource.indexOf(
      '<div v-if="kiroAccountType === \'idc\'"',
    );
    const importOnlyIndex = kiroTemplateSource.indexOf(
      '<div v-if="isKiroImportMode"',
    );
    const initializationSource = reauthSource.slice(
      reauthSource.indexOf("// Watchers"),
      reauthSource.indexOf("const resetState"),
    );
    const resetStateSource = reauthSource.slice(
      reauthSource.indexOf("const resetState"),
      reauthSource.indexOf("const kiroModeClass"),
    );
    const kiroAPIRegionOptionsSource = reauthSource.slice(
      reauthSource.indexOf("const kiroAPIRegionOptions = computed(() =>"),
      reauthSource.indexOf("const isManualInputMethod"),
    );
    const handleExchangeSource = reauthSource.slice(
      reauthSource.indexOf("const handleExchangeCode"),
      reauthSource.indexOf("const handleKiroImport"),
    );
    const kiroExchangeSource = handleExchangeSource.slice(
      handleExchangeSource.indexOf("} else if (isKiro.value) {"),
      handleExchangeSource.indexOf("} else if (isAntigravity.value) {"),
    );
    const kiroImportSource = reauthSource.slice(
      reauthSource.indexOf("const handleKiroImport"),
      reauthSource.indexOf("const handleCookieAuth"),
    );
    const apiRegionAssignments =
      reauthSource.match(/credentials\.api_region\s*=\s*kiroAPIRegion\.value/g) ?? [];

    expect(reauthSource).toContain("DEFAULT_KIRO_API_REGION");
    expect(reauthSource).toContain("resolveKiroAPIRegion");
    expect(reauthSource).toContain("const kiroAPIRegion = ref(DEFAULT_KIRO_API_REGION)");
    expect(kiroAPIRegionOptionsSource).toContain(
      "buildKiroAPIRegionOptions(kiroAPIRegion.value,",
    );
    expect(reauthSource).toContain("admin.accounts.oauth.kiro.apiRegionLegacy");
    expect(reauthSource).toContain("admin.accounts.oauth.kiro.apiRegionUsEast");
    expect(reauthSource).toContain("admin.accounts.oauth.kiro.apiRegionEuCentral");
    expect(reauthSource).toContain("`${region} - ${t(regionLabelKey)}`");

    expect(selectorOpeningTag).not.toBe("");
    expect(selectorOpeningTag).not.toContain("v-if");
    expect(selectorIndex).toBeGreaterThan(idcOnlyIndex);
    expect(selectorIndex).toBeGreaterThan(importOnlyIndex);
    expect(kiroTemplateSource).toMatch(
      /<div v-if="isKiroImportMode"[\s\S]*<\/div>\s*<\/div>\s*<div\b[^>]*data-testid="kiro-api-region-select-reauth"[^>]*>[\s\S]*<\/div>\s*<\/div>\s*$/,
    );
    expect(kiroTemplateSource).toContain('v-model="kiroAPIRegion"');
    expect(kiroTemplateSource).toContain(':options="kiroAPIRegionOptions"');
    expect(kiroTemplateSource).toContain("admin.accounts.oauth.kiro.apiRegionLabel");
    expect(kiroTemplateSource).toContain("admin.accounts.oauth.kiro.apiRegionHint");

    expect(initializationSource).toContain(
      "kiroAPIRegion.value = resolveKiroAPIRegion(creds.api_region)",
    );
    expect(initializationSource).not.toMatch(
      /kiroAPIRegion\.value\s*=\s*resolveKiroAPIRegion\([^)]*creds\.region/,
    );
    expect(resetStateSource).toContain(
      "kiroAPIRegion.value = DEFAULT_KIRO_API_REGION",
    );

    expect(apiRegionAssignments).toHaveLength(2);
    expect(kiroExchangeSource.match(/credentials\.api_region\s*=\s*kiroAPIRegion\.value/g)).toHaveLength(1);
    expect(kiroImportSource.match(/credentials\.api_region\s*=\s*kiroAPIRegion\.value/g)).toHaveLength(1);
    expect(kiroExchangeSource).toContain(
      "const credentials = kiroOAuth.buildCredentials(tokenInfo)",
    );
    expect(kiroImportSource).toContain(
      "const credentials = kiroOAuth.buildCredentials(tokenInfo)",
    );
    expect(kiroExchangeSource).toContain("buildUpdatedCredentials(credentials)");
    expect(kiroImportSource).toContain("buildUpdatedCredentials(credentials)");
    expect(reauthSource).not.toMatch(
      /api_region\s*(?:=|:)\s*(?:kiroIDCRegion|tokenInfo\.region)/,
    );
  });
});
