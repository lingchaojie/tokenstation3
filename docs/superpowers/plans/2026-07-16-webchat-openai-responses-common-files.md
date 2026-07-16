# WebChat OpenAI Responses Common Files Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route every GPT/OpenAI WebChat turn through `/v1/responses`, expose automatic Web Search only from the backend, send images and OpenAI-supported local files as native inputs, and replace the two attachment buttons with one upload entry.

**Architecture:** Preserve the provider-neutral WebChat persistence model and non-OpenAI forwarding paths. A centralized Go file-type registry validates filename, declared MIME, and stored bytes; the OpenAI-only Responses builder emits typed inputs and the dispatch layer selects it unconditionally for provider `openai`. A small frontend accept-list utility supplies provider-aware file-picker hints while the backend remains authoritative.

**Tech Stack:** Go, Gin, archive/zip, OpenAI Responses compatibility types, Testify/GJSON, Vue 3, Pinia, TypeScript, Vitest, graphify.

## Global Constraints

- Scope is GPT/OpenAI WebChat input only; do not add attachment forwarding behavior to Claude, Anthropic, Kiro, Gemini, or other providers.
- Existing `gpt-image-2` output remains supported; do not add ordinary GPT image generation or PPT/PDF output generation.
- Every GPT/OpenAI WebChat turn—text, image, file, or mixed—uses `/v1/responses` with `store: false`.
- A searchable GPT model always receives `tools: [{"type":"web_search"}]` and `tool_choice: "auto"`; never force a search call.
- GPT/OpenAI renders no search toggle and no search explanation copy.
- The Composer renders one file-upload button, uses the existing `upload` icon, and keeps one-file-per-selection behavior.
- Accept OpenAI-documented local PDF, document, presentation, spreadsheet, text, and code formats only; reject unknown binaries, archives, executables, audio, video, and Google cloud placeholder files.
- Validate filename extension, declared MIME, and real bytes at upload and again before OpenAI forwarding; never trust metadata alone.
- Keep the 20 MiB per-attachment limit and reject more than 50 MiB of raw `input_file` data in one Responses request.
- PDF `input_file` uses `detail: "auto"`; non-PDF `input_file` parts omit `detail`.
- Never omit an invalid or provider-incompatible attachment and continue as text-only.
- Non-OpenAI search controls and legacy attachment forwarding contents remain unchanged; newly accepted OpenAI-only types fail explicitly on those paths.
- Do not modify or deploy production. A real OpenAI OAuth `input_file` request needs separate user approval and must use the existing WebChat/OpenAI flow and configured proxy.
- Preserve unrelated dirty files; do not stage generated `graphify-out` artifacts or unrelated documentation.

## Completed Prerequisites

- Task 1 is complete at `d4e42158c`: GPT text models expose image, file, and search capabilities while `gpt-image-2` remains isolated.
- Task 2 is complete at `b694ef60f`: Responses file fields, bounded stored reads, basic PDF/DOCX validation, and previewless file counting exist.
- The approved expanded design is committed at `3e76836a6`.
- Task 3 was paused after RED and GREEN. Its three working-tree files are intentionally uncommitted; preserve and review them rather than recreating or discarding them.

---

### Task 3: Finish the OpenAI-only native Responses request

**Files:**
- Modify: `backend/internal/service/web_chat_openai_responses.go`
- Modify: `backend/internal/service/web_chat_openai_responses_test.go`
- Modify: `backend/internal/service/web_chat_adapter_test.go`

**Interfaces:**
- Consumes: `readWebChatStoredAttachment`, `webChatAttachmentDataURL`, `apicompat.ResponsesInputItem`, and `apicompat.ResponsesRequest`.
- Produces: `BuildOpenAIWebChatResponsesPayload(ctx context.Context, storage WebChatStorage, caps WebChatModelCapability, messages []WebChatMessage, stream bool, options ...WebChatCompletionsPayloadOptions) ([]byte, error)`.

- [ ] **Step 1: Audit the interrupted working tree without discarding it**

Run:

```bash
git status --short -- backend/internal/service/web_chat_openai_responses.go backend/internal/service/web_chat_openai_responses_test.go backend/internal/service/web_chat_adapter_test.go
git diff --check -- backend/internal/service/web_chat_openai_responses.go backend/internal/service/web_chat_openai_responses_test.go backend/internal/service/web_chat_adapter_test.go
```

Expected: exactly those three Task 3 files are modified and `git diff --check` is clean. Do not use reset, checkout, restore, or clean.

- [ ] **Step 2: Preserve the already-captured RED evidence**

The interrupted implementer already ran this command before production edits:

```bash
cd backend
go test ./internal/service -run '^TestBuildOpenAIWebChatResponsesPayload' -count=1
```

Expected RED evidence in the task report: compilation failed because `BuildOpenAIWebChatResponsesPayload` was undefined. Do not destroy valid working changes merely to reproduce RED; append this prior evidence to `.superpowers/sdd/task-3-report.md`.

- [ ] **Step 3: Verify the typed input implementation**

The implementation must retain this control flow:

```go
func buildOpenAIWebChatResponsesInput(ctx context.Context, storage WebChatStorage, messages []WebChatMessage) ([]apicompat.ResponsesInputItem, error) {
	items := make([]apicompat.ResponsesInputItem, 0, len(messages))
	var declaredFileBytes int64
	for _, message := range messages {
		if message.Role != WebChatRoleUser {
			continue
		}
		for _, attachment := range message.Attachments {
			if attachment.Kind != WebChatAttachmentKindFile {
				continue
			}
			declaredFileBytes += attachment.SizeBytes
			if declaredFileBytes > webChatMaxResponsesFileBytes {
				return nil, ErrWebChatUploadRejected
			}
		}
	}

	var actualFileBytes int64
	for _, message := range messages {
		parts := make([]apicompat.ResponsesContentPart, 0, 1+len(message.Attachments))
		if message.ContentText != "" {
			partType := "input_text"
			if message.Role == WebChatRoleAssistant {
				partType = "output_text"
			}
			parts = append(parts, apicompat.ResponsesContentPart{Type: partType, Text: message.ContentText})
		}
		if message.Role == WebChatRoleUser {
			for _, attachment := range message.Attachments {
				data, contentType, err := readWebChatStoredAttachment(ctx, storage, attachment)
				if err != nil {
					return nil, err
				}
				switch attachment.Kind {
				case WebChatAttachmentKindImage:
					parts = append(parts, apicompat.ResponsesContentPart{Type: "input_image", ImageURL: webChatAttachmentDataURL(contentType, data)})
				case WebChatAttachmentKindFile:
					actualFileBytes += int64(len(data))
					if actualFileBytes > webChatMaxResponsesFileBytes {
						return nil, ErrWebChatUploadRejected
					}
					part := apicompat.ResponsesContentPart{
						Type: "input_file", FileData: webChatAttachmentDataURL(contentType, data),
						Filename: webChatResponsesFilename(attachment, contentType),
					}
					if contentType == "application/pdf" {
						part.Detail = "auto"
					}
					parts = append(parts, part)
				default:
					return nil, ErrWebChatUploadRejected
				}
			}
		}
		if len(parts) == 0 {
			continue
		}
		content, err := json.Marshal(parts)
		if err != nil {
			return nil, fmt.Errorf("marshal web chat Responses content: %w", err)
		}
		items = append(items, apicompat.ResponsesInputItem{Role: message.Role, Content: content})
	}
	return items, nil
}
```

This order preserves text followed by the stored attachment slice order. The declared-size preflight must complete before any storage open.

- [ ] **Step 4: Verify the OpenAI request and tool semantics**

The builder must create this request shape:

```go
store := false
request := apicompat.ResponsesRequest{
	Model: caps.Model,
	Input: inputJSON,
	Stream: stream,
	Include: []string{"reasoning.encrypted_content"},
	Store: &store,
}
```

For image generation use `json.RawMessage([]byte("{\"type\":\"image_generation\"}"))`. For searchable GPT models append one `web_search` tool and use `json.RawMessage([]byte("\"auto\""))`. The OpenAI builder must never read `payloadOptions.WebSearch`.

- [ ] **Step 5: Keep the compatibility builder non-OpenAI**

In `TestBuildWebChatResponsesPayload_IncludesWebSearchToolChoice`, use:

```go
caps := WebChatModelCapability{
	Provider: "anthropic", Platform: PlatformAnthropic, Model: "claude-sonnet-4",
	SupportsText: true, SupportsWebSearch: true,
}
```

