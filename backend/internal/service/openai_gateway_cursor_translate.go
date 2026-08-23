package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// Request-side translation is deliberately stateless. Cursor's agent service
// accepts one prompt plus opaque conversation state minted by Cursor itself;
// pooled gateway requests therefore flatten the caller's complete history into
// a self-contained turn and leave conversation state empty.

const (
	envCursorNativeTools  = "SUB2API_CURSOR_NATIVE_TOOLS"
	envCursorNativeImages = "SUB2API_CURSOR_NATIVE_IMAGES"
)

const (
	cursorPromptUserLabel      = "User"
	cursorPromptAssistantLabel = "Assistant"
	cursorPromptToolLabel      = "Tool result"
	cursorThinkingSuffix       = "-thinking"
)

const (
	cursorImageFallbackTokens = 1500
	cursorImageTokenDivisor   = 750
)

type cursorTranslateOptions struct {
	nativeTools    bool
	nativeImages   bool
	observedModels []string
	cwd            string
}

func cursorNativeFlags() cursorTranslateOptions {
	return cursorTranslateOptions{
		nativeTools:  parseCursorEnvBoolDefaultTrue(os.Getenv(envCursorNativeTools)),
		nativeImages: parseCursorEnvBoolDefaultTrue(os.Getenv(envCursorNativeImages)),
	}
}

func parseCursorEnvBoolDefaultTrue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

type cursorToolPlan struct {
	declarations []cursorpkg.AgentTool
	instruction  string
}

type cursorInputEstimate struct {
	text        string
	imageTokens int
}

const cursorTranscriptVersion = "cursor-transcript-v1"

type cursorTranscriptEnvelope struct {
	Version  string                   `json:"version"`
	Messages []cursorTranscriptRecord `json:"messages"`
}

type cursorTranscriptRecord struct {
	Role         string                     `json:"role"`
	Content      string                     `json:"content"`
	Name         string                     `json:"name,omitempty"`
	ToolCallID   string                     `json:"tool_call_id,omitempty"`
	ToolCalls    []cursorTranscriptToolCall `json:"tool_calls,omitempty"`
	FunctionCall *cursorTranscriptToolCall  `json:"function_call,omitempty"`
}

type cursorTranscriptToolCall struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

func buildCursorAgentRun(
	account *Account,
	upstreamModel string,
	req *apicompat.ChatCompletionsRequest,
) (cursorpkg.AgentRunParams, cursorInputEstimate, error) {
	opts := cursorNativeFlags()
	opts.cwd = cursorpkg.AgentDefaultCwd
	if account != nil && req != nil && cursorAgentWantsThinking(req.ReasoningEffort) {
		opts.observedModels = CursorObservedModelIDs(account.Extra)
	}
	return buildCursorAgentRunParams(upstreamModel, req, opts)
}

