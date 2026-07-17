import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const currentDir = dirname(fileURLToPath(import.meta.url));
const groupsViewSource = readFileSync(
  resolve(currentDir, "../GroupsView.vue"),
  "utf8",
);
const enOverviewSource = readFileSync(
  resolve(currentDir, "../../../i18n/locales/en/admin/overview.ts"),
  "utf8",
);
const zhOverviewSource = readFileSync(
  resolve(currentDir, "../../../i18n/locales/zh/admin/overview.ts"),
  "utf8",
);

describe("GroupsView Kiro endpoint mode", () => {
  it("exposes opt-in Kiro auto endpoint mode in create and edit forms", () => {
    expect(groupsViewSource).toContain('v-model="createForm.kiro_endpoint_mode"');
    expect(groupsViewSource).toContain('v-model="editForm.kiro_endpoint_mode"');
    expect(groupsViewSource).toContain('const kiroEndpointModeOptions');
    expect(groupsViewSource).toContain('kiro_endpoint_mode: "q" as "q" | "krs" | "auto"');
    expect(groupsViewSource).toContain('{ value: "auto", label: t("admin.groups.kiroCache.endpointModeAuto") }');
    expect(groupsViewSource).toContain('group.kiro_endpoint_mode === "auto"');
    expect(groupsViewSource).toContain('requestData.kiro_endpoint_mode === "auto"');
    expect(groupsViewSource).toContain('payload.kiro_endpoint_mode === "auto"');
    expect(groupsViewSource).toContain('payload.kiro_endpoint_mode = "q"');
    expect(groupsViewSource).toContain('requestData.kiro_endpoint_mode = "q"');
    expect(enOverviewSource).toContain("endpointModeAuto: 'Auto (Q → KRS on retryable failure)'");
    expect(zhOverviewSource).toContain("endpointModeAuto: '自动（Q 遇到可重试失败后切换 KRS）'");
  });
});