Keep both assertions: enabled search produces the existing forced choice, disabled search produces `"auto"`. This protects the non-OpenAI compatibility path.

- [ ] **Step 6: Run GREEN verification and commit**

```bash
cd backend
gofmt -w internal/service/web_chat_openai_responses.go internal/service/web_chat_openai_responses_test.go internal/service/web_chat_adapter_test.go
go test ./internal/service -run 'Test(BuildOpenAIWebChatResponsesPayload|BuildWebChatResponsesPayload)' -count=1
go test ./internal/pkg/apicompat -count=1
cd ..
git add backend/internal/service/web_chat_openai_responses.go backend/internal/service/web_chat_openai_responses_test.go backend/internal/service/web_chat_adapter_test.go
git commit -m "feat(webchat): build native OpenAI Responses inputs"
```

Expected: PASS for mixed inputs, role mapping, declared/actual aggregate limits, PDF-only detail, automatic search, image generation, and existing Anthropic behavior.

---

### Task 4: Add the official file registry and byte validators

**Files:**
- Create: `backend/internal/service/web_chat_file_types.go`
- Create: `backend/internal/service/web_chat_file_types_test.go`
- Modify: `backend/internal/service/web_chat_service.go`
- Modify: `backend/internal/service/web_chat_storage_test.go`
- Modify: `backend/internal/service/web_chat_adapter.go`
- Modify: `backend/internal/service/web_chat_adapter_test.go`
- Modify: `backend/internal/service/web_chat_openai_responses.go`
- Modify: `backend/internal/service/web_chat_openai_responses_test.go`

**Interfaces:**
- Produces: `classifyWebChatUploadContentType(filename, rawContentType string, body []byte) (contentType, kind string, textPreviewEnabled bool, err error)`.
- Produces: `webChatUploadTypeForFilename(filename string) (webChatUploadType, bool)` and `webChatUploadType.AcceptsLegacyProvider bool` for Task 5.
- Consumes: `sanitizeWebChatDisplayFilename`, `webChatMaxUploadBytes`, and `ErrWebChatUploadRejected`.

- [ ] **Step 1: Write failing registry coverage tests**

Create `web_chat_file_types_test.go` with an explicit extension inventory:

```go
func TestWebChatUploadRegistryContainsOfficialLocalFormats(t *testing.T) {
	expected := []string{
		".pdf", ".doc", ".docx", ".dot", ".odt", ".rtf", ".pages",
		".pot", ".ppa", ".pps", ".ppt", ".pptx", ".pwz", ".wiz", ".key",
		".xla", ".xlb", ".xlc", ".xlm", ".xls", ".xlsx", ".xlt", ".xlw",
		".csv", ".tsv", ".iif",
		".asm", ".bat", ".c", ".cc", ".conf", ".cpp", ".css", ".cxx",
		".def", ".dic", ".eml", ".h", ".hh", ".htm", ".html", ".ics",
		".ifb", ".in", ".js", ".json", ".ksh", ".list", ".log", ".markdown",
		".md", ".mht", ".mhtml", ".mime", ".mjs", ".nws", ".pl", ".py",
		".rst", ".s", ".sql", ".srt", ".text", ".txt", ".vcf", ".vtt", ".xml",
		".ts", ".tsx", ".jsx", ".java", ".go", ".rs", ".scala", ".ps1",
		".diff", ".patch", ".php", ".rb", ".sh", ".bash", ".zsh", ".tex",
		".cs", ".kt", ".kts", ".swift", ".lua", ".r", ".jl", ".m", ".mm",
		".erl", ".ex", ".exs", ".hs", ".clj", ".cljs", ".cljc", ".groovy",
		".dart", ".awk", ".hbs", ".mustache", ".ejs", ".jinja", ".jinja2",
		".liquid", ".erb", ".twig", ".pug", ".jade", ".tmpl", ".cmake",
		".gradle", ".ini", ".properties", ".proto", ".scss", ".sass", ".less",
		".hcl", ".tf", ".toml", ".graphql", ".ndjson", ".json5", ".yaml",
		".yml", ".astro",
	}
	for _, extension := range expected {
		_, ok := webChatUploadTypeForFilename("attachment" + extension)
		require.Truef(t, ok, "missing registry extension %s", extension)
	}
	_, ok := webChatUploadTypeForFilename("Dockerfile")
	require.True(t, ok)
}
```

Run:

```bash
cd backend
go test ./internal/service -run '^TestWebChatUploadRegistryContainsOfficialLocalFormats$' -count=1
```

Expected: FAIL to compile because the registry lookup does not exist.

- [ ] **Step 2: Define the registry types and registration helpers**

Create `web_chat_file_types.go` with these focused types:

```go
package service

import (
	"archive/zip"
	"bytes"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type webChatUploadValidator uint8

const (
	webChatValidateImage webChatUploadValidator = iota + 1
	webChatValidatePDF
	webChatValidateText
	webChatValidateOLE
	webChatValidateRTF
	webChatValidateZIPEntries
	webChatValidateODT
	webChatValidatePages
	webChatValidateKeynote
)

type webChatUploadType struct {
	CanonicalContentType  string
	AcceptedContentTypes  []string
	Kind                  string
	TextPreviewEnabled    bool
	AcceptsLegacyProvider bool
	Validator             webChatUploadValidator
	RequiredZIPEntries    []string
}

var webChatUploadTypesByExtension = buildWebChatUploadTypesByExtension()
var webChatUploadTypesByFilename = map[string]webChatUploadType{
	"dockerfile": newWebChatTextUploadType("text/x-dockerfile", false),
}

func newWebChatUploadType(canonical string, aliases []string, kind string, preview, legacy bool, validator webChatUploadValidator, zipEntries ...string) webChatUploadType {
	accepted := append([]string{canonical}, aliases...)
	return webChatUploadType{
		CanonicalContentType: canonical, AcceptedContentTypes: accepted, Kind: kind,
		TextPreviewEnabled: preview, AcceptsLegacyProvider: legacy,
		Validator: validator, RequiredZIPEntries: zipEntries,
	}
}

func newWebChatTextUploadType(canonical string, legacy bool, aliases ...string) webChatUploadType {
	return newWebChatUploadType(canonical, aliases, WebChatAttachmentKindFile, true, legacy, webChatValidateText)
}
```

- [ ] **Step 3: Populate document, presentation, spreadsheet, image, and legacy entries**

In `buildWebChatUploadTypesByExtension`, register these exact groups:

```go
func buildWebChatUploadTypesByExtension() map[string]webChatUploadType {
	registry := make(map[string]webChatUploadType)
	register := func(extensions []string, fileType webChatUploadType) {
		for _, extension := range extensions {
			registry[extension] = fileType
		}
	}

	register([]string{".png"}, newWebChatUploadType("image/png", nil, WebChatAttachmentKindImage, false, true, webChatValidateImage))
	register([]string{".jpg", ".jpeg"}, newWebChatUploadType("image/jpeg", nil, WebChatAttachmentKindImage, false, true, webChatValidateImage))
	register([]string{".webp"}, newWebChatUploadType("image/webp", nil, WebChatAttachmentKindImage, false, true, webChatValidateImage))
	register([]string{".gif"}, newWebChatUploadType("image/gif", nil, WebChatAttachmentKindImage, false, true, webChatValidateImage))
	register([]string{".pdf"}, newWebChatUploadType("application/pdf", nil, WebChatAttachmentKindFile, false, true, webChatValidatePDF))

	register([]string{".docx"}, newWebChatUploadType(
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document", nil,
		WebChatAttachmentKindFile, false, true, webChatValidateZIPEntries,
		"[Content_Types].xml", "word/document.xml",
	))
	register([]string{".doc", ".dot"}, newWebChatUploadType(
		"application/msword", nil, WebChatAttachmentKindFile, false, false, webChatValidateOLE,
	))
	register([]string{".rtf"}, newWebChatUploadType(
		"application/rtf", []string{"text/rtf"}, WebChatAttachmentKindFile, false, false, webChatValidateRTF,
	))
	register([]string{".odt"}, newWebChatUploadType(
		"application/vnd.oasis.opendocument.text", nil, WebChatAttachmentKindFile, false, false, webChatValidateODT,
	))
	register([]string{".pages"}, newWebChatUploadType(
		"application/vnd.apple.pages", []string{"application/vnd.apple.iwork"},
		WebChatAttachmentKindFile, false, false, webChatValidatePages,
	))

	register([]string{".pptx"}, newWebChatUploadType(
		"application/vnd.openxmlformats-officedocument.presentationml.presentation", nil,
		WebChatAttachmentKindFile, false, false, webChatValidateZIPEntries,
		"[Content_Types].xml", "ppt/presentation.xml",
	))
	register([]string{".pot", ".ppa", ".pps", ".ppt", ".pwz", ".wiz"}, newWebChatUploadType(
		"application/vnd.ms-powerpoint", nil, WebChatAttachmentKindFile, false, false, webChatValidateOLE,
	))
	register([]string{".key"}, newWebChatUploadType(
		"application/vnd.apple.keynote", []string{"application/vnd.apple.iwork"},
		WebChatAttachmentKindFile, false, false, webChatValidateKeynote,
	))

	register([]string{".xlsx"}, newWebChatUploadType(
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", nil,
		WebChatAttachmentKindFile, false, false, webChatValidateZIPEntries,
		"[Content_Types].xml", "xl/workbook.xml",
	))
	register([]string{".xla", ".xlb", ".xlc", ".xlm", ".xls", ".xlt", ".xlw"}, newWebChatUploadType(
		"application/vnd.ms-excel", nil, WebChatAttachmentKindFile, false, false, webChatValidateOLE,
	))
	register([]string{".csv"}, newWebChatTextUploadType("text/csv", true, "application/csv"))
	register([]string{".tsv"}, newWebChatTextUploadType("text/tsv", false))
	register([]string{".iif"}, newWebChatTextUploadType("text/x-iif", false, "application/x-iif"))

	register([]string{".txt", ".text"}, newWebChatTextUploadType("text/plain", true))
	register([]string{".md", ".markdown"}, newWebChatTextUploadType("text/markdown", true, "text/plain"))
	register([]string{".json"}, newWebChatTextUploadType("application/json", true, "text/plain"))

	for extension, contentType := range webChatCodeContentTypeByExtension {
		aliases := append([]string{"text/plain"}, webChatCodeContentTypeAliasesByExtension[extension]...)
		register([]string{extension}, newWebChatTextUploadType(contentType, false, aliases...))
	}
	return registry
}
```

- [ ] **Step 4: Add the complete text/code extension map**

Use this explicit map; these MIME values come from the approved OpenAI file-input list:

```go
var webChatCodeContentTypeByExtension = map[string]string{
	".asm": "text/x-asm", ".bat": "text/x-shellscript", ".c": "text/x-c",
	".cc": "text/x-c++", ".conf": "text/plain", ".cpp": "text/x-c++",
	".css": "text/css", ".cxx": "text/x-c++", ".def": "text/plain",
	".dic": "text/plain", ".eml": "message/rfc822", ".h": "text/x-c",
	".hh": "text/x-c++", ".htm": "text/html", ".html": "text/html",
	".ics": "text/calendar", ".ifb": "text/calendar", ".in": "text/plain",
	".js": "text/javascript", ".ksh": "text/x-shellscript", ".list": "text/plain",
	".log": "text/plain", ".mht": "message/rfc822", ".mhtml": "message/rfc822",
	".mime": "message/rfc822", ".mjs": "text/javascript", ".nws": "text/plain",
	".pl": "text/x-perl", ".py": "text/x-python", ".rst": "text/x-rst",
	".s": "text/x-asm", ".sql": "text/x-sql", ".srt": "application/x-subrip",
	".vcf": "text/x-vcard", ".vtt": "text/vtt", ".xml": "text/xml",
	".ts": "text/x-typescript", ".tsx": "text/tsx", ".jsx": "text/jsx",
	".java": "text/x-java", ".go": "text/x-golang", ".rs": "text/x-rust",
	".scala": "application/x-scala", ".ps1": "application/x-powershell",
	".diff": "text/x-diff", ".patch": "text/x-patch", ".php": "text/x-php",
	".rb": "text/x-ruby", ".sh": "text/x-sh", ".bash": "text/x-bash",
	".zsh": "text/x-zsh", ".tex": "text/x-tex", ".cs": "text/x-csharp",
	".kt": "text/x-kotlin", ".kts": "text/x-kotlin", ".swift": "text/x-swift",
	".lua": "text/x-lua", ".r": "text/x-r", ".jl": "text/x-julia",
	".m": "text/x-objectivec", ".mm": "text/x-objectivec++", ".erl": "text/x-erlang",
	".ex": "text/x-elixir", ".exs": "text/x-elixir", ".hs": "text/x-haskell",
	".clj": "text/x-clojure", ".cljs": "text/x-clojure", ".cljc": "text/x-clojure",
	".groovy": "text/x-groovy", ".dart": "text/x-dart", ".awk": "application/x-awk",
	".hbs": "text/x-handlebars", ".mustache": "text/x-mustache", ".ejs": "text/x-ejs",
	".jinja": "text/x-jinja2", ".jinja2": "text/x-jinja2", ".liquid": "text/x-liquid",
	".erb": "text/x-erb", ".twig": "text/x-twig", ".pug": "text/x-pug",
	".jade": "text/x-jade", ".tmpl": "text/x-tmpl", ".cmake": "text/x-cmake",
	".gradle": "text/x-gradle", ".ini": "text/x-ini", ".properties": "text/x-properties",
	".proto": "text/x-protobuf", ".scss": "text/x-scss", ".sass": "text/x-sass",
	".less": "text/x-less", ".hcl": "text/x-hcl", ".tf": "text/x-terraform",
	".toml": "application/toml", ".graphql": "application/graphql",
	".ndjson": "application/x-ndjson", ".json5": "application/json5",
	".yaml": "application/yaml", ".yml": "application/yaml", ".astro": "text/x-astro",
}

var webChatCodeContentTypeAliasesByExtension = map[string][]string{
	".js": {"application/javascript"},
	".ts": {"application/typescript"},
	".rs": {"application/x-rust"},
	".scala": {"text/x-scala"},
	".patch": {"application/x-patch"},
	".php": {"application/x-php", "application/x-httpd-php", "application/x-httpd-php-source"},
	".sh": {"text/x-shellscript"},
	".bash": {"application/x-bash", "text/x-shellscript"},
	".sql": {"application/x-sql"},
	".r": {"text/x-R"},
	".awk": {"text/x-awk"},
	".proto": {"application/x-protobuf"},
	".tf": {"application/x-terraform"},
	".toml": {"application/x-toml", "text/x-toml"},
	".graphql": {"application/x-graphql", "text/x-graphql"},
	".json5": {"application/x-json5"},
	".yaml": {"application/x-yaml", "text/x-yaml"},
	".yml": {"application/x-yaml", "text/x-yaml"},
	".srt": {"text/srt", "text/x-subrip"},
}
```

- [ ] **Step 5: Implement filename/MIME lookup and classification**

Move `classifyWebChatUploadContentType`, `isGenericWebChatUploadContentType`, and `webChatBodyLooksText` from `web_chat_service.go` into the new file and use this signature:

```go
func webChatUploadTypeForFilename(filename string) (webChatUploadType, bool) {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(filename)))
	if fileType, ok := webChatUploadTypesByFilename[base]; ok {
		return fileType, true
	}
	fileType, ok := webChatUploadTypesByExtension[strings.ToLower(filepath.Ext(base))]
	return fileType, ok
}

func (fileType webChatUploadType) acceptsContentType(contentType string) bool {
	for _, accepted := range fileType.AcceptedContentTypes {
		if strings.EqualFold(strings.TrimSpace(accepted), contentType) {
			return true
		}
	}
	return false
}

func isGenericWebChatUploadContentType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "", "application/octet-stream", "binary/octet-stream":
		return true
	default:
		return false
	}
}

func webChatBodyLooksText(body []byte) bool {
	return utf8.Valid(body) && !bytes.ContainsRune(body, '\x00')
}

func classifyWebChatUploadContentType(filename, rawContentType string, body []byte) (string, string, bool, error) {
	fileType, ok := webChatUploadTypeForFilename(filename)
	if !ok {
		return "", "", false, ErrWebChatUploadRejected
	}
	contentType, _, err := mime.ParseMediaType(strings.TrimSpace(rawContentType))
	if err != nil && strings.TrimSpace(rawContentType) != "" {
		return "", "", false, ErrWebChatUploadRejected
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if contentType != "" && !isGenericWebChatUploadContentType(contentType) && !fileType.acceptsContentType(contentType) {
		return "", "", false, ErrWebChatUploadRejected
	}
	if !webChatUploadBytesMatchType(fileType, body) {
		return "", "", false, ErrWebChatUploadRejected
	}
	return fileType.CanonicalContentType, fileType.Kind, fileType.TextPreviewEnabled, nil
}
```