func buildCursorAgentRunParams(
	upstreamModel string,
	req *apicompat.ChatCompletionsRequest,
	opts cursorTranslateOptions,
) (cursorpkg.AgentRunParams, cursorInputEstimate, error) {
	if req == nil {
		return cursorpkg.AgentRunParams{}, cursorInputEstimate{}, fmt.Errorf("cursor: nil chat request")
	}

	plan, err := planCursorAgentTools(req, opts.nativeTools)
	if err != nil {
		return cursorpkg.AgentRunParams{}, cursorInputEstimate{}, err
	}

	var (
		systemRecords []cursorTranscriptRecord
		turns         []cursorTranscriptRecord
		images        []cursorpkg.AgentImage
		inputParts    []string
	)
	appendInput := func(text string) {
		if text != "" {
			inputParts = append(inputParts, text)
		}
	}
	if strings.TrimSpace(req.Instructions) != "" {
		systemRecords = append(systemRecords, cursorTranscriptRecord{Role: "instructions", Content: req.Instructions})
		appendInput(req.Instructions)
	}

	leadingSystemRoles := true
	for _, message := range req.Messages {
		text, messageImages := cursorAgentMessageParts(message, opts.nativeImages)
		images = append(images, messageImages...)
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if leadingSystemRoles && (role == "system" || role == "developer") {
			systemRecords = append(systemRecords, cursorTranscriptRecord{Role: role, Content: text})
			appendInput(text)
			continue
		}
		leadingSystemRoles = false

		switch role {
		case "system", "developer":
			turns = append(turns, cursorTranscriptRecord{Role: role, Content: text})
			appendInput(text)
		case "assistant":
			record := cursorAssistantTranscriptRecord(message, text)
			if text != "" || len(record.ToolCalls) > 0 || record.FunctionCall != nil {
				turns = append(turns, record)
				appendInput(text)
				appendInput(cursorAssistantToolCallText(message))
			}
		case "tool", "function":
			turns = append(turns, cursorTranscriptRecord{
				Role: role, Content: text, Name: strings.TrimSpace(message.Name), ToolCallID: strings.TrimSpace(message.ToolCallID),
			})
			appendInput(text)
		default:
			if text == "" && len(messageImages) == 0 {
				continue
			}
			turns = append(turns, cursorTranscriptRecord{Role: "user", Content: text})
			appendInput(text)
		}
	}

	systemPrompt := renderCursorSystemPrompt(systemRecords, plan.instruction)
	model, maxMode := cursorAgentWireModel(upstreamModel, req.ReasoningEffort, opts.observedModels)
	cwd := opts.cwd
	if cwd == "" {
		cwd = cursorpkg.AgentDefaultCwd
	}
	params := cursorpkg.AgentRunParams{
		Prompt:       renderCursorAgentPrompt(turns),
		Model:        model,
		MaxMode:      maxMode,
		SystemPrompt: systemPrompt,
		Mode:         cursorpkg.AgentModeAgent,
		Tools:        plan.declarations,
		Images:       images,
		Cwd:          cwd,
	}

	appendInput(plan.instruction)
	if len(plan.declarations) > 0 {
		appendInput(cursorToolDeclarationsEstimateText(plan.declarations, req.ToolChoice))
	}
	return params, cursorInputEstimate{
		text:        strings.Join(inputParts, "\n"),
		imageTokens: cursorImageTokens(images),
	}, nil
}

func cursorToolDeclarationsEstimateText(declarations []cursorpkg.AgentTool, toolChoice json.RawMessage) string {
	parts := make([]string, 0, len(declarations)+1)
	for _, tool := range declarations {
		encoded, err := json.Marshal(tool)
		if err != nil {
			parts = append(parts, tool.Name+" "+tool.Description)
			continue
		}
		parts = append(parts, string(encoded))
	}
	if choice := strings.TrimSpace(string(toolChoice)); choice != "" && choice != "null" {
		parts = append(parts, choice)
	}
	return strings.Join(parts, "\n")
}

func cursorImageTokens(images []cursorpkg.AgentImage) int {
	maxInt := int(^uint(0) >> 1)
	total := 0
	for _, image := range images {
		cost := uint64(cursorImageFallbackTokens)
		if image.Width > 0 && image.Height > 0 {
			cost = uint64(image.Width) * uint64(image.Height) / uint64(cursorImageTokenDivisor)
		}
		if cost > uint64(maxInt-total) {
			return maxInt
		}
		total += int(cost)
	}
	return total
}

func renderCursorAgentPrompt(turns []cursorTranscriptRecord) string {
	if len(turns) == 1 && turns[0].Role == "user" && turns[0].Name == "" && turns[0].ToolCallID == "" &&
		len(turns[0].ToolCalls) == 0 && turns[0].FunctionCall == nil {
		return turns[0].Content
	}
	if len(turns) == 0 {
		return ""
	}
	return marshalCursorTranscript(turns)
}

func renderCursorSystemPrompt(records []cursorTranscriptRecord, toolInstruction string) string {
	if len(records) == 0 {
		return toolInstruction
	}
	if toolInstruction != "" {
		records = append(append([]cursorTranscriptRecord(nil), records...), cursorTranscriptRecord{
			Role: "tool_instruction", Content: toolInstruction,
		})
	}
	return marshalCursorTranscript(records)
}

func marshalCursorTranscript(records []cursorTranscriptRecord) string {
	encoded, err := json.Marshal(cursorTranscriptEnvelope{
		Version: cursorTranscriptVersion, Messages: records,
	})
	if err != nil {
		return ""
	}
	return string(encoded)
}

func cursorAssistantTranscriptRecord(message apicompat.ChatMessage, content string) cursorTranscriptRecord {
	record := cursorTranscriptRecord{Role: "assistant", Content: content}
	if len(message.ToolCalls) > 0 {
		record.ToolCalls = make([]cursorTranscriptToolCall, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			if strings.TrimSpace(call.ID) == "" && strings.TrimSpace(call.Function.Name) == "" {
				continue
			}
			record.ToolCalls = append(record.ToolCalls, cursorTranscriptToolCall{
				ID:        strings.TrimSpace(call.ID),
				Type:      strings.TrimSpace(call.Type),
				Name:      strings.TrimSpace(call.Function.Name),
				Arguments: call.Function.Arguments,
			})
		}
	}
	if call := message.FunctionCall; call != nil && strings.TrimSpace(call.Name) != "" {
		record.FunctionCall = &cursorTranscriptToolCall{
			Name: strings.TrimSpace(call.Name), Arguments: call.Arguments,
		}
	}
	return record
}

