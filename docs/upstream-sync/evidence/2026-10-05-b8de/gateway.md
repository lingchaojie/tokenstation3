# Gateway integration — in progress

## Protocol checkpoint

- Responses→Anthropic retains the opt-in KIRO compaction API, now using the
  upstream Opus/Sonnet 5.5 signed-thinking policy. Compaction remains scoped to
  callers setting EnableCompaction.
- Tool-start inline arguments use the local strings.Builder and retained-output
  budget. Real deltas replace the seed; seed-only calls emit a delta followed by
  identical done arguments. Charge on start, transfer without double-counting
  on stop, release discarded seed bytes when replaced by real deltas.
- Terminal Responses text recovery runs before local function-call argument
  repair; nonempty terminal text is authoritative, failed accumulators cannot
  synthesize success.

## Intermediate tests (not final-tree release evidence)

- `go test -count=1 ./internal/config ./internal/pkg/xai`: exit0,
  config0.724s/xai0.004s, tool output only.
- `go test -count=1 ./internal/pkg/apicompat -run TestB8deToolSeed`:
  behavioral RED exit1 (missing start-time budget charge),
  scratch tool-seed-red.log. An earlier incorrect test type name failed
  compilation and is not counted as RED.
- `go test -count=1 -timeout=5m ./internal/pkg/apicompat`: GREEN exit0,
  0.063s, scratch tool-seed-green.log.
- `go test -count=1 -p 2 -timeout=10m ./internal/pkg/...`: exit0,
  scratch task4-pkg-initial.log, including KIRO/kirocooldown/TypeSafe.
  This does not compile service/handler packages.

## Confirmed response-contract decision

Local parent gateway_forward_as_chat_completions.go and
openai_gateway_chat_completions_anthropic_native.go use the client's
stream_options.include_usage. Upstream removes that argument and emits real
provider usage regardless of absent/false options, suppressing it only when
the provider omitted usage. The new TestAnthropicChatStreamAuthoritativeUsage
explicitly checks all three client settings for identical output.

This is a public stream contract change, including local KIRO direct traffic
through ForwardAsChatCompletions, separate from internal billing. It has no
specific prior decision:

1. Recommended: retain local opt-in outward usage, independently adopt provider
   usage/cache normalization fixes.
2. Adopt upstream: always emit actual provider usage, even include_usage=false.

User selected option1: preserve local include_usage opt-in. Implement this on
both Anthropic compatibility paths, including KIRO direct forwarding; retain
normalized internal usage independently. Full service/handler integration and
final-tree tests remain incomplete.

## Gateway conflict checkpoint (service/handler tests not yet executable)

- Antigravity compatibility keeps the local capture scanner drain/join and raw
  read-activity idle tracking. New pre-content timers commit keepalive without
  marking semantic output or first-token latency. Errors after a ping are SSE
  terminal errors, not failover; errors before a ping retain provider headers
  through the local incomplete-stream error. Added header/non-billable-ping
  assertions to existing tests; awaiting shared-package compilation.
- Gemini cancellation marks 499 only before commitment; local capture-only
  failure handling and settlement of delivered partial output remain. Native
  Gemini error responses use upstream sanitization while retaining local typed
  terminal errors for capture/diagnostics.
- Excluded Composite routing in HTTP/WS/catalog paths, retaining TypeSafe-only
  protocol guards and the local display-only ModelsListConfig. Upstream model
  discovery regression is adapted to the local display configuration. Deleted
  the new Composite-only WS test; its generic local-provider HTTP test adapter
  remains in the common WS harness. No existing local tests were removed.
- Upstream native Anthropic normalization regressions and local disconnect /
  incomplete-capture tests both remain. Client usage is explicitly opt-in; the
  KIRO regression asserts final zero input and cache-read120 regardless of flag.

These are integration checkpoints, not a claim of complete Task4 audit or
passing service/handler tests. Shared schema/service conflicts remain; the
Claude fallback billing-ownership regression and full Task4 suite are pending.
