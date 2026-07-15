# CC Switch and OpenCode Guide Expansion Design

## Goal

Extend both user-facing key-configuration entry points so OpenCode and CC Switch are first-class choices, while preserving the existing Claude Code, Codex CLI, SDK, and WorkBuddy behavior.

## Scope

- In “使用密钥”, order the primary client tabs as Claude Code, Codex CLI, OpenCode, CC Switch, WorkBuddy. Existing SDK tabs remain available immediately before WorkBuddy so WorkBuddy is literally the final tab.
- Keep OpenCode’s existing configuration behavior, but move its generator into the shared client-configuration module so the beginner guide can reuse it.
- Add CC Switch instructions based on its official app workflow: choose the target app, add a Custom provider, fill name/endpoint/API key (and Codex model/protocol where applicable), save, enable, and restart the target client when required.
- In the beginner guide, add OpenCode and CC Switch to the client selector and persist those values for anonymous and authenticated users.
- Provide macOS, Windows, and Linux installation metadata for both new choices. OpenCode remains desktop-first with its official CLI fallback; CC Switch links to official releases and does not invent a CLI fallback.
- Keep the network-access warning and all existing guide steps.

## Configuration behavior

### OpenCode

- Generate `~/.config/opencode/opencode.json` on macOS/Linux and `%userprofile%\\.config\\opencode\\opencode.json` on Windows.
- Select the provider shape from the chosen key: Anthropic-compatible keys use the Anthropic provider, OpenAI-compatible keys use an OpenAI-compatible provider, and unified keys expose both configurations.
- Use `options.baseURL` and `options.apiKey`, matching OpenCode’s official provider schema.

### CC Switch

- CC Switch is a desktop configuration manager, not a gateway protocol. Render copyable field lists instead of pretending that its SQLite database is a user-editable config file.
- Anthropic-compatible keys produce a Claude Code Custom-provider field list using the bare gateway URL.
- OpenAI-compatible keys produce a Codex Custom-provider field list using the `/v1` gateway URL, Responses protocol, and the existing default Codex model.
- Unified keys produce both alternatives so the user can add either or both providers.
- The visible instructions tell users to click `+`, choose the target app and Custom provider, paste the fields, save, and enable.

## Beginner-guide behavior

- Extend `BeginnerGuideClient` and backend validation with `opencode` and `cc_switch` without changing progress version 1; the wire shape and step order are unchanged.
- OpenCode accepts active unified, Anthropic, or OpenAI keys.
- CC Switch accepts the same key types because it can configure Claude Code or Codex depending on the key.
- CC Switch installation and first-run panels allow desktop-only variants with no fake terminal command.
- Troubleshooting derives config locations per client; CC Switch points users to its app UI/data location rather than Claude/Codex paths.

## Non-goals

- No automatic `ccswitch://` deep-link import containing a user API key.
- No downloading or executing third-party installers during verification.
- No removal or redesign of the existing Python SDK tabs.
- No changes to production environments.

## Verification

- Frontend unit tests cover primary tab order, CC Switch field generation, OpenCode shared generation, four-client curriculum variants, key compatibility, desktop-only rendering, and localized labels.
- Backend service and handler tests cover accepting the two new persisted client values.
- Run frontend typecheck/build and focused backend tests. Installer URLs are checked against official documentation, not downloaded.