func joinCursorPromptParts(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "\n\n")
}

func cursorToolResultLabel(message apicompat.ChatMessage) string {
	id := strings.TrimSpace(message.ToolCallID)
	name := strings.TrimSpace(message.Name)
	identifiers := make([]string, 0, 2)
	if id != "" {
		identifiers = append(identifiers, id)
	}
	if name != "" && name != id {
		identifiers = append(identifiers, name)
	}
	if len(identifiers) == 0 {
		return cursorPromptToolLabel
	}
	return cursorPromptToolLabel + " (" + strings.Join(identifiers, ", ") + ")"
}

func cursorAssistantToolCallText(message apicompat.ChatMessage) string {
	parts := make([]string, 0, len(message.ToolCalls)+1)
	for _, call := range message.ToolCalls {
		id := strings.TrimSpace(call.ID)
		name := strings.TrimSpace(call.Function.Name)
		if id == "" && name == "" {
			continue
		}
		label := "[tool call"
		if id != "" {
			label += " " + id
		}
		label += "]"
		if name != "" {
			label += " " + name
		}
		if arguments := strings.TrimSpace(call.Function.Arguments); arguments != "" {
			label += " " + arguments
		}
		parts = append(parts, label)
	}
	if call := message.FunctionCall; call != nil {
		name := strings.TrimSpace(call.Name)
		if name != "" {
			rendered := "[function call] " + name
			if arguments := strings.TrimSpace(call.Arguments); arguments != "" {
				rendered += " " + arguments
			}
			parts = append(parts, rendered)
		}
	}
	return strings.Join(parts, "\n")
}

func cursorAgentWireModel(model, reasoningEffort string, observedModels []string) (string, bool) {
	id := strings.TrimSpace(model)
	maxMode := false
	lowerID := strings.ToLower(id)
	for _, suffix := range []string{"-max", ":max"} {
		if strings.HasSuffix(lowerID, suffix) && len(id) > len(suffix) {
			id = id[:len(id)-len(suffix)]
			maxMode = true
			break
		}
	}
	switch strings.ToLower(id) {
	case "", "auto", cursorpkg.AgentDefaultModel:
		return cursorpkg.AgentDefaultModel, maxMode
	}

	if cursorAgentWantsThinking(reasoningEffort) && !strings.HasSuffix(strings.ToLower(id), cursorThinkingSuffix) {
		candidate := id + cursorThinkingSuffix
		if containsCursorModelFold(observedModels, candidate) {
			id = candidate
		}
	}
	return id, maxMode
}

func cursorAgentWantsThinking(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high", "xhigh", "minimal":
		return true
	default:
		return false
	}
}

func containsCursorModelFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

type cursorToolDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type cursorToolChoice struct {
	none     bool
	required bool
	function string
}

func planCursorAgentTools(req *apicompat.ChatCompletionsRequest, nativeTools bool) (cursorToolPlan, error) {
	if req == nil {
		return cursorToolPlan{}, fmt.Errorf("cursor: nil chat request")
	}
	declarations := collectCursorToolDeclarations(req)
	choice, err := resolveCursorToolChoice(req)
	if err != nil {
		return cursorToolPlan{}, err
	}
	if choice.none {
		return cursorToolPlan{}, nil
	}
	if len(declarations) == 0 {
		if choice.required || choice.function != "" {
			return cursorToolPlan{}, fmt.Errorf("cursor: tool_choice requires at least one function declaration")
		}
		return cursorToolPlan{}, nil
	}
	if choice.function != "" && !containsCursorToolDeclaration(declarations, choice.function) {
		return cursorToolPlan{}, fmt.Errorf("cursor: tool_choice names undeclared function %q", choice.function)
	}
	if !nativeTools {
		return cursorToolPlan{instruction: cursorToolInstruction(declarations, choice)}, nil
	}

	plan := cursorToolPlan{declarations: make([]cursorpkg.AgentTool, 0, len(declarations))}
	for _, declaration := range declarations {
		plan.declarations = append(plan.declarations, cursorpkg.AgentTool{
			Name:        declaration.Name,
			Description: declaration.Description,
			InputSchema: cursorAgentToolSchema(declaration.Parameters),
		})
	}
	plan.instruction = cursorToolChoiceInstruction(choice)
	return plan, nil
}

