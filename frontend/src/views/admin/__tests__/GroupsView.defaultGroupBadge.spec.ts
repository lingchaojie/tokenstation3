import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";

import type { AdminGroup } from "@/types";
import GroupsView from "../GroupsView.vue";

const {
  listGroups,
  getSettings,
  getUsageSummary,
  getCapacitySummary,
  getModelsListCandidates,
  showError,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getSettings: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getModelsListCandidates: vi.fn(),
  showError: vi.fn(),
}));

vi.mock("@/api/admin", () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      getUsageSummary,
      getCapacitySummary,
      getModelsListCandidates,
    },
    settings: {
      getSettings,
    },
  },
}));

vi.mock("@/stores/app", () => ({
  useAppStore: () => ({
    showError,
    showSuccess: vi.fn(),
  }),
}));

vi.mock("@/stores/onboarding", () => ({
  useOnboardingStore: () => ({
    startTour: vi.fn(),
  }),
}));

vi.mock("vue-i18n", async () => {
  const actual = await vi.importActual<typeof import("vue-i18n")>("vue-i18n");
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) =>
        ({
          "admin.groups.defaultGroup": "默认",
        })[key] ?? key,
    }),
  };
});

const DataTableStub = {
  props: ["data"],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" data-test="group-row">
        <slot name="cell-name" :row="row" :value="row.name" />
      </div>
    </div>
  `,
};

function makeGroup(overrides: Partial<AdminGroup>): AdminGroup {
  return {
    id: 1,
    name: "Group",
    description: "",
    platform: "anthropic",
    rate_multiplier: 1,
    is_exclusive: false,
    status: "active",
    subscription_type: "standard",
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    allow_image_generation: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    image_price_1k: null,
    image_price_2k: null,
    image_price_4k: null,
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    model_routing_enabled: false,
    mcp_xml_inject: false,
    supported_model_scopes: [],
    sort_order: 0,
    allow_messages_dispatch: false,
    require_oauth_only: false,
    require_privacy_set: false,
    default_mapped_model: "",
    messages_dispatch_model_config: null,
    models_list_config: null,
    rpm_limit: 0,
    account_count: 0,
    active_account_count: 0,
    rate_limited_account_count: 0,
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-06-01T00:00:00Z",
    ...overrides,
  } as AdminGroup;
}

describe("GroupsView default group badge", () => {
  beforeEach(() => {
    listGroups.mockReset();
    getSettings.mockReset();
    getUsageSummary.mockReset();
    getCapacitySummary.mockReset();
    getModelsListCandidates.mockReset();
    showError.mockReset();

    listGroups.mockResolvedValue({
      items: [
        makeGroup({ id: 11, name: "Anthropic Default", platform: "anthropic" }),
        makeGroup({ id: 12, name: "Anthropic Other", platform: "anthropic" }),
        makeGroup({ id: 21, name: "OpenAI Default", platform: "openai" }),
        makeGroup({ id: 31, name: "Gemini Group", platform: "gemini" }),
      ],
      total: 4,
      page: 1,
      page_size: 20,
      pages: 1,
    });
    getSettings.mockResolvedValue({
      default_anthropic_group_id: 11,
      default_openai_group_id: 21,
    });
    getUsageSummary.mockResolvedValue([]);
    getCapacitySummary.mockResolvedValue([]);
    getModelsListCandidates.mockResolvedValue([]);
  });

  it("marks only the groups configured as global API key defaults", async () => {
    const wrapper = mount(GroupsView, {
      global: {
        stubs: {
          AppLayout: { template: "<div><slot /></div>" },
          TablePageLayout: {
            template:
              '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
          },
          DataTable: DataTableStub,
          Pagination: true,
          BaseDialog: true,
          ConfirmDialog: true,
          EmptyState: true,
          Select: true,
          Icon: true,
          PlatformIcon: true,
          GroupCapacityBadge: true,
          GroupRateMultipliersModal: true,
          GroupRPMOverridesModal: true,
          VueDraggable: true,
          Teleport: true,
        },
      },
    });

    await flushPromises();

    const rows = wrapper.findAll('[data-test="group-row"]');

    expect(rows).toHaveLength(4);
    expect(rows[0].text()).toContain("Anthropic Default");
    expect(rows[0].text()).toContain("默认");
    expect(rows[1].text()).toContain("Anthropic Other");
    expect(rows[1].text()).not.toContain("默认");
    expect(rows[2].text()).toContain("OpenAI Default");
    expect(rows[2].text()).toContain("默认");
    expect(rows[3].text()).toContain("Gemini Group");
    expect(rows[3].text()).not.toContain("默认");
  });
});
