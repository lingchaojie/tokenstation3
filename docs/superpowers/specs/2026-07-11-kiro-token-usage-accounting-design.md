# KIRO Token Usage Accounting Fix Design

## Context

KIRO requests are currently estimated before translation by tokenizing the
canonical Anthropic `messages` JSON. Large image or document payloads therefore
count base64 transport bytes as text. The cache emulator then partitions this
inflated total and the response parser can replace upstream input usage with the
inflated simulated buckets. This produced the observed 15-million-token usage
records even though the translated KIRO request contained much less semantic
content.

The production investigation found distinct requests rather than duplicated or
cross-session records. The defect is in token accounting, not session identity
or database aggregation.

## Goals

- Persist upstream-reported token usage whenever KIRO provides it.
- Keep KIRO cache emulation enabled, including cache creation and cache-hit
  behavior when the configured ratio is `1`.
- Use cache emulation only to partition a trustworthy prompt-token total; it
  must never replace that total with the raw pre-translation estimate.
- When upstream usage is absent, estimate the final translated KIRO semantic
  payload without tokenizing image or document base64 as text.
- Apply identical final accounting to streaming and non-streaming requests.

## Non-goals

- Do not change KIRO credits, usage percentages, pricing, or quota logic.
- Do not change PDF extraction, truncation, conversion, or upstream request
  behavior in this fix.
- Do not disable account-level or group-level cache emulation.
- Do not change production systems as part of local implementation.

## Considered Approaches

### 1. Upstream-first accounting with normalized cache emulation (selected)

Parse all official KIRO token fields, preserve whether optional fields were
present, and normalize simulated cache buckets to the upstream prompt total.
Fall back to a semantic estimate of the translated KIRO payload only when
upstream usage is unavailable.

This keeps cache-hit reporting while preventing simulation from inventing a new
total. It is a larger change than a one-line guard but directly fixes both root
causes.

### 2. Disable cache emulation whenever upstream usage exists

This prevents inflated totals but loses simulated cache creation/read reporting
for responses that provide totals without cache fields. It would make the
configured ratio `1` ineffective in the common case and is therefore rejected.

### 3. Keep pre-translation estimation and only ignore or cap base64 fields

This is smaller, but it still counts content before KIRO translation, including
text later removed or truncated. A hard cap also hides errors rather than
representing model input. It is retained only as a structured safety fallback
for exceptional paths that cannot build a KIRO payload.

## Upstream Field Model

The parser will support both the official `metadataEvent` envelope and the
existing `messageMetadataEvent` compatibility envelope. Within `tokenUsage`, it
will parse:

- `uncachedInputTokens`
- `outputTokens`
- `totalTokens`
- `cacheReadInputTokens`
- `cacheWriteInputTokens`

Internal presence flags will distinguish an absent optional cache field from an
explicit zero. `cacheWriteInputTokens` maps to Anthropic-compatible
`cache_creation_input_tokens`.

The primary field reference is the generated Amazon Q Developer streaming
client in `aws/amazon-q-developer-cli`. The local upstream comparison baseline
remains `nianzs/sub2api` commit
`88a5666b478e234cace9090e0d5f483f1146cb96` as required by
`docs/kiro-upstream-sync.md`.

## Accounting Rules

The response parser resolves one prompt-token total in this order:

1. If upstream cache-read or cache-write fields are explicitly present, trust
   the upstream categories. The prompt total is uncached input plus cache read
   plus cache write.
2. Otherwise, when upstream `totalTokens` and `outputTokens` are present and
   valid, use `totalTokens - outputTokens` as the prompt total.
3. Otherwise, use upstream `uncachedInputTokens`.
4. Only if no valid upstream token usage is available, use the translated
   semantic fallback estimate.

Negative or internally impossible arithmetic is rejected in favor of the next
valid source. Returned values must satisfy:

```text
input_tokens + cache_read_input_tokens + cache_creation_input_tokens
  = resolved prompt-token total
```

When the upstream explicitly provides cache fields, those fields win and
simulation does not overwrite them. When the upstream provides only a total,
the cache emulator's proportions are scaled to that total. Rounding residuals
remain in `input_tokens`, so no token is created or lost. The 5-minute and
1-hour creation sub-buckets are scaled within the normalized creation bucket.

With a cache-emulation ratio of `1`, all tracker-eligible tokens may still be
classified as cache creation on a miss or cache read on a matching request. The
ratio does not mean that all prompt tokens are cached, and this fix does not
change its meaning.

## Translated Semantic Fallback

`BuildKiroPayloadWithContext` will calculate an input estimate from the typed
`KiroPayload` after all existing translation, filtering, tool-result truncation,
system injection, and PDF extraction have completed. The estimate is carried in
`KiroRequestContext` to the runtime and parser.

The estimator counts semantic fields that the model can consume:

- final user, assistant, and injected-system text;
- final tool-result text;
- tool names, descriptions, schemas, and tool-use inputs;
- small per-message and per-tool framing overhead;
- a fixed conservative image token allowance per translated image.

It does not tokenize profile ARNs, conversation IDs, transport JSON keys, or
`KiroImage.Source.Bytes`. Therefore increasing a base64 string without changing
the semantic request cannot create millions of estimated text tokens. Existing
PDF behavior is untouched; only the final extracted text already placed in the
translated payload is counted.

Exceptional paths that cannot build a typed KIRO payload will use the existing
Anthropic-body fallback after changing it to traverse recognized text, thinking,
tool input, and tool-result fields structurally. Unknown media/document blocks
contribute no base64 text tokens.

## Request and Response Data Flow

```text
Anthropic request
  -> existing KIRO translation
  -> typed translated semantic estimate
  -> upstream KIRO request
  -> upstream tokenUsage (preferred)
  -> cache tracker proportions normalized to trusted prompt total
  -> Anthropic response usage and persisted usage record
```

For streaming responses, the initial `message_start` uses the translated
fallback because upstream metadata has not arrived yet. Final stream usage and
the persisted usage record use the resolved upstream total when metadata
arrives. Non-streaming responses resolve upstream usage before constructing the
final response.

## Error Handling and Compatibility

- Explicit zero-valued upstream cache fields remain authoritative.
- Missing or malformed optional fields do not erase otherwise valid upstream
  usage.
- Existing `messageMetadataEvent` parsing remains compatible while adding the
  official `metadataEvent` path.
- Output tokens and unrelated KIRO usage data pass through unchanged.
- Cache fingerprinting and TTL matching remain unchanged; only the token total
  supplied to the partitioning calculation changes.

## Test Strategy

Tests will be written before implementation and will cover:

- parsing all five upstream fields from `metadataEvent`;
- compatibility with `messageMetadataEvent`;
- absent fields versus explicit zero cache fields;
- normalizing a 15.6-million-token simulated usage to a small upstream total;
- preserving explicit upstream cache-read and cache-write values;
- identical invariants in streaming and non-streaming responses;
- stable fallback estimates when image base64 size changes;
- counting final translated/truncated tool-result text rather than raw input;
- using translated fallback when upstream token usage is absent;
- preserving existing cache miss, hit, TTL, ratio, and account-isolation tests.

After targeted package tests pass, the affected backend package test suites will
run, followed by `graphify update .` and a final diff review. No production
deployment is included.

## External References

- Amazon Q Developer generated `TokenUsage` type and deserializer for the
  authoritative field names.
- `justlovemaki/AIClient2API` and `hank9999/kiro.rs` for structured fallback
  counting that excludes media base64.
- `jwadow/kiro-gateway` for upstream cache-field pass-through priority.

Only the relevant patterns are adopted. Credits/percentage accounting and
payload-wide base64 tokenization from other projects are deliberately excluded.
