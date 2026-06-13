/* eslint-disable @typescript-eslint/triple-slash-reference */
/// <reference path="../../../vite-env.d.ts" />

import { mount } from "@vue/test-utils";
import { describe, expect, it, vi } from "vitest";
import SubscriptionPlanCard from "../SubscriptionPlanCard.vue";

const i18nMessages = vi.hoisted(() => ({
  "payment.days": "days",
  "payment.currentSubscription": "Current subscription",
  "payment.models": "Models",
  "payment.planCard.quota": "Quota",
  "payment.planCard.rate": "Rate",
  "payment.planCard.sevenDayQuota": "7-day quota",
  "payment.planCard.totalMonthlyQuota": "Total obtainable",
  "payment.planCard.unlimited": "Unlimited",
  "payment.planCard.weeklyLimit": "Weekly",
  "payment.renewNow": "Renew",
  "payment.subscribeNow": "Subscribe now",
  "payment.switchSubscription": "Switch subscription",
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({
    t: (key: string) => i18nMessages[key as keyof typeof i18nMessages] ?? key,
    locale: { value: 'en' },
  }),
}));

const basePlan = (overrides: Record<string, unknown> = {}) => ({
  id: 1,
  group_id: 10,
  group_platform: "openai",
  group_name: "OpenAI",
  name: "Pro",
  description: "",
  price: 10,
  original_price: 0,
  features: [],
  rate_multiplier: 1,
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
  validity_days: 30,
  validity_unit: "day",
  supported_model_scopes: ["claude", "gemini_text", "gemini_image"],
  for_sale: true,
  sort_order: 1,
  ...overrides,
});

const activeSubscription = (overrides: Record<string, unknown> = {}) => ({
  id: 99,
  user_id: 1,
  group_id: 10,
  plan_id: 1,
  status: "active" as const,
  starts_at: "2026-01-01T00:00:00Z",
  expires_at: "2026-02-01T00:00:00Z",
  daily_usage_usd: 0,
  weekly_usage_usd: 0,
  monthly_usage_usd: 0,
  daily_window_start: null,
  weekly_window_start: null,
  monthly_window_start: null,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  ...overrides,
});

const mountPlanCard = (
  groupPlatform: string,
  planOverrides: Record<string, unknown> = {},
  activeSubscriptions: Array<Record<string, unknown>> = [],
) =>
  mount(SubscriptionPlanCard, {
    props: {
      plan: basePlan({ group_platform: groupPlatform, ...planOverrides }),
      activeSubscriptions,
    },
  });

