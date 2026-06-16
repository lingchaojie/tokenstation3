import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const currentDir = dirname(fileURLToPath(import.meta.url));
const groupsViewSource = readFileSync(
  resolve(currentDir, "../GroupsView.vue"),
  "utf8",
);

const createModalSource = groupsViewSource.slice(
  groupsViewSource.indexOf('id="create-group-form"'),
  groupsViewSource.indexOf('id="edit-group-form"'),
);

describe("GroupsView create form billing type", () => {
  it("does not ask admins to choose billing type when creating a group", () => {
    expect(createModalSource).not.toContain("createForm.subscription_type");
    expect(groupsViewSource).toContain(
      'subscription_type: "standard" as SubscriptionType',
    );
  });
});