Images no longer rely on a caller-provided MIME alone; the validator compares `http.DetectContentType(body)` with the registry canonical MIME.

- [ ] **Step 6: Implement bounded container and byte validators**

Add these exact validator rules:

```go
var webChatOLEMagic = []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}

func webChatUploadBytesMatchType(fileType webChatUploadType, body []byte) bool {
	switch fileType.Validator {
	case webChatValidateImage:
		detected := strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(body), ";", 2)[0]))
		return detected == fileType.CanonicalContentType
	case webChatValidatePDF:
		return bytes.HasPrefix(body, []byte("%PDF-"))
	case webChatValidateText:
		return webChatBodyLooksText(body)
	case webChatValidateOLE:
		return bytes.HasPrefix(body, webChatOLEMagic)
	case webChatValidateRTF:
		return bytes.HasPrefix(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf}), []byte("{\\rtf"))
	case webChatValidateZIPEntries:
		return webChatZIPContainsEntries(body, fileType.RequiredZIPEntries...)
	case webChatValidateODT:
		return webChatZIPSmallEntryEquals(body, "mimetype", "application/vnd.oasis.opendocument.text")
	case webChatValidatePages:
		return webChatZIPContainsEntries(body, "Index/Document.iwa")
	case webChatValidateKeynote:
		return webChatZIPContainsEntries(body, "Index/Slide.iwa")
	default:
		return false
	}
}

func webChatZIPContainsEntries(body []byte, required ...string) bool {
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return false
	}
	found := make(map[string]bool, len(required))
	for _, file := range archive.File {
		for _, name := range required {
			if file.Name == name {
				found[name] = true
			}
		}
	}
	for _, name := range required {
		if !found[name] {
			return false
		}
	}
	return true
}

func webChatZIPSmallEntryEquals(body []byte, entryName, expected string) bool {
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return false
	}
	for _, file := range archive.File {
		if file.Name != entryName || file.UncompressedSize64 > 256 {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return false
		}
		value, readErr := io.ReadAll(io.LimitReader(reader, 257))
		closeErr := reader.Close()
		return readErr == nil && closeErr == nil && string(value) == expected
	}
	return false
}
```

Do not open macros, embedded objects, or archive members other than the bounded ODT `mimetype` entry.

- [ ] **Step 7: Update every classifier call with filename and real bytes**

Use these exact call shapes:

```go
// uploadAttachmentFromReader
contentType, kind, textPreviewEnabled, err := classifyWebChatUploadContentType(in.Filename, in.ContentType, body)

// readWebChatStoredAttachment
contentType, kind, _, err := classifyWebChatUploadContentType(attachment.Filename, attachment.ContentType, data)

// buildWebChatImageDataURL, after the bounded read
contentType, kind, _, err := classifyWebChatUploadContentType(attachment.Filename, attachment.ContentType, data)
```

Delete `webChatStoredContentMatchesType`; the centralized validator replaces it. Remove now-unused `archive/zip`, `bytes`, and `net/http` imports from `web_chat_openai_responses.go`, and now-unused `mime`/`net/http` imports from `web_chat_service.go`.

- [ ] **Step 8: Add positive and negative byte-validation tests**

Use table-driven tests with representative fixtures:

```go
func TestClassifyWebChatUploadContentType_AcceptsSupportedContainersAndCode(t *testing.T) {
	ole := append(append([]byte(nil), webChatOLEMagic...), []byte("payload")...)
	cases := []struct {
		name, filename, declared, wantType string
		body []byte
	}{
		{"pdf", "paper.pdf", "application/pdf", "application/pdf", []byte("%PDF-1.7\n%%EOF")},
		{"pptx", "slides.pptx", "application/octet-stream", "application/vnd.openxmlformats-officedocument.presentationml.presentation", testWebChatZIP(t, map[string]string{"[Content_Types].xml": "<Types/>", "ppt/presentation.xml": "<p:presentation/>"})},
		{"xlsx", "sheet.xlsx", "", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", testWebChatZIP(t, map[string]string{"[Content_Types].xml": "<Types/>", "xl/workbook.xml": "<workbook/>"})},
		{"legacy ppt", "slides.ppt", "application/vnd.ms-powerpoint", "application/vnd.ms-powerpoint", ole},
		{"odt", "notes.odt", "application/vnd.oasis.opendocument.text", "application/vnd.oasis.opendocument.text", testWebChatZIP(t, map[string]string{"mimetype": "application/vnd.oasis.opendocument.text"})},
		{"rtf", "notes.rtf", "text/rtf", "application/rtf", []byte("{\\rtf1 hello}")},
		{"python", "script.py", "text/plain", "text/x-python", []byte("print('ok')\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, kind, _, err := classifyWebChatUploadContentType(tc.filename, tc.declared, tc.body)
			require.NoError(t, err)
			require.Equal(t, WebChatAttachmentKindFile, kind)
			require.Equal(t, tc.wantType, got)
		})
	}
}
```

Add the exact rejection table:

```go
func TestClassifyWebChatUploadContentType_RejectsUnknownCorruptAndMismatchedFiles(t *testing.T) {
	cases := []struct {
		name, filename, declared string
		body []byte
	}{
		{"archive", "archive.zip", "application/zip", []byte("PK\x03\x04")},
		{"executable", "program.exe", "application/octet-stream", []byte("MZ")},
		{"audio", "sample.mp3", "audio/mpeg", []byte("ID3")},
		{"broken pptx", "slides.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", testWebChatZIP(t, map[string]string{"[Content_Types].xml": "<Types/>"})},
		{"fake pdf", "paper.pdf", "application/pdf", []byte("not a pdf")},
		{"nul code", "script.py", "text/plain", []byte{'p', 0, 'y'}},
		{"mime mismatch", "script.py", "application/pdf", []byte("print('ok')")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contentType, kind, preview, err := classifyWebChatUploadContentType(tc.filename, tc.declared, tc.body)
			require.ErrorIs(t, err, ErrWebChatUploadRejected)
			require.Empty(t, contentType)
			require.Empty(t, kind)
			require.False(t, preview)
		})
	}
}
```

Add this shared test fixture next to the existing DOCX helper and reuse it from both test files:

```go
func testWebChatZIP(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, body := range entries {
		entry, err := archive.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	return buffer.Bytes()
}

func testWebChatDOCX(t *testing.T) []byte {
	t.Helper()
	return testWebChatZIP(t, map[string]string{
		"[Content_Types].xml": "<Types/>",
		"word/document.xml":   "<w:document/>",
	})
}
```

- [ ] **Step 9: Cover size/error identity and generic MIME upload behavior**

Extend the shared fake storage with `openErrors map[string]error`. In `Open`, validate and append the expected key first, then return the configured error before looking up bytes:

```go
if openErr := s.openErrors[key]; openErr != nil {
	return nil, WebChatStoredFileMeta{}, openErr
}
```

Add these focused tests to `web_chat_openai_responses_test.go`:

```go
func TestReadWebChatStoredAttachmentRejectsDeclaredSizeBeforeOpen(t *testing.T) {
	storage := fakeWebChatStorageWithoutOpens(t)
	data, contentType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile, Filename: "paper.pdf", ContentType: "application/pdf",
		SizeBytes: webChatMaxUploadBytes + 1, StorageKey: "paper.pdf",
	})
	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Nil(t, data)
	require.Empty(t, contentType)
	storage.requireOpened()
}

func TestReadWebChatStoredAttachmentRejectsMetadataAndActualSize(t *testing.T) {
	pdf := []byte("%PDF-1.7\n%%EOF")
	actualOversize := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte{'x'}, webChatMaxUploadBytes)...)
	cases := []struct {
		name string
		data []byte
		metaSize int64
	}{
		{"metadata", pdf, webChatMaxUploadBytes + 1},
		{"actual", actualOversize, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage := fakeWebChatStorageWithFileMeta(t, "paper.pdf", tc.data, tc.metaSize)
			data, contentType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
				Kind: WebChatAttachmentKindFile, Filename: "paper.pdf", ContentType: "application/pdf", StorageKey: "paper.pdf",
			})
			require.ErrorIs(t, err, ErrWebChatUploadRejected)
			require.Nil(t, data)
			require.Empty(t, contentType)
			storage.requireOpened("paper.pdf")
		})
	}
}

func TestReadWebChatStoredAttachmentWrapsOpenError(t *testing.T) {
	storage := &fakeWebChatStorage{
		t: t, expectedKeys: []string{"paper.pdf"},
		openErrors: map[string]error{"paper.pdf": errors.New("storage unavailable")},
	}
	data, contentType, err := readWebChatStoredAttachment(context.Background(), storage, WebChatAttachment{
		Kind: WebChatAttachmentKindFile, Filename: "paper.pdf", ContentType: "application/pdf", StorageKey: "paper.pdf",
	})
	require.ErrorIs(t, err, ErrWebChatUploadRejected)
	require.Nil(t, data)
	require.Empty(t, contentType)
	storage.requireOpened("paper.pdf")
}
```

