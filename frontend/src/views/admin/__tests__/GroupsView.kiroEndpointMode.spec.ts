import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const currentDir = dirname(fileURLToPath(import.meta.url));
const groupsViewSource = readFileSync(
  resolve(currentDir, "../GroupsView.vue"),
  "utf8",
);

describe("GroupsView Kiro endpoint mode", () => {
  it("exposes Kiro q/krs endpoint mode in create and edit forms", () => {
    expect(groupsViewSource).toContain('v-model="createForm.kiro_endpoint_mode"');
    expect(groupsViewSource).toContain('v-model="editForm.kiro_endpoint_mode"');
    expect(groupsViewSource).toContain('const kiroEndpointModeOptions');
    expect(groupsViewSource).toContain('kiro_endpoint_mode: "q" as "q" | "krs"');
    expect(groupsViewSource).toContain('group.kiro_endpoint_mode === "krs" ? "krs" : "q"');
    expect(groupsViewSource).toContain('payload.kiro_endpoint_mode = "q"');
    expect(groupsViewSource).toContain('requestData.kiro_endpoint_mode = "q"');
  });
});
