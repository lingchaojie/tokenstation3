/* eslint-disable @typescript-eslint/triple-slash-reference */
/// <reference path="../../../vite-env.d.ts" />

import { mount } from "@vue/test-utils";
import { describe, expect, it, vi } from "vitest";
import SubscriptionPlanCard from "../SubscriptionPlanCard.vue";

const i18nMessages = vi.hoisted(() => ({
  "payment.days": "days",
  "payment.perWeek": "week",
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
  name: "Pro",
  description: "",
  price: 10,
  original_price: 0,
  features: [],
  validity_days: 30,
  validity_unit: "day",
  for_sale: true,
  sort_order: 1,
  ...overrides,
});

const activeSubscription = (overrides: Record<string, unknown> = {}) => ({
  id: 99,
  user_id: 1,
  group_id: null,
  plan_id: 1,
  plan_name: "Pro",
  status: "active" as const,
  starts_at: "2026-01-01T00:00:00Z",
  expires_at: "2026-02-01T00:00:00Z",
  daily_usage_usd: 0,
  weekly_usage_usd: 0,
  monthly_usage_usd: 0,
  seven_day_limit_usd: null,
  seven_day_usage_usd: 0,
  seven_day_remaining_usd: null,
  seven_day_reset_at: null,
  daily_window_start: null,
  weekly_window_start: null,
  monthly_window_start: null,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  ...overrides,
});

const mountPlanCard = (
  planOverrides: Record<string, unknown> = {},
  activeSubscriptions: Array<Record<string, unknown>> = [],
) =>
  mount(SubscriptionPlanCard, {
    props: {
      plan: basePlan(planOverrides),
      activeSubscriptions,
    },
  });

describe("SubscriptionPlanCard", () => {
  it("renders plan seven-day quota from generic plan fields", () => {
    const wrapper = mount(SubscriptionPlanCard, {
      props: {
        plan: basePlan({
          id: 2,
          name: "Plus monthly",
          description: "Everyday development",
          price: 399,
          seven_day_quota_usd: 110,
          features: ["Larger seven-day quota", "Recharge fallback"],
          sort_order: 20,
        }),
      },
      });

    const text = wrapper.text();

    expect(text).toContain("$110 / 7 days");
    expect(text).toContain("Total obtainable");
    expect(text).toContain("$440");
    expect(text).not.toContain("Rate");
    expect(text).not.toContain("×1");
  });

  it("displays weekly validity units as weeks instead of days", () => {
    const text = mountPlanCard({ validity_days: 1, validity_unit: "week" }).text();

    expect(text).toContain("/ week");
    expect(text).not.toContain("/ 1days");
  });

  it("does not show provider model scope tags", () => {
    const text = mountPlanCard().text();

    expect(text).not.toContain("Gemini");
    expect(text).not.toContain("Imagen");
  });

  it("shows current plan marker and separate renew button", () => {
    const wrapper = mount(SubscriptionPlanCard, {
      props: {
        plan: basePlan({
          id: 7,
          name: "Pro monthly",
          price: 799,
          seven_day_quota_usd: 260,
        }),
        activeSubscriptions: [
          {
            id: 9,
            user_id: 1,
            group_id: null,
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

  it("shows switch subscription for another active generic plan", () => {
    const wrapper = mount(SubscriptionPlanCard, {
      props: {
        plan: basePlan({
          id: 8,
          name: "Max monthly",
          price: 1599,
          seven_day_quota_usd: 550,
        }),
        activeSubscriptions: [
          {
            id: 9,
            user_id: 1,
            group_id: null,
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
    const wrapper = mountPlanCard();

    expect(wrapper.text()).toContain("Subscribe now");
    wrapper.get("button").trigger("click");
    expect(wrapper.emitted("select")?.[0][1]).toBe("subscribe");
  });

  it("shows the current opened count tag for limited plans", () => {
    const text = mountPlanCard({ seat_limit: 100, seat_used: 12 }).text();

    expect(text).toContain("当前已开通 12/100");
  });

  it("does not show plan-seat text for unlimited plans", () => {
    const text = mountPlanCard({ seat_limit: null, seat_used: 12 }).text();

    expect(text).not.toContain("当前已开通");
    expect(text).not.toContain("12/100");
    expect(text).not.toContain("名额");
  });

  it("disables full limited plans for new openings and does not emit select", async () => {
    const wrapper = mountPlanCard({
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

  it("does not treat a different active generic plan as a renewal", async () => {
    const wrapper = mountPlanCard(
      { id: 7, name: "Max monthly", seat_limit: 100, seat_used: 100, seat_full: true },
      [activeSubscription({ plan_id: 8, plan_name: "Pro monthly" })],
    );
    const button = wrapper.get("button");

    expect(button.attributes("disabled")).toBeDefined();
    expect(button.text()).toContain("名额已满");

    await button.trigger("click");

    expect(wrapper.emitted("select")).toBeUndefined();
  });

  it("allows same-plan renewal even when a limited plan is full", async () => {
    const wrapper = mountPlanCard(
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