Add `errors` to the test imports. Add filenames to the existing `TestReadWebChatStoredAttachmentAcceptsPDFAndDOCX` attachments because registry lookup now requires them.

Extend `web_chat_storage_test.go`:

```go
func TestWebChatService_UploadAttachmentUsesExtensionForGenericCodeMIME(t *testing.T) {
	repo := &webChatUploadRepoStub{}
	storage := NewLocalWebChatStorage(t.TempDir())
	svc := &WebChatService{attachmentRepo: repo, storage: storage}
	attachment, err := svc.uploadAttachmentFromReader(context.Background(), UploadWebChatAttachmentInput{
		UserID: 42, Filename: "script.py", ContentType: "application/octet-stream",
		Reader: strings.NewReader("print('ok')\n"),
	})
	require.NoError(t, err)
	require.Equal(t, "text/x-python", attachment.ContentType)
	require.NotNil(t, attachment.TextPreview)
	require.Len(t, repo.created, 1)
	require.Equal(t, attachment.ID, repo.created[0].ID)
}
```

Update the three existing image-upload tests to use a valid signature rather than `"png-data"` or `"png"`:

```go
Reader: strings.NewReader(string([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})),
```

Rename `TestWebChatService_UploadAttachmentRejectsUnsupportedMIME` to `TestWebChatService_UploadAttachmentRejectsUnknownArchive` and use:

```go
Filename: "archive.zip",
ContentType: "application/zip",
Reader: strings.NewReader("PK\x03\x04"),
```

Shell and Python files are now valid OpenAI text inputs.

- [ ] **Step 10: Verify and commit**

```bash
cd backend
gofmt -w internal/service/web_chat_file_types.go internal/service/web_chat_file_types_test.go internal/service/web_chat_service.go internal/service/web_chat_storage_test.go internal/service/web_chat_adapter.go internal/service/web_chat_adapter_test.go internal/service/web_chat_openai_responses.go internal/service/web_chat_openai_responses_test.go
go test ./internal/service -run 'Test(WebChatUploadRegistry|ClassifyWebChatUploadContentType|WebChatService_UploadAttachment|ReadWebChatStoredAttachment|BuildWebChatCompletionsPayload)' -count=1
go test ./internal/pkg/apicompat -count=1
cd ..
git add backend/internal/service/web_chat_file_types.go backend/internal/service/web_chat_file_types_test.go backend/internal/service/web_chat_service.go backend/internal/service/web_chat_storage_test.go backend/internal/service/web_chat_adapter.go backend/internal/service/web_chat_adapter_test.go backend/internal/service/web_chat_openai_responses.go backend/internal/service/web_chat_openai_responses_test.go
git commit -m "feat(webchat): accept OpenAI supported file inputs"
```

Expected: every registered format is discoverable, representative byte validators pass, corrupt/mismatched files fail before storage/forwarding, and previous upload/adapter tests remain green.

---

### Task 5: Enforce provider isolation without silent attachment loss

**Files:**
- Modify: `backend/internal/service/web_chat_file_types.go`
- Modify: `backend/internal/service/web_chat_file_types_test.go`
- Modify: `backend/internal/service/web_chat_adapter.go`
- Modify: `backend/internal/service/web_chat_adapter_test.go`
- Modify: `backend/internal/service/web_chat_openai_responses_test.go`

**Interfaces:**
- Consumes: `webChatUploadTypeForFilename` and `webChatUploadType.AcceptsLegacyProvider`.
- Produces: `webChatAttachmentAllowedForProvider(provider string, attachment WebChatAttachment) bool`.

- [ ] **Step 1: Add failing OpenAI/Anthropic isolation tests**

Add to `web_chat_adapter_test.go`:

```go
func TestBuildWebChatCompletionsPayload_RejectsOpenAIOnlyFileForAnthropic(t *testing.T) {
	payload, err := BuildWebChatCompletionsPayload(context.Background(), fakeWebChatStorageWithoutOpens(t), WebChatModelCapability{
		Provider: "anthropic", Platform: PlatformAnthropic, Model: "claude-sonnet-4",
		SupportsFileContext: true,
	}, []WebChatMessage{{Role: WebChatRoleUser, Attachments: []WebChatAttachment{{
		Kind: WebChatAttachmentKindFile, Filename: "slides.pptx",
		ContentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	}}}}, false)

	require.ErrorIs(t, err, ErrWebChatUnsupportedContext)
	require.Nil(t, payload)
}

func TestBuildWebChatCompletionsPayload_KeepsLegacyDOCXForAnthropic(t *testing.T) {
	payload, err := BuildWebChatCompletionsPayload(context.Background(), fakeWebChatStorageWithoutOpens(t), WebChatModelCapability{
		Provider: "anthropic", Platform: PlatformAnthropic, Model: "claude-sonnet-4",
		SupportsFileContext: true,
	}, []WebChatMessage{{Role: WebChatRoleUser, ContentText: "Summarize", Attachments: []WebChatAttachment{{
		Kind: WebChatAttachmentKindFile, Filename: "notes.docx",
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	}}}}, false)

	require.NoError(t, err)
	require.Contains(t, string(payload), "Summarize")
}
```

Run:

```bash
cd backend
go test ./internal/service -run 'TestBuildWebChatCompletionsPayload_(RejectsOpenAIOnlyFileForAnthropic|KeepsLegacyDOCXForAnthropic)$' -count=1
```

Expected: the first test FAILS because capability validation currently counts only file quantity, not provider compatibility.

- [ ] **Step 2: Implement provider-aware metadata validation**

Add to `web_chat_file_types.go`:

```go
func webChatAttachmentAllowedForProvider(provider string, attachment WebChatAttachment) bool {
	if attachment.Kind == WebChatAttachmentKindImage {
		return true
	}
	if attachment.Kind != WebChatAttachmentKindFile {
		return false
	}
	fileType, ok := webChatUploadTypeForFilename(attachment.Filename)
	if !ok || !fileType.acceptsContentType(strings.ToLower(strings.TrimSpace(attachment.ContentType))) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(provider), "openai") {
		return true
	}
	return fileType.AcceptsLegacyProvider
}
```

In `validateWebChatAdapterContext`, reject incompatible metadata before summary validation:

```go
case WebChatAttachmentKindFile:
	if !webChatAttachmentAllowedForProvider(caps.Provider, attachment) {
		return fmt.Errorf("%w: file %s is not supported by provider %s", ErrWebChatUnsupportedContext, webChatAttachmentDisplayName(attachment), caps.Provider)
	}
	summary.FileAttachmentCount++
```

Do not read storage in this metadata preflight. OpenAI's later `readWebChatStoredAttachment` remains the real-byte security boundary.

- [ ] **Step 3: Verify new formats reach native OpenAI input**

Add table-driven cases to `web_chat_openai_responses_test.go` for a PPTX and a Python file. Use `testWebChatZIP` for PPTX and plain UTF-8 bytes for Python. For each payload assert:

```go
require.Equal(t, "input_file", gjson.GetBytes(payload, "input.0.content.0.type").String())
require.False(t, gjson.GetBytes(payload, "input.0.content.0.detail").Exists())
require.NotEmpty(t, gjson.GetBytes(payload, "input.0.content.0.file_data").String())
```

The PPTX data URL must begin with `data:application/vnd.openxmlformats-officedocument.presentationml.presentation;base64,`; the Python data URL must begin with `data:text/x-python;base64,`.

- [ ] **Step 4: Verify and commit**

