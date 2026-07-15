---
type: "query"
date: "2026-07-15T00:53:09.440074+00:00"
question: "How does the beginner getting-started guide connect homepage discovery, dashboard prompt state, API key selection, and shared client configuration?"
contributor: "graphify"
outcome: "useful"
source_nodes: ["HomeView.vue", "BeginnerGuideCard.vue", "index.ts", "GettingStartedView.vue", "UserDashboardContent.vue", "BeginnerWelcomeDialog.vue", "AppSidebar.vue", "beginnerGuide.ts", "GuideApiKeyStep.vue", "keysAPI"]
---

# Q: How does the beginner getting-started guide connect homepage discovery, dashboard prompt state, API key selection, and shared client configuration?

## Answer

Expanded from the original query via graph vocabulary: [beginner, guide, homepage, dashboard, prompt, state, api, key, selection, shared, client, configuration]. The graph connects HomeView.vue and BeginnerGuideCard.vue to the canonical route in router/index.ts and GettingStartedView.vue; UserDashboardContent.vue, BeginnerWelcomeDialog.vue, and AppSidebar.vue to the beginnerGuide.ts API/store prompt state; GuideApiKeyStep.vue to keysAPI; and GettingStartedView.vue plus UseKeyModal.vue to buildClientConfigFiles() in clientConfigFiles.ts. The focused traversal surfaced the route, both beginner-guide state modules, dashboard entry points, API-key step, and shared client configuration module.

## Outcome

- Signal: useful

## Source Nodes

- HomeView.vue
- BeginnerGuideCard.vue
- index.ts
- GettingStartedView.vue
- UserDashboardContent.vue
- BeginnerWelcomeDialog.vue
- AppSidebar.vue
- beginnerGuide.ts
- GuideApiKeyStep.vue
- keysAPI
- buildClientConfigFiles()
- clientConfigFiles.ts
- UseKeyModal.vue