func resolveCursorToolChoice(req *apicompat.ChatCompletionsRequest) (cursorToolChoice, error) {
	if req == nil {
		return cursorToolChoice{}, fmt.Errorf("cursor: nil chat request")
	}
	if cursorJSONChoiceProvided(req.ToolChoice) {
		return parseCursorToolChoice(req.ToolChoice)
	}
	return parseCursorLegacyFunctionChoice(req.FunctionCall)
}

func cursorJSONChoiceProvided(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

func parseCursorLegacyFunctionChoice(raw json.RawMessage) (cursorToolChoice, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return cursorToolChoice{}, nil
	}

	var stringChoice string
	if err := json.Unmarshal(raw, &stringChoice); err == nil {
		switch strings.ToLower(strings.TrimSpace(stringChoice)) {
		case "auto":
			return cursorToolChoice{}, nil
		case "none":
			return cursorToolChoice{none: true}, nil
		default:
			return cursorToolChoice{}, fmt.Errorf("cursor: unsupported function_call %q", stringChoice)
		}
	}

	var object struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return cursorToolChoice{}, fmt.Errorf("cursor: malformed function_call: %w", err)
	}
	name := strings.TrimSpace(object.Name)
	if name == "" {
		return cursorToolChoice{}, fmt.Errorf("cursor: named function_call requires a function name")
	}
	return cursorToolChoice{required: true, function: name}, nil
}