```bash
cd backend
gofmt -w internal/service/web_chat_file_types.go internal/service/web_chat_file_types_test.go internal/service/web_chat_adapter.go internal/service/web_chat_adapter_test.go internal/service/web_chat_openai_responses_test.go
go test ./internal/service -run 'Test(BuildWebChatCompletionsPayload|BuildOpenAIWebChatResponsesPayload|WebChatAttachmentAllowedForProvider)' -count=1
cd ..
git add backend/internal/service/web_chat_file_types.go backend/internal/service/web_chat_file_types_test.go backend/internal/service/web_chat_adapter.go backend/internal/service/web_chat_adapter_test.go backend/internal/service/web_chat_openai_responses_test.go
git commit -m "fix(webchat): isolate OpenAI-only file inputs"
```

Expected: OpenAI accepts newly registered formats as native files, Anthropic rejects them explicitly, and legacy DOCX/text behavior remains unchanged.

---

### Task 6: Route every OpenAI WebChat message through Responses

**Files:**
- Modify: `backend/internal/service/web_chat_dispatch.go`
- Modify: `backend/internal/service/web_chat_service_test.go`
- Modify: `backend/internal/service/openai_oauth_passthrough_test.go`

**Interfaces:**
- Consumes: `BuildOpenAIWebChatResponsesPayload`.
- Produces: `webChatUseResponsesPayload(input webChatDispatchInput) bool`, true for provider `openai` regardless of content or legacy search config.

- [ ] **Step 1: Add failing routing regressions**

Rename the OpenAI search test to `TestWebChatSend_OpenAIAlwaysUsesResponsesWithAutomaticSearch`, omit `WebSearch` from its input, and assert:

```go
requireOrderedEvents(t, svc.events, "forward_openai_responses", "record_openai_usage", "usage_lookup")
require.Equal(t, "/v1/responses", svc.openAIRecordUsageInput.UpstreamEndpoint)
require.Equal(t, "web_search", gjson.GetBytes(svc.forwardedBody, "tools.0.type").String())
require.Equal(t, "auto", gjson.GetBytes(svc.forwardedBody, "tool_choice").String())
require.Equal(t, "search today's AI news", gjson.GetBytes(svc.forwardedBody, "input.1.content.0.text").String())
```

Add:

```go
func TestWebChatSend_OpenAIIgnoresLegacyDisabledSearchConfig(t *testing.T) {
	svc := newWebChatServiceWithStubs(t)
	user := &User{ID: 42, AllowedGroups: []int64{11}, SubscriptionBalanceFallbackEnabled: true}
	svc.availableGroups = []Group{{ID: 11, Platform: PlatformOpenAI, Status: StatusActive}}
	svc.openAIForwardResult = &OpenAIForwardResult{
		RequestID: "openai_req", Model: "gpt-5.5", UpstreamModel: "gpt-5.5", Stream: true,
	}

	_, err := svc.SendMessage(newTestGinContext(context.Background()), WebChatSendInput{
		UserID: 42, User: user, ConversationID: 7,
		Model: "gpt-5.5", Provider: "openai", Text: "answer", Stream: true,
		WebSearch: WebChatWebSearchConfig{Configured: true, Enabled: false},
		GinContext: newTestGinContext(context.Background()),
	})

	require.NoError(t, err)
	require.Equal(t, "web_search", gjson.GetBytes(svc.forwardedBody, "tools.0.type").String())
	require.Equal(t, "auto", gjson.GetBytes(svc.forwardedBody, "tool_choice").String())
}
```

Update `TestWebChatSend_SavesOpenAIImageResultsAsArtifacts` to require the Responses forwarder, `/v1/responses`, `image_generation` tool choice, and no `web_search`.

Run:

```bash
cd backend
go test ./internal/service -run 'TestWebChatSend_(OpenAI|SavesOpenAIImageResults)' -count=1
```

Expected: FAIL because unconfigured OpenAI text still selects Chat Completions.

- [ ] **Step 2: Select the native builder only for provider OpenAI**

Add `strings` to `web_chat_dispatch.go` and use:

```go
useResponsesPayload := webChatUseResponsesPayload(input)
var body []byte
switch {
case strings.EqualFold(strings.TrimSpace(input.Capabilities.Provider), "openai"):
	body, err = BuildOpenAIWebChatResponsesPayload(ctx, s.storage, input.Capabilities, input.Messages, input.Stream, payloadOptions)
case useResponsesPayload:
	body, err = BuildWebChatResponsesPayload(ctx, s.storage, input.Capabilities, input.Messages, input.Stream, payloadOptions)
default:
	body, err = BuildWebChatCompletionsPayload(ctx, s.storage, input.Capabilities, input.Messages, input.Stream, payloadOptions)
}
```

Replace the predicate with:

```go
func webChatUseResponsesPayload(input webChatDispatchInput) bool {
	if strings.EqualFold(strings.TrimSpace(input.Capabilities.Provider), "openai") {
		return true
	}
	if !input.Capabilities.SupportsWebSearch {
		return false
	}
	if !input.WebSearch.Configured && !input.WebSearch.Enabled {
		return false
	}
	return input.Capabilities.Platform == PlatformAnthropic
}
```

Do not remove `forwardWebChatOpenAI` or public Chat Completions compatibility code.

- [ ] **Step 3: Prove the existing API Key and OAuth gateways preserve native files**

Add this regression to `openai_oauth_passthrough_test.go`. It uses the existing gateway, request transformation, account-type selection, and HTTP recorder without making a network request:

```go
func TestOpenAIGatewayService_ResponsesPreservesNativeFileForAPIKeyAndOAuth(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"store":false,"input":[{"role":"user","content":[{"type":"input_file","filename":"paper.pdf","file_data":"data:application/pdf;base64,JVBERi0xLjcKJSVFT0Y=","detail":"auto"}]}],"tools":[{"type":"web_search"}],"tool_choice":"auto"}`)
	cases := []struct {
		name, wantURL string
		account       *Account
	}{
		{
			name: "api key", wantURL: "http://upstream.example/v1/responses",
			account: &Account{
				ID: 701, Name: "openai-apikey", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test", "base_url": "http://upstream.example"},
				Extra: map[string]any{
					openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeAuto),
					openai_compat.ExtraKeyResponsesSupported: true,
				},
				Status: StatusActive, Schedulable: true,
			},
		},
		{
			name: "oauth", wantURL: chatgptCodexURL,
			account: &Account{
				ID: 702, Name: "openai-oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
				Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
				Status: StatusActive, Schedulable: true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"stop after capture"}}`)),
			}}
			svc := &OpenAIGatewayService{
				cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
					Enabled: false, AllowInsecureHTTP: true,
				}}},
				httpUpstream: upstream,
			}

			result, err := svc.Forward(context.Background(), c, tc.account, body)
			require.Error(t, err)
			require.Nil(t, result)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, tc.wantURL, upstream.lastReq.URL.String())
			require.Equal(t, "input_file", gjson.GetBytes(upstream.lastBody, "input.0.content.0.type").String())
			require.Equal(t, "paper.pdf", gjson.GetBytes(upstream.lastBody, "input.0.content.0.filename").String())
			require.Equal(t, "data:application/pdf;base64,JVBERi0xLjcKJSVFT0Y=", gjson.GetBytes(upstream.lastBody, "input.0.content.0.file_data").String())
			require.Equal(t, "auto", gjson.GetBytes(upstream.lastBody, "input.0.content.0.detail").String())
			require.Equal(t, "web_search", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
			require.Equal(t, "auto", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
			require.False(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
		})
	}
}
```

If the OAuth subtest rejects or deletes `input_file` locally, fix only the existing OAuth Responses transformation needed to preserve the official field shape. Do not send a real provider request in this task.

- [ ] **Step 4: Make service attachment fixtures contain valid bytes**

Use an actual PNG signature in `webChatStorageStub.Open`:

```go
func (s webChatStorageStub) Open(context.Context, string) (io.ReadCloser, WebChatStoredFileMeta, error) {
	data := []byte("\x89PNG\r\n\x1a\n")
	return io.NopCloser(bytes.NewReader(data)), WebChatStoredFileMeta{SizeBytes: int64(len(data))}, nil
}
```

Add `bytes` to the test imports. Ensure image attachment fixtures have filename `image.png`, because registry validation now checks extension.

- [ ] **Step 5: Verify and commit**

