# Codex API Key Example Hardening Design

## Objective

Make the user-facing Codex API-key example difficult to misapply while preserving the authentication flow that current Codex versions already accept.

The generated example must continue to use a custom `OpenAI` provider backed by `auth.json`. It must not switch existing users to an environment-variable-only flow.

## Scope

This change is limited to the Codex section of the user “Use API Key” modal:

- Normalize the generated Codex provider URL to end in `/v1`.
- Keep generating both `config.toml` and `auth.json`.
- Explain that both files are required.
- Warn users to merge `OPENAI_API_KEY` into an existing `auth.json` instead of overwriting other credentials.
- State that a literal API key must not be placed in `env_key`.
- Tell users to restart Codex and open a new conversation after saving the files.
- Strengthen component tests around the complete configuration and authentication contract.

The change does not alter gateway routes, API-key validation, model routing, model defaults, or backend authentication.

## Existing Behavior

`UseKeyModal.vue` generates a valid two-file Codex configuration:

- `config.toml` selects `model_provider = "OpenAI"`, defines `[model_providers.OpenAI]`, and sets `requires_openai_auth = true`.
- `auth.json` stores the selected gateway key as `OPENAI_API_KEY`.

An isolated Codex 0.144.1 strict `doctor` run accepts this structure and reports API-key authentication. The current weakness is presentation: the UI does not clearly describe the two-file dependency or safe handling of an existing `auth.json`. The Codex generator also receives the raw public base URL even though the component already computes a normalized `/v1` API base.

## Design

### Configuration generation

Both unified-key and OpenAI-key Codex branches will call `generateOpenAIFiles(apiBase, apiKey)`. The existing `ensureV1` normalization makes this idempotent:

- `https://gateway.example.com` becomes `https://gateway.example.com/v1`.
- `https://gateway.example.com/v1` remains unchanged.
- Trailing slashes are removed before normalization.

The generated provider contract remains:

```toml
model_provider = "OpenAI"

[model_providers.OpenAI]
name = "OpenAI"
base_url = "https://gateway.example.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

The API key remains exclusively in the separately displayed `auth.json` example:

```json
{
  "OPENAI_API_KEY": "<gateway key>"
}
```

`config.toml` must not contain the literal key or an `env_key` entry.

### User guidance

The Codex description and platform notes will communicate four requirements:

1. Save both displayed files in the Codex configuration directory.
2. If `auth.json` already exists, merge the `OPENAI_API_KEY` property instead of replacing the whole file.
3. Do not place the literal key in `env_key`; this example deliberately uses `auth.json`.
4. Fully restart Codex and create a new conversation after saving the configuration.

The same meaning will be provided in Chinese and English. Windows guidance keeps the existing `%userprofile%\.codex` path; macOS/Linux guidance keeps `~/.codex`.

For unified keys, selecting the Codex client must show Codex-specific guidance rather than the generic unified-provider note. Other client tabs retain their current descriptions and notes.

### Error prevention

The modal will continue to render `config.toml` and `auth.json` as separate copyable blocks so users can inspect each file. Guidance will avoid claiming that copying one block configures Codex automatically.

No new credential-writing command will be added. This avoids shell-history exposure, platform-specific persistence behavior, and automatic replacement of an existing credential file.

## Testing

Component tests will verify the generated artifacts as a single contract:

- A bare base URL is normalized to `/v1` for Codex.
- An existing `/v1` suffix is not duplicated.
- `config.toml` contains the selected provider, normalized base URL, Responses wire API, and `requires_openai_auth = true`.
- `config.toml` contains neither the API key nor `env_key`.
- `auth.json` parses as JSON and contains exactly the expected `OPENAI_API_KEY` value.
- Unified-key Codex selection shows the Codex-specific safety guidance.
- Chinese and English locale keys remain type-compatible with the existing locale structure.

The focused `UseKeyModal.spec.ts` suite will be run first, followed by the frontend type check or the closest existing validation command that covers locale and Vue template changes.

## Success Criteria

- Users copying the displayed Codex example receive a valid `/v1` provider configuration.
- The UI clearly identifies the two required files and safe merge behavior.
- The UI explicitly prevents the `env_key = "<literal key>"` mistake.
- Existing non-Codex key examples are unchanged.
- Focused component tests and frontend static validation pass.