func collectCursorToolDeclarations(req *apicompat.ChatCompletionsRequest) []cursorToolDeclaration {
	if req == nil {
		return nil
	}
	declarations := make([]cursorToolDeclaration, 0, len(req.Tools)+len(req.Functions))
	seen := make(map[string]struct{}, len(req.Tools)+len(req.Functions))
	add := func(declaration cursorToolDeclaration) {
		declaration.Name = strings.TrimSpace(declaration.Name)
		if declaration.Name == "" {
			return
		}
		if _, exists := seen[declaration.Name]; exists {
			return
		}
		seen[declaration.Name] = struct{}{}
		declarations = append(declarations, declaration)
	}

	for _, tool := range req.Tools {
		typeName := strings.TrimSpace(tool.Type)
		if typeName != "" && !strings.EqualFold(typeName, "function") {
			continue
		}
		if tool.Function == nil {
			continue
		}
		add(cursorToolDeclaration{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	for _, function := range req.Functions {
		add(cursorToolDeclaration{
			Name:        function.Name,
			Description: function.Description,
			Parameters:  function.Parameters,
		})
	}
	return declarations
}

func containsCursorToolDeclaration(declarations []cursorToolDeclaration, name string) bool {
	for _, declaration := range declarations {
		if declaration.Name == name {
			return true
		}
	}
	return false
}

func cursorAgentToolSchema(parameters json.RawMessage) any {
	trimmed := strings.TrimSpace(string(parameters))
	if trimmed == "" || trimmed == "null" {
		return cursorEmptyAgentToolSchema()
	}
	var decoded any
	if err := json.Unmarshal(parameters, &decoded); err != nil || decoded == nil {
		return cursorEmptyAgentToolSchema()
	}
	return decoded
}

func cursorEmptyAgentToolSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func parseCursorToolChoice(raw json.RawMessage) (cursorToolChoice, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return cursorToolChoice{}, nil
	}

	var stringChoice string
	if err := json.Unmarshal(raw, &stringChoice); err == nil {
		switch strings.ToLower(strings.TrimSpace(stringChoice)) {
		case "auto":
			return cursorToolChoice{}, nil
		case "none":
			return cursorToolChoice{none: true}, nil
		case "required", "any":
			return cursorToolChoice{required: true}, nil
		default:
			return cursorToolChoice{}, fmt.Errorf("cursor: unsupported tool_choice %q", stringChoice)
		}
	}

	var object struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return cursorToolChoice{}, fmt.Errorf("cursor: malformed tool_choice: %w", err)
	}
	typeName := strings.ToLower(strings.TrimSpace(object.Type))
	if typeName == "none" {
		return cursorToolChoice{none: true}, nil
	}
	if typeName != "" && typeName != "function" {
		return cursorToolChoice{}, fmt.Errorf("cursor: unsupported tool_choice type %q", object.Type)
	}
	name := strings.TrimSpace(object.Function.Name)
	if name == "" {
		name = strings.TrimSpace(object.Name)
	}
	if name == "" {
		return cursorToolChoice{}, fmt.Errorf("cursor: named tool_choice requires a function name")
	}
	return cursorToolChoice{required: true, function: name}, nil
}

func cursorToolChoiceInstruction(choice cursorToolChoice) string {
	switch {
	case choice.function != "":
		return "You must call the tool `" + choice.function + "` before replying."
	case choice.required:
		return "You must call at least one of the available tools before replying."
	default:
		return ""
	}
}

func cursorToolInstruction(declarations []cursorToolDeclaration, choice cursorToolChoice) string {
	type serializableDeclaration struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Parameters  any    `json:"parameters,omitempty"`
	}
	serializable := make([]serializableDeclaration, 0, len(declarations))
	for _, declaration := range declarations {
		serializable = append(serializable, serializableDeclaration{
			Name:        declaration.Name,
			Description: declaration.Description,
			Parameters:  cursorAgentToolSchema(declaration.Parameters),
		})
	}
	encoded, err := json.Marshal(serializable)
	if err != nil {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("You may call the following tools when helpful. ")
	builder.WriteString("Respond with a JSON tool call when you decide to use one.\n")
	builder.WriteString("Available tools: ")
	builder.Write(encoded)
	if nudge := cursorToolChoiceInstruction(choice); nudge != "" {
		builder.WriteString("\nTool choice preference: ")
		builder.WriteString(nudge)
	}
	return builder.String()
}

func cursorAgentMessageParts(message apicompat.ChatMessage, nativeImages bool) (string, []cursorpkg.AgentImage) {
	return cursorAgentMessagePartsWithImageParser(message, nativeImages, cursorpkg.ParseImageDataURI)
}

func cursorAgentMessagePartsWithImageParser(
	message apicompat.ChatMessage,
	nativeImages bool,
	parseImage func(string) (cursorpkg.AgentImage, error),
) (string, []cursorpkg.AgentImage) {
	raw := strings.TrimSpace(string(message.Content))
	role := strings.ToLower(strings.TrimSpace(message.Role))
	isToolResult := role == "tool" || role == "function"
	if raw == "" {
		if isToolResult {
			return "[empty result]", nil
		}
		return "", nil
	}
	if raw == "null" {
		if isToolResult {
			return "[null result]", nil
		}
		return "", nil
	}

	var text string
	if err := json.Unmarshal(message.Content, &text); err == nil {
		if isToolResult && text == "" {
			return "[empty result]", nil
		}
		return text, nil
	}

	var rawParts []json.RawMessage
	if err := json.Unmarshal(message.Content, &rawParts); err != nil {
		if isToolResult && json.Valid([]byte(raw)) {
			return raw, nil
		}
		return cursorNonTextContentFallback(raw), nil
	}

	textParts := make([]string, 0, len(rawParts))
	images := make([]cursorpkg.AgentImage, 0)
	for _, rawPart := range rawParts {
		var part apicompat.ChatContentPart
		if err := json.Unmarshal(rawPart, &part); err != nil {
			textParts = append(textParts, "[content omitted: invalid part]")
			continue
		}
		switch {
		case strings.EqualFold(strings.TrimSpace(part.Type), "text"):
			if strings.TrimSpace(part.Text) != "" {
				textParts = append(textParts, part.Text)
			}
		case strings.EqualFold(strings.TrimSpace(part.Type), "image_url"):
			if part.ImageURL == nil {
				textParts = append(textParts, "[image omitted: missing URL]")
				continue
			}
			url := strings.TrimSpace(part.ImageURL.URL)
			if url == "" {
				textParts = append(textParts, "[image omitted: empty URL]")
				continue
			}
			if !cursorHasDataScheme(url) {
				textParts = append(textParts, cursorImageFallbackText(url, cursorpkg.ErrNotImageDataURI))
				continue
			}
			if !nativeImages {
				textParts = append(textParts, "[image omitted: native images disabled]")
				continue
			}
			if err := preflightCursorImageDataURI(url); err != nil {
				textParts = append(textParts, cursorImageFallbackText(url, err))
				continue
			}
			image, err := parseImage(url)
			if err != nil {
				textParts = append(textParts, cursorImageFallbackText(url, err))
				continue
			}
			images = append(images, image)
		default:
			textParts = append(textParts, "[unsupported content: "+cursorSafeContentType(part.Type)+"]")
		}
	}
	return strings.Join(textParts, "\n"), images
}

func cursorNonTextContentFallback(raw string) string {
	if !json.Valid([]byte(raw)) {
		return "[content omitted: invalid JSON]"
	}
	switch raw[0] {
	case '{':
		return "[content omitted: object]"
	case 't', 'f':
		return "[content omitted: boolean]"
	default:
		return "[content omitted: non-text]"
	}
}

func cursorSafeContentType(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 64 {
		return "unknown"
	}
	for _, char := range raw {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return "unknown"
	}
	return raw
}

var (
	errCursorImageEncodedTooLarge  = errors.New("cursor: encoded image exceeds decoded size limit")
	errCursorImageEncodedMalformed = errors.New("cursor: malformed encoded image")
)

// preflightCursorImageDataURI validates the encoded payload and proves its
// decoded upper bound before ParseImageDataURI allocates a decoded byte slice.
// It scans the caller-owned string in place: no cleaned/copy payload is built.
func preflightCursorImageDataURI(uri string) error {
	raw := strings.TrimSpace(uri)
	if !cursorHasDataScheme(raw) {
		return errCursorImageEncodedMalformed
	}
	comma := strings.IndexByte(raw, ',')
	if comma < 0 {
		return errCursorImageEncodedMalformed
	}
	payload := raw[comma+1:]
	maxDataCharacters := cursorMaxBase64DataCharacters(cursorpkg.MaxImageBytes)
	dataCharacters := 0
	padding := 0
	paddingStarted := false

	for index := 0; index < len(payload); index++ {
		character := payload[index]
		switch character {
		case ' ', '\t', '\r', '\n':
			continue
		case '=':
			paddingStarted = true
			padding++
			if padding > 2 {
				return errCursorImageEncodedMalformed
			}
			continue
		}
		if paddingStarted || !cursorIsBase64Character(character) {
			return errCursorImageEncodedMalformed
		}
		dataCharacters++
		if dataCharacters > maxDataCharacters {
			return errCursorImageEncodedTooLarge
		}
	}

	if dataCharacters == 0 || dataCharacters%4 == 1 {
		return errCursorImageEncodedMalformed
	}
	expectedPadding := 0
	switch dataCharacters % 4 {
	case 2:
		expectedPadding = 2
	case 3:
		expectedPadding = 1
	}
	if padding != 0 && padding != expectedPadding {
		return errCursorImageEncodedMalformed
	}
	decodedBytes := dataCharacters/4*3 + dataCharacters%4*6/8
	if decodedBytes > cursorpkg.MaxImageBytes {
		return errCursorImageEncodedTooLarge
	}
	return nil
}

func cursorMaxBase64DataCharacters(decodedBytes int) int {
	characters := decodedBytes / 3 * 4
	switch decodedBytes % 3 {
	case 1:
		characters += 2
	case 2:
		characters += 3
	}
	return characters
}

func cursorIsBase64Character(character byte) bool {
	return (character >= 'A' && character <= 'Z') ||
		(character >= 'a' && character <= 'z') ||
		(character >= '0' && character <= '9') ||
		character == '+' || character == '/'
}

func cursorHasDataScheme(raw string) bool {
	raw = strings.TrimSpace(raw)
	return len(raw) >= len("data:") && strings.EqualFold(raw[:len("data:")], "data:")
}

func dataURIMediaType(uri string) string {
	raw := strings.TrimSpace(uri)
	if !strings.HasPrefix(strings.ToLower(raw), "data:") {
		return ""
	}
	comma := strings.IndexByte(raw, ',')
	if comma < 0 {
		return ""
	}
	mediaType, _, _ := strings.Cut(raw[len("data:"):comma], ";")
	return strings.ToLower(strings.TrimSpace(mediaType))
}

func cursorImageFallbackText(url string, err error) string {
	if cursorHasDataScheme(url) {
		logger.L().Debug("cursor: dropping inline image", zap.Error(err))
		return "[image omitted: could not be decoded]"
	}
	return "[image: " + url + "]"
}

func cursorRequestOutputLimit(req *apicompat.ChatCompletionsRequest) int {
	if req == nil {
		return 0
	}
	if req.MaxCompletionTokens != nil && *req.MaxCompletionTokens > 0 {
		return *req.MaxCompletionTokens
	}
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		return *req.MaxTokens
	}
	return 0
}