```bash
cd backend
gofmt -w internal/service/web_chat_dispatch.go internal/service/web_chat_service_test.go internal/service/openai_oauth_passthrough_test.go
go test ./internal/service -run 'TestWebChatSend_(OpenAI|AnthropicWebSearch|SavesOpenAIImageResults|UsesCreateMessageAttachments)' -count=1
go test ./internal/service -run '^TestOpenAIGatewayService_ResponsesPreservesNativeFileForAPIKeyAndOAuth$' -count=1
go test ./internal/service -count=1
cd ..
git add backend/internal/service/web_chat_dispatch.go backend/internal/service/web_chat_service_test.go backend/internal/service/openai_oauth_passthrough_test.go
git commit -m "fix(webchat): route OpenAI turns through Responses"
```

Expected: OpenAI calls only the Responses forwarder; the existing API Key and OAuth gateways preserve native `input_file`, PDF detail, automatic search, and `store: false`; configured Anthropic search still uses its compatibility path; ordinary Anthropic behavior remains unchanged.

---

### Task 7: Remove GPT search controls and unify attachment upload

**Files:**
- Create: `frontend/src/utils/webChatAttachmentAccept.ts`
- Create: `frontend/src/utils/__tests__/webChatAttachmentAccept.spec.ts`
- Modify: `frontend/src/stores/chat.ts`
- Modify: `frontend/src/components/chat/Composer.vue`
- Modify: `frontend/src/components/chat/__tests__/chatStore.spec.ts`
- Modify: `frontend/src/components/chat/__tests__/ChatView.spec.ts`
- Modify: `Makefile`

**Interfaces:**
- Produces: `webChatAttachmentAccept(provider?: string): string`.
- Produces: Pinia computed `selectedModelHasConfigurableWebSearch`, true only for searchable non-OpenAI models.

- [ ] **Step 1: Write failing provider-aware search request tests**

In `chatStore.spec.ts`, import `WebChatConversationDetail`, add an Anthropic searchable model, and add:

```ts
function emptyWebSearchConversation(model: WebChatModel): WebChatConversationDetail {
  return {
    conversation: {
      id: 7,
      title: 'Search',
      default_model: model.model,
      default_provider: model.provider,
      last_model: model.model,
      last_provider: model.provider,
      status: 'active',
      message_count: 0,
      created_at: '2026-06-22T00:00:00Z',
      updated_at: '2026-06-22T00:00:00Z',
    },
    messages: [],
  }
}

function mockCompletedWebChatStream() {
  return vi.spyOn(chatAPI, 'sendMessageStream').mockResolvedValue({
    response: new Response('data: [DONE]\n\n', {
      status: 200,
      headers: {
        'X-Web-Chat-User-Message-ID': '100',
        'X-Web-Chat-Assistant-Message-ID': '101',
      },
    }),
    userMessageId: 100,
    assistantMessageId: 101,
  })
}
```

Replace the existing `keeps web search off by default and only sends config when enabled` test with these two tests:

```ts
it('never sends a frontend search config for OpenAI', async () => {
  const streamSpy = mockCompletedWebChatStream()
  const store = useChatStore()
  store.selectedModel = webSearchModel
  store.currentConversation = emptyWebSearchConversation(webSearchModel)
  store.webSearchEnabled = true

  await store.sendMessage('Use server-managed search')

  expect(streamSpy.mock.calls[0][1]).not.toHaveProperty('web_search')
})

it('keeps configurable search for a non-OpenAI model', async () => {
  const model: WebChatModel = {
    ...webSearchModel,
    provider: 'anthropic', platform: 'anthropic', key_type: 'anthropic',
    model: 'claude-sonnet-4', display_name: 'Claude Sonnet 4',
  }
  const streamSpy = mockCompletedWebChatStream()
  const store = useChatStore()
  store.selectedModel = model
  store.currentConversation = emptyWebSearchConversation(model)
  store.webSearchEnabled = true

  await store.sendMessage('Search with Claude')

  expect(streamSpy.mock.calls[0][1]).toMatchObject({
    model: 'claude-sonnet-4', provider: 'anthropic', web_search: { enabled: true },
  })
})
```

- [ ] **Step 2: Write failing one-button Composer tests**

Import `Icon` in `ChatView.spec.ts` and add:

```ts
it('renders one upload entry with the upload icon for GPT', () => {
  const store = useChatStore()
  store.selectedModel = chatModel

  const wrapper = mount(Composer)
  const button = wrapper.get('[data-testid="chat-attachment-upload"]')

  expect(wrapper.findAll('[data-testid="chat-attachment-upload"]')).toHaveLength(1)
  expect(button.findComponent(Icon).props('name')).toBe('upload')
  expect(button.attributes('aria-label')).toBe('Upload file')
  expect(wrapper.findAll('input[type="file"]')).toHaveLength(1)
})

it('uses provider-aware attachment accept hints', async () => {
  const store = useChatStore()
  store.selectedModel = chatModel
  const wrapper = mount(Composer)
  const input = wrapper.get('[data-testid="chat-attachment-input"]')

  expect(input.attributes('accept')).toContain('.pptx')
  expect(input.attributes('accept')).toContain('.py')

  store.selectedModel = anthropicModel
  await wrapper.vm.$nextTick()

  expect(input.attributes('accept')).toContain('.docx')
  expect(input.attributes('accept')).not.toContain('.pptx')
  expect(input.attributes('accept')).not.toContain('.py')
})

it.each([
  ['image', 'diagram.png', 'image/png'],
  ['document', 'slides.pptx', 'application/vnd.openxmlformats-officedocument.presentationml.presentation'],
])('uploads a selected %s through the single attachment input', async (_kind, filename, contentType) => {
  const store = useChatStore()
  store.selectedModel = chatModel
  const uploadSpy = vi.spyOn(store, 'uploadAttachment').mockResolvedValue({} as WebChatAttachment)
  const wrapper = mount(Composer)
  const input = wrapper.get('[data-testid="chat-attachment-input"]')
  const file = new File(['content'], filename, { type: contentType })
  Object.defineProperty(input.element, 'files', { configurable: true, value: [file] })

  await input.trigger('change')

  expect(uploadSpy).toHaveBeenCalledWith(file)
})
```

Add `WebChatAttachment` to the existing `@/api/chat` type imports for this test.

Add GPT/no-copy and non-OpenAI search-control tests:

```ts
it('does not render a web search control or explanation for GPT', async () => {
  const store = useChatStore()
  store.selectedModel = chatModel
  const wrapper = mount(Composer)
  await wrapper.get('[data-testid="chat-options-toggle"]').trigger('click')
  expect(wrapper.find('[data-testid="chat-web-search-toggle"]').exists()).toBe(false)
  expect(wrapper.text()).not.toContain('Web search')
  expect(wrapper.text()).not.toContain('联网')
})

it('keeps the web search control for a searchable non-OpenAI model', async () => {
  const store = useChatStore()
  store.selectedModel = { ...anthropicModel, supports_web_search: true }
  const wrapper = mount(Composer)
  await wrapper.get('[data-testid="chat-options-toggle"]').trigger('click')
  expect(wrapper.get('[data-testid="chat-web-search-toggle"]').exists()).toBe(true)
})
```

Update the existing `omits secondary status text for thinking and web search toggles` test so it exercises the preserved non-OpenAI control:

```ts
const store = useChatStore()
store.selectedModel = { ...anthropicModel, supports_web_search: true }
const wrapper = mount(Composer)
await wrapper.get('[data-testid="chat-options-toggle"]').trigger('click')
expect(wrapper.get('[data-testid="chat-thinking-toggle"]').exists()).toBe(true)
expect(wrapper.get('[data-testid="chat-web-search-toggle"]').exists()).toBe(true)
expect(wrapper.text()).not.toContain('关闭')
expect(wrapper.text()).not.toContain('强制搜索')
expect(wrapper.text()).not.toContain('使用该模型最高思考档位')
```

Run:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/components/chat/__tests__/chatStore.spec.ts src/components/chat/__tests__/ChatView.spec.ts
```

Expected: FAIL because GPT still sends/renders search state and Composer still has two file inputs/buttons.

- [ ] **Step 3: Implement the provider-aware accept utility**

Create `webChatAttachmentAccept.ts`:

```ts
const IMAGE_ACCEPT = ['image/png', 'image/jpeg', 'image/webp', 'image/gif']

const LEGACY_FILE_EXTENSIONS = ['.pdf', '.docx', '.txt', '.md', '.markdown', '.json', '.csv']