describe("SubscriptionPlanCard", () => {
  it("renders plan seven-day quota and hides legacy weekly limit when present", () => {
    const wrapper = mount(SubscriptionPlanCard, {
      props: {
        plan: {
          id: 2,
          group_id: 1,
          group_platform: "anthropic",
          group_name: "LINX2 Subscription",
          rate_multiplier: 1,
          daily_limit_usd: null,
          weekly_limit_usd: 999,
          monthly_limit_usd: null,
          supported_model_scopes: ["claude"],
          name: "Plus monthly",
          description: "Everyday development",
          price: 399,
          validity_days: 30,
          validity_unit: "day",
          seven_day_quota_usd: 110,
          features: ["Larger seven-day quota", "Recharge fallback"],
          for_sale: true,
          sort_order: 20,
        },
      },
      });

    const text = wrapper.text();

    expect(text).toContain("$110 / 7 days");
    expect(text).toContain("Total obtainable");
    expect(text).toContain("$440");
    expect(text).not.toContain("Rate");
    expect(text).not.toContain("×1");
    expect(text).not.toContain("$999");
  });

  it("does not show provider model scope tags", () => {
    const text = mountPlanCard("antigravity").text();

    expect(text).not.toContain("Gemini");
    expect(text).not.toContain("Imagen");
  });

  it("shows current plan marker and separate renew button", () => {
    const wrapper = mount(SubscriptionPlanCard, {
      props: {
        plan: {
          id: 7,
          group_id: 3,
          group_platform: "anthropic",
          name: "Pro monthly",
          description: "",
          price: 799,
          validity_days: 30,
          validity_unit: "day",
          seven_day_quota_usd: 260,
          features: [],
          rate_multiplier: 1,
          for_sale: true,
          sort_order: 1,
        },
        activeSubscriptions: [
          {
            id: 9,
            user_id: 1,
            group_id: 3,
            plan_id: 7,
            plan_name: "Pro monthly",
            status: "active",
            starts_at: "2030-01-01T00:00:00Z",
            expires_at: "2030-02-01T00:00:00Z",
            daily_usage_usd: 0,
            weekly_usage_usd: 0,
            monthly_usage_usd: 0,
            seven_day_limit_usd: 260,
            seven_day_usage_usd: 0,
            seven_day_remaining_usd: 260,
            seven_day_reset_at: null,
            daily_window_start: null,
            weekly_window_start: null,
            monthly_window_start: null,
            created_at: "2030-01-01T00:00:00Z",
            updated_at: "2030-01-01T00:00:00Z",
          },
        ],
      },
    });

    expect(wrapper.text()).toContain("Current subscription");
    expect(wrapper.text()).toContain("Renew");
    expect(wrapper.find("button[disabled]").text()).toContain("Current subscription");
    wrapper.findAll("button")[1].trigger("click");
    expect(wrapper.emitted("select")?.[0][1]).toBe("renew");
  });

  it("shows switch subscription for non-current active same-group plans", () => {
    const wrapper = mount(SubscriptionPlanCard, {
      props: {
        plan: {
          id: 8,
          group_id: 3,
          group_platform: "anthropic",
          name: "Max monthly",
          description: "",
          price: 1599,
          validity_days: 30,
          validity_unit: "day",
          seven_day_quota_usd: 550,
          features: [],
          rate_multiplier: 1,
          for_sale: true,
          sort_order: 1,
        },
        activeSubscriptions: [
          {
            id: 9,
            user_id: 1,
            group_id: 3,
            plan_id: 7,
            plan_name: "Pro monthly",
            status: "active",
            starts_at: "2030-01-01T00:00:00Z",
            expires_at: "2030-02-01T00:00:00Z",
            daily_usage_usd: 0,
            weekly_usage_usd: 0,
            monthly_usage_usd: 0,
            seven_day_limit_usd: 260,
            seven_day_usage_usd: 0,
            seven_day_remaining_usd: 260,
            seven_day_reset_at: null,
            daily_window_start: null,
            weekly_window_start: null,
            monthly_window_start: null,
            created_at: "2030-01-01T00:00:00Z",
            updated_at: "2030-01-01T00:00:00Z",
          },
        ],
      },
    });

    expect(wrapper.text()).toContain("Switch subscription");
    expect(wrapper.text()).not.toContain("Current subscription");
    wrapper.get("button").trigger("click");
    expect(wrapper.emitted("select")?.[0][1]).toBe("switch");
  });

  it("shows subscribe CTA when there is no active subscription", () => {
    const wrapper = mountPlanCard("anthropic");

    expect(wrapper.text()).toContain("Subscribe now");
    wrapper.get("button").trigger("click");
    expect(wrapper.emitted("select")?.[0][1]).toBe("subscribe");
  });

  it("shows the current opened count tag for limited plans", () => {
    const text = mountPlanCard("openai", { seat_limit: 100, seat_used: 12 }).text();

    expect(text).toContain("当前已开通 12/100");
  });

  it("does not show plan-seat text for unlimited plans", () => {
    const text = mountPlanCard("openai", { seat_limit: null, seat_used: 12 }).text();

    expect(text).not.toContain("当前已开通");
    expect(text).not.toContain("12/100");
    expect(text).not.toContain("名额");
  });

  it("disables full limited plans for new openings and does not emit select", async () => {
    const wrapper = mountPlanCard("openai", {
      seat_limit: 100,
      seat_used: 100,
      seat_full: true,
    });
    const button = wrapper.get("button");

    expect(button.attributes("disabled")).toBeDefined();
    expect(button.text()).toContain("名额已满");

    await button.trigger("click");

    expect(wrapper.emitted("select")).toBeUndefined();
  });

  it("does not treat a different plan in the same group as a renewal", async () => {
    const wrapper = mountPlanCard(
      "openai",
      { id: 7, group_id: 10, seat_limit: 100, seat_used: 100, seat_full: true },
      [activeSubscription({ plan_id: 8, group_id: 10 })],
    );
    const button = wrapper.get("button");

    expect(button.attributes("disabled")).toBeDefined();
    expect(button.text()).toContain("名额已满");

    await button.trigger("click");

    expect(wrapper.emitted("select")).toBeUndefined();
  });

  it("allows same-plan renewal even when a limited plan is full", async () => {
    const wrapper = mountPlanCard(
      "openai",
      { id: 7, seat_limit: 100, seat_used: 100, seat_full: true },
      [activeSubscription({ plan_id: 7 })],
    );
    const buttons = wrapper.findAll("button");
    const button = buttons[buttons.length - 1];

    expect(button.attributes("disabled")).toBeUndefined();

    await button.trigger("click");

    expect(wrapper.emitted("select")?.[0]).toEqual([expect.objectContaining({ id: 7 }), "renew"]);
  });
});
