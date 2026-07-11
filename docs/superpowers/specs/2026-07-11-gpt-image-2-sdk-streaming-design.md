# GPT Image 2 Python SDK Streaming Example Design

## Objective

Replace the existing non-streaming, incorrectly named image example in the user “Use API Key” modal with a copy-pasteable GPT Image 2 streaming example for the OpenAI Python SDK.

## Scope

This change is limited to the existing OpenAI image Python SDK tab in `UseKeyModal.vue`, its availability for OpenAI and unified keys, the client-tab wrapping layout, its Chinese and English tab labels, and the focused component tests.

The change does not alter gateway routes, backend streaming behavior, image model routing, pricing, or account configuration.

## Existing Behavior

The modal already has a separate `openai-imagen2-python-sdk` tab. It generates a synchronous `client.images.generate(...)` example with model name `imagen-2` and prints `image.data[0].url`.

That example no longer matches the supported GPT Image 2 contract:

- The project exposes the image model as `gpt-image-2`.
- The images gateway accepts `stream` and `partial_images` on `/v1/images/generations`.
- Streaming responses expose `image_generation.partial_image` and `image_generation.completed` events with base64 image data in `b64_json`.
- Current OpenAI Python SDK types expose the same streaming parameters and event names.

## Design

### Placement and naming

Reuse the existing dedicated image SDK tab instead of mixing image generation into the general OpenAI Responses text-streaming example.

Rename its visible Chinese and English label from `Imagen 2 Python SDK` to `GPT Image 2 Python SDK`. Keep the internal tab ID and locale key unchanged to avoid unnecessary component churn.

Show the dedicated image SDK tab for both OpenAI keys and unified keys. The unified-key order will be:

1. `Claude Code`
2. `Codex CLI`
3. `WorkBuddy`
4. `OpenCode`
5. `Anthropic Python SDK`
6. `OpenAI Python SDK`
7. `GPT Image 2 Python SDK`

The client-tab navigation will wrap onto additional lines instead of scrolling horizontally. Use flex wrapping with horizontal and vertical gaps so a new row starts flush left without inheriting `space-x` margins. Existing tab order remains unchanged apart from appending GPT Image 2 to unified keys.

### Generated Python example

The generated file remains `imagen2_client.py` so existing UI behavior and filename-based expectations remain stable. Its code will:

1. Import `base64`, `Path`, and `OpenAI`.
2. Construct the client with the displayed API key and normalized `/v1` base URL.
3. Call `client.images.generate` with:
   - `model="gpt-image-2"`
   - the existing fox prompt
   - `size="1024x1024"`
   - `stream=True`
   - `partial_images=2`
4. Iterate over the returned SDK event stream.
5. Decode every event carrying `b64_json`.
6. Save partial events as `partial_<index>.png` and the completion event as `image.png`.
7. Print each written path so a terminal user can see progress and find the final output.

The event loop will explicitly distinguish `image_generation.partial_image` from `image_generation.completed`. It will not depend on the gateway’s optional convenience `url` field because the official SDK contract guarantees `b64_json` for GPT image models.

### Error behavior

Network, authentication, API, and filesystem errors are left visible as Python exceptions. The example will not add broad exception handling that could hide actionable SDK errors.

If an unknown stream event arrives, it is ignored. If a recognized image event lacks `b64_json`, it is also ignored instead of writing an invalid file.

## Testing

The focused `UseKeyModal.spec.ts` test will verify that the generated example contains:

- the normalized `/v1` base URL and displayed API key;
- `model="gpt-image-2"` and no `model="imagen-2"`;
- `stream=True` and `partial_images=2`;
- both official event type strings;
- base64 decoding;
- partial and completed output paths.

The tab-layout tests will verify:

- OpenAI keys retain their existing order and end with `GPT Image 2 Python SDK`.
- Unified keys retain both `Anthropic Python SDK` and `OpenAI Python SDK`, then append `GPT Image 2 Python SDK`.
- The client navigation uses wrapping with horizontal and vertical gaps and does not use horizontal scrolling.

The locale test will verify the Chinese and English tab labels both use `GPT Image 2 Python SDK`.

The focused component suite will run before implementation to demonstrate failure, then again after implementation. Frontend type checking and the existing final verification workflow will cover the completed combined change.

## Success Criteria

- A user can copy the dedicated example and receive progressive GPT Image 2 output through this project’s gateway.
- The example writes preview files while streaming and a stable final `image.png` on completion.
- The public model name is `gpt-image-2` everywhere in the generated example and visible tab label.
- Both OpenAI and unified keys expose the dedicated GPT Image 2 example.
- Client tabs wrap onto additional rows when they do not fit, with each row left-aligned.
- The general OpenAI text-streaming SDK example remains unchanged.
- Focused tests and frontend static validation pass.