const OPENAI_FILE_EXTENSIONS = [
  '.pdf', '.doc', '.docx', '.dot', '.odt', '.rtf', '.pages',
  '.pot', '.ppa', '.pps', '.ppt', '.pptx', '.pwz', '.wiz', '.key',
  '.xla', '.xlb', '.xlc', '.xlm', '.xls', '.xlsx', '.xlt', '.xlw', '.csv', '.tsv', '.iif',
  '.asm', '.bat', '.c', '.cc', '.conf', '.cpp', '.css', '.cxx', '.def', '.dic', '.eml',
  '.h', '.hh', '.htm', '.html', '.ics', '.ifb', '.in', '.js', '.json', '.ksh', '.list',
  '.log', '.markdown', '.md', '.mht', '.mhtml', '.mime', '.mjs', '.nws', '.pl', '.py',
  '.rst', '.s', '.sql', '.srt', '.text', '.txt', '.vcf', '.vtt', '.xml', '.ts', '.tsx',
  '.jsx', '.java', '.go', '.rs', '.scala', '.ps1', '.diff', '.patch', '.php', '.rb', '.sh',
  '.bash', '.zsh', '.tex', '.cs', '.kt', '.kts', '.swift', '.lua', '.r', '.jl', '.m', '.mm',
  '.erl', '.ex', '.exs', '.hs', '.clj', '.cljs', '.cljc', '.groovy', '.dart', '.awk',
  '.hbs', '.mustache', '.ejs', '.jinja', '.jinja2', '.liquid', '.erb', '.twig', '.pug',
  '.jade', '.tmpl', '.cmake', '.gradle', '.ini', '.properties', '.proto', '.scss', '.sass',
  '.less', '.hcl', '.tf', '.toml', '.graphql', '.ndjson', '.json5', '.yaml', '.yml', '.astro',
]

const OPENAI_SPECIAL_FILE_ACCEPT = ['text/x-dockerfile']

export function webChatAttachmentAccept(provider?: string): string {
  const isOpenAI = provider?.trim().toLowerCase() === 'openai'
  const extensions = isOpenAI ? OPENAI_FILE_EXTENSIONS : LEGACY_FILE_EXTENSIONS
  const special = isOpenAI ? OPENAI_SPECIAL_FILE_ACCEPT : []
  return [...IMAGE_ACCEPT, ...extensions, ...special].join(',')
}
```

Create the utility test:

```ts
import { describe, expect, it } from 'vitest'
import { webChatAttachmentAccept } from '@/utils/webChatAttachmentAccept'

describe('webChatAttachmentAccept', () => {
  it('includes OpenAI presentation, spreadsheet, and code formats', () => {
    const accept = webChatAttachmentAccept(' OpenAI ')
    expect(accept).toContain('image/png')
    expect(accept).toContain('.pptx')
    expect(accept).toContain('.xlsx')
    expect(accept).toContain('.py')
    expect(accept).toContain('.go')
    expect(accept).toContain('text/x-dockerfile')
  })

  it('keeps non-OpenAI hints on the legacy range', () => {
    const accept = webChatAttachmentAccept('anthropic')
    expect(accept).toContain('.docx')
    expect(accept).not.toContain('.pptx')
    expect(accept).not.toContain('.py')
  })
})
```

- [ ] **Step 4: Make GPT search server-managed in the store**

Replace `selectedModelSupportsWebSearch` with:

```ts
const selectedModelHasConfigurableWebSearch = computed(() => {
  const model = selectedModel.value
  return Boolean(model?.supports_web_search && model.provider.trim().toLowerCase() !== 'openai')
})
```

Use it in reconciliation, request construction, and the returned store API:

```ts
if (selectedModelHasConfigurableWebSearch.value && webSearchEnabled.value) {
  request.web_search = { enabled: true }
}
```

Keep `webSearchEnabled`; non-OpenAI providers still use it.

- [ ] **Step 5: Replace both Composer upload controls with one**

Import `webChatAttachmentAccept` and use:

```ts
const attachmentInput = ref<HTMLInputElement | null>(null)
const attachmentAccept = computed(() => webChatAttachmentAccept(chatStore.selectedModel?.provider))
```

Replace both buttons with:

```vue
<button
  class="inline-flex h-9 w-9 items-center justify-center rounded-lg text-linear-ink-muted transition-colors hover:bg-linear-surface-1 hover:text-linear-ink disabled:cursor-not-allowed disabled:opacity-50"
  type="button"
  :title="t('chat.attachFile')"
  :aria-label="t('chat.attachFile')"
  data-testid="chat-attachment-upload"
  :disabled="chatStore.streaming || uploading"
  @click="attachmentInput?.click()"
>
  <Icon name="upload" size="sm" />
</button>
```

Replace both hidden inputs with:

```vue
<input
  ref="attachmentInput"
  class="hidden"
  type="file"
  :accept="attachmentAccept"
  data-testid="chat-attachment-input"
  @change="handleFileInput"
/>
```

Do not add `multiple`; repeated single selections continue appending through the existing store method.

Use `selectedModelHasConfigurableWebSearch` in the options template, `hasModelOptions`, and `toggleWebSearch`. Add no GPT label, tooltip, banner, or explanation.

- [ ] **Step 6: Add the new utility test to WebChat verification**

Append to `FRONTEND_WEBCHAT_VITEST` in `Makefile`:

```make
	src/utils/__tests__/webChatAttachmentAccept.spec.ts \
```

Keep all existing WebChat test paths.

- [ ] **Step 7: Verify and commit**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/utils/__tests__/webChatAttachmentAccept.spec.ts src/components/chat/__tests__/chatStore.spec.ts src/components/chat/__tests__/ChatView.spec.ts
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run lint:check
git diff --check
git add Makefile frontend/src/utils/webChatAttachmentAccept.ts frontend/src/utils/__tests__/webChatAttachmentAccept.spec.ts frontend/src/stores/chat.ts frontend/src/components/chat/Composer.vue frontend/src/components/chat/__tests__/chatStore.spec.ts frontend/src/components/chat/__tests__/ChatView.spec.ts
git commit -m "fix(webchat): unify GPT attachment controls"
```

Expected: one upload control, provider-aware accept hints, no GPT search config/UI, preserved non-OpenAI search control, clean typecheck, and clean ESLint output.

---

### Task 8: Integrated verification and production boundary

**Files:**
- Verify: all files committed in Tasks 1-7.
- Refresh locally: `graphify-out` via `graphify update .`; do not stage generated graph files.

**Interfaces:**
- Consumes: the completed capability, registry, payload, dispatch, store, and Composer changes.
- Produces: local verification evidence and an explicit production-validation gate.

- [ ] **Step 1: Run backend verification**

```bash
cd backend
go test ./internal/pkg/apicompat ./internal/service -count=1
go test ./... -count=1
cd ..
```

Expected: all packages PASS. The service suite must include upload registry, corrupt-file rejection, provider isolation, native Responses inputs, OpenAI dispatch, Anthropic compatibility, usage, and image artifact tests.

- [ ] **Step 2: Run frontend verification**

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 make test-frontend-webchat
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run lint:check
```

Expected: WebChat Vitest (including the accept utility), Vue type checking, and ESLint exit zero.

- [ ] **Step 3: Refresh graphify and inspect exact scope**

```bash
graphify update .
git diff --check
git log --oneline ec30c01743baad037c84aeaf5f9193b6658441d3..HEAD
git status --short
```

Expected: graphify completes without an integrity error; `git diff --check` is empty. Only generated `graphify-out` files may remain dirty/untracked. No Task 3 source file may remain uncommitted.

- [ ] **Step 4: Run whole-branch review**

Generate a review package from the last plan-only commit before implementation began, so Tasks 1 and 2 are included as well as the expanded work:

```bash
/home/alvin/.codex/plugins/cache/openai-curated-remote/superpowers/6.1.1/skills/subagent-driven-development/scripts/review-package ec30c01743baad037c84aeaf5f9193b6658441d3 HEAD
```

Dispatch the final reviewer with the package, the approved design, this plan, and the progress-ledger Minor findings. Fix all Critical/Important findings through one fix subagent, rerun covering tests, and re-review. The final review must explicitly check that the frontend and backend extension lists do not drift in the formats promised by the spec.

- [ ] **Step 5: Stop before a production OAuth request**

Report local results and request separate authorization for one real upstream file request. After approval, send it through the normal authenticated production WebChat conversation/message endpoint so account selection, configured proxy, `OpenAIGatewayService.Forward`, usage recording, and logs are exercised together.

If OAuth rejects Base64 `input_file`, report it as a release blocker. Do not fall back to filename-only text, `text_preview`, local extraction, OCR, direct unproxied provider calls, or a production code/config change.
