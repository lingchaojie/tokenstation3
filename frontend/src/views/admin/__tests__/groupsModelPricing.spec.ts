import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import type { ChannelModelPricing } from "@/api/admin/channels";
import {
  emptyGroupPricing,
  groupPricingFromAPI,
  groupPricingToAPI,
} from "@/views/admin/groupsModelPricing";

const apiPricing: ChannelModelPricing = {
  platform: "openai",
  models: ["gpt-5.6-sol"],
  billing_mode: "token",
  input_price: 2e-6,
  output_price: 8e-6,
  cache_write_price: null,
  cache_read_price: 2e-7,
  fast_multiplier: 1.5,
  flex_multiplier: 0.75,
  image_input_price: 4e-6,
  image_output_price: 10e-6,
  per_request_price: null,
  intervals: [],
  time_pricing: null,
};

const currentDir = dirname(fileURLToPath(import.meta.url));
const groupsViewSource = readFileSync(
  resolve(currentDir, "../GroupsView.vue"),
  "utf8",
);

describe("group model pricing round-trip", () => {
  it("preserves Fast/Flex multipliers from API through the editable form", () => {
    const form = groupPricingFromAPI([apiPricing]);

    expect(form).toHaveLength(1);
    expect(form[0].fast_multiplier).toBe(1.5);
    expect(form[0].flex_multiplier).toBe(0.75);

    expect(groupPricingToAPI(form, "openai")).toEqual([apiPricing]);
  });

  it("initializes new entries with empty tier multipliers", () => {
    expect(emptyGroupPricing()).toMatchObject({
      fast_multiplier: null,
      flex_multiplier: null,
    });
  });

  it("enables tier multiplier controls in both create and edit forms", () => {
    expect(groupsViewSource.match(/enable-tier-multipliers/g)).toHaveLength(2);
  });
});
