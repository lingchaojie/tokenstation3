package apicompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/sjson"
)

const (
	maxResponsesClientToolTrackedCalls          = 1024
	maxResponsesClientToolRetainedMetadataBytes = 1024
	maxResponsesClientToolRetainedArgumentBytes = 8 << 20
)

// ResponsesClientToolMapping records the reversible lowering applied before a
// native Responses request is sent to an upstream that only understands
// function tools.
type ResponsesClientToolMapping struct {
	CustomTools    map[string]bool
	ToolSearch     bool
	NamespaceTools map[string]ResponsesNamespaceName
}

// AdaptResponsesClientTools lowers Codex client-only tools in req to
// ordinary function tools. It mutates req and returns the mapping required to
// restore the upstream response.
func AdaptResponsesClientTools(req map[string]any) (ResponsesClientToolMapping, bool, error) {
	if req == nil {
		return ResponsesClientToolMapping{}, false, nil
	}
	tools, ok := req["tools"].([]any)
	if !ok || len(tools) == 0 {
		return ResponsesClientToolMapping{}, false, nil
	}

	adapter := ResponsesClientToolMapping{CustomTools: make(map[string]bool)}
	functionNames := make(map[string]bool)
	customNames := make(map[string]bool)
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringValue(tool["name"]))
		switch strings.TrimSpace(stringValue(tool["type"])) {
		case "function":
			if name != "" {
				functionNames[name] = true
			}
		case "custom":
			if name != "" {
				customNames[name] = true
			}
		case "tool_search":
			adapter.ToolSearch = true
		}
	}
	for name := range customNames {
		if functionNames[name] {
			return ResponsesClientToolMapping{}, false, fmt.Errorf("custom tool %q conflicts with a function tool of the same name; this upstream cannot disambiguate them, rename one of the tools", name)
		}
	}
	if adapter.ToolSearch && (functionNames[toolSearchProxyName] || customNames[toolSearchProxyName]) {
		return ResponsesClientToolMapping{}, false, fmt.Errorf("built-in tool_search conflicts with a declared tool named %q; this upstream cannot disambiguate them, rename the tool", toolSearchProxyName)
	}

	// Namespace flattening also rewrites namespace-qualified history and choice.
	names, flattened, err := FlattenResponsesNamespaces(req)
	if err != nil {
		return ResponsesClientToolMapping{}, false, err
	}
	adapter.NamespaceTools = names
	if adapter.ToolSearch {
		if _, exists := names[toolSearchProxyName]; exists {
			return ResponsesClientToolMapping{}, false, fmt.Errorf("built-in tool_search conflicts with namespace tool flattened as %q; this upstream cannot disambiguate them, rename the tool", toolSearchProxyName)
		}
	}

	tools, _ = req["tools"].([]any)
	lowered := make([]any, 0, len(tools))
	changed := flattened
	seenSearch := false
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			lowered = append(lowered, raw)
			continue
		}
		typ := strings.TrimSpace(stringValue(tool["type"]))
		name := strings.TrimSpace(stringValue(tool["name"]))
		switch typ {
		case "custom":
			if name == "" {
				lowered = append(lowered, raw)
				continue
			}
			copy := copyClientTool(tool)
			copy["type"] = "function"
			copy["parameters"] = json.RawMessage(customToolInputSchema)
			delete(copy, "format")
			adapter.CustomTools[name] = true
			lowered = append(lowered, copy)
			changed = true
		case "tool_search":
			if seenSearch {
				changed = true
				continue
			}
			seenSearch = true
			lowered = append(lowered, map[string]any{
				"type": "function", "name": toolSearchProxyName,
				"description": "Search and load Codex tools, plugins, connectors, and MCP namespaces for the current task.",
				"parameters":  json.RawMessage(toolSearchProxySchema),
			})
			changed = true
		default:
			lowered = append(lowered, raw)
		}
	}
	if changed {
		req["tools"] = lowered
	}
	if rewriteClientToolHistory(req["input"], &adapter) {
		changed = true
	}
	if rewriteClientToolChoice(req, &adapter) {
		changed = true
	}
	if len(adapter.CustomTools) == 0 {
		adapter.CustomTools = nil
	}
	if len(adapter.NamespaceTools) == 0 {
		adapter.NamespaceTools = nil
	}
	return adapter, changed, nil
}

func copyClientTool(tool map[string]any) map[string]any {
	copy := make(map[string]any, len(tool))
	for key, value := range tool {
		copy[key] = value
	}
	return copy
}

func rewriteClientToolHistory(value any, adapter *ResponsesClientToolMapping) bool {
	changed := false
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			typ := strings.TrimSpace(stringValue(typed["type"]))
			switch typ {
			case "custom_tool_call":
				if adapter.CustomTools[strings.TrimSpace(stringValue(typed["name"]))] {
					typed["type"] = "function_call"
					typed["arguments"] = customToolCallArguments(stringValue(typed["input"]))
					delete(typed, "input")
					changed = true
				}
			case "custom_tool_call_output":
				typed["type"] = "function_call_output"
				normalizeClientToolOutput(typed)
				changed = true
			case "tool_search_call":
				if adapter.ToolSearch {
					typed["type"] = "function_call"
					typed["name"] = toolSearchProxyName
					typed["arguments"] = rawObjectString(typed["arguments"])
					delete(typed, "execution")
					changed = true
				}
			case "tool_search_output":
				if adapter.ToolSearch {
					typed["type"] = "function_call_output"
					normalizeClientToolOutput(typed)
					changed = true
				}
			}
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	return changed
}

func normalizeClientToolOutput(item map[string]any) {
	output, exists := item["output"]
	if !exists {
		return
	}
	if _, ok := output.(string); ok {
		return
	}
	if output == nil {
		item["output"] = ""
		return
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		item["output"] = ""
		return
	}
	item["output"] = string(encoded)
}

func rewriteClientToolChoice(req map[string]any, adapter *ResponsesClientToolMapping) bool {
	choice, ok := req["tool_choice"].(map[string]any)
	if !ok {
		return false
	}
	typ := strings.TrimSpace(stringValue(choice["type"]))
	name := strings.TrimSpace(stringValue(choice["name"]))
	if typ == "custom" && adapter.CustomTools[name] {
		choice["type"] = "function"
		return true
	}
	if typ == "tool_search" && adapter.ToolSearch {
		req["tool_choice"] = map[string]any{"type": "function", "name": toolSearchProxyName}
		return true
	}
	return false
}

func customToolCallArguments(input string) string {
	encoded, _ := json.Marshal(map[string]string{"input": input})
	return string(encoded)
}

func rawObjectString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// RestoreResponsesClientToolPayload restores client tool calls in a non-stream
// native Responses JSON payload.
func RestoreResponsesClientToolPayload(payload []byte, mapping ResponsesClientToolMapping) ([]byte, bool, error) {
	if len(payload) == 0 {
		return payload, false, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(payload, &root); err != nil {
		return payload, false, err
	}
	changed, err := restoreResponsesClientToolRawObject(root, &mapping)
	if err != nil {
		return payload, false, err
	}
	if !changed {
		return payload, false, nil
	}
	rebuiltPayload, err := marshalResponsesClientToolRawObject(root)
	if err != nil {
		return payload, false, err
	}
	return rebuiltPayload, true, nil
}

func restoreResponsesClientToolRawObject(root map[string]json.RawMessage, adapter *ResponsesClientToolMapping) (bool, error) {
	eventType := decodedRawJSONString(root["type"])
	if isResponsesClientToolTerminalEvent(eventType) {
		// Responses SSE terminal events wrap the response object exactly once.
		// Root-level output/item fields are extensions of the event envelope and
		// must not be interpreted as response output.
		response, ok := root["response"]
		if !ok || !rawJSONHasLeadingByte(response, '{') {
			return false, nil
		}
		var responseObject map[string]json.RawMessage
		if err := json.Unmarshal(response, &responseObject); err != nil {
			return false, err
		}
		responseChanged, err := restoreResponsesClientToolRawFields(responseObject, adapter)
		if err != nil {
			return false, err
		}
		if responseChanged {
			rebuilt, err := marshalResponsesClientToolRawObject(responseObject)
			if err != nil {
				return false, err
			}
			root["response"] = rebuilt
		}
		return responseChanged, nil
	}
	switch eventType {
	case "response.output_item.added", "response.output_item.done":
		// Item lifecycle events expose exactly one item at the root. Do not
		// interpret an unrelated root output extension.
		return restoreResponsesClientToolRawItemField(root, adapter)
	case "":
		// Direct Responses JSON may omit object:"response". Unknown SSE event
		// types are still protected by their non-empty type discriminator.
		return restoreResponsesClientToolRawOutputField(root, adapter)
	}
	return false, nil
}

func isResponsesClientToolTerminalEvent(eventType string) bool {
	switch eventType {
	case "response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled", "response.canceled":
		return true
	default:
		return false
	}
}

func restoreResponsesClientToolRawFields(root map[string]json.RawMessage, adapter *ResponsesClientToolMapping) (bool, error) {
	itemChanged, err := restoreResponsesClientToolRawItemField(root, adapter)
	if err != nil {
		return false, err
	}
	outputChanged, err := restoreResponsesClientToolRawOutputField(root, adapter)
	if err != nil {
		return false, err
	}
	return itemChanged || outputChanged, nil
}

func restoreResponsesClientToolRawItemField(root map[string]json.RawMessage, adapter *ResponsesClientToolMapping) (bool, error) {
	if item, ok := root["item"]; ok && rawJSONHasLeadingByte(item, '{') {
		restored, itemChanged, err := restoreResponsesClientToolRawItem(item, adapter)
		if err != nil {
			return false, err
		}
		if itemChanged {
			root["item"] = restored
			return true, nil
		}
	}
	return false, nil
}

func restoreResponsesClientToolRawOutputField(root map[string]json.RawMessage, adapter *ResponsesClientToolMapping) (bool, error) {
	if output, ok := root["output"]; ok && rawJSONHasLeadingByte(output, '[') {
		restored, outputChanged, err := restoreResponsesClientToolRawOutput(output, adapter)
		if err != nil {
			return false, err
		}
		if outputChanged {
			root["output"] = restored
			return true, nil
		}
	}
	return false, nil
}

func restoreResponsesClientToolRawOutput(raw json.RawMessage, adapter *ResponsesClientToolMapping) (json.RawMessage, bool, error) {
	var output []json.RawMessage
	if err := json.Unmarshal(raw, &output); err != nil {
		return raw, false, err
	}
	if len(output) > maxResponsesClientToolTrackedCalls {
		return raw, false, fmt.Errorf("responses client-tool output exceeds %d-item limit", maxResponsesClientToolTrackedCalls)
	}
	changed := false
	for index := range output {
		rebuilt, itemChanged, err := restoreResponsesClientToolRawItem(output[index], adapter)
		if err != nil {
			return raw, false, err
		}
		if !itemChanged {
			continue
		}
		output[index] = rebuilt
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	rebuilt, err := marshalResponsesClientToolRawArray(output)
	if err != nil {
		return raw, false, err
	}
	return rebuilt, true, nil
}

func marshalResponsesClientToolRawArray(value []json.RawMessage) ([]byte, error) {
	var rebuilt bytes.Buffer
	encoder := json.NewEncoder(&rebuilt)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(rebuilt.Bytes(), []byte("\n")), nil
}

func restoreResponsesClientToolRawItem(raw json.RawMessage, adapter *ResponsesClientToolMapping) (json.RawMessage, bool, error) {
	if !rawJSONHasLeadingByte(raw, '{') {
		return raw, false, nil
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return raw, false, err
	}
	if decodedRawJSONString(item["type"]) != "function_call" {
		return raw, false, nil
	}
	name := strings.TrimSpace(decodedRawJSONString(item["name"]))
	changed := false
	switch {
	case adapter.CustomTools[name]:
		item["type"] = rawJSONString("custom_tool_call")
		item["input"] = rawJSONString(extractCustomToolCallInput(rawResponsesArgumentsString(item["arguments"])))
		delete(item, "arguments")
		delete(item, "namespace")
		changed = true
	case adapter.ToolSearch && name == toolSearchProxyName:
		item["type"] = rawJSONString("tool_search_call")
		item["execution"] = rawJSONString("client")
		item["arguments"] = toolSearchCallArgumentsJSON(rawResponsesArgumentsString(item["arguments"]))
		delete(item, "name")
		delete(item, "namespace")
		changed = true
	}
	itemType := decodedRawJSONString(item["type"])
	if namespace, ok := adapter.NamespaceTools[name]; ok && isNamespaceQualifiedCallType(itemType) {
		item["name"] = rawJSONString(namespace.Name)
		item["namespace"] = rawJSONString(namespace.Namespace)
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	rebuilt, err := marshalResponsesClientToolRawObject(item)
	if err != nil {
		return raw, false, err
	}
	return rebuilt, true, nil
}

func rawJSONHasLeadingByte(raw json.RawMessage, want byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == want
}

func decodedRawJSONString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func rawResponsesArgumentsString(raw json.RawMessage) string {
	if value := decodedRawJSONString(raw); value != "" || len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '"' {
		return value
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	return string(trimmed)
}

func marshalResponsesClientToolRawObject(value map[string]json.RawMessage) ([]byte, error) {
	var rebuilt bytes.Buffer
	encoder := json.NewEncoder(&rebuilt)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(rebuilt.Bytes(), []byte("\n")), nil
}

// ResponsesClientToolStreamRestorer restores client tool stream lifecycles.
// It is intentionally stateful because custom tools need their function
// arguments buffered until the upstream signals the call is complete.
type ResponsesClientToolStreamRestorer struct {
	adapter               ResponsesClientToolMapping
	nextSeq               int
	seenSeq               bool
	calls                 map[string]*responsesClientToolStreamCall
	byOutput              map[int]*responsesClientToolStreamCall
	trackedCalls          int
	retainedArgumentBytes int
	conversionErr         error
}

type responsesClientToolStreamCall struct {
	kind      string
	name      string
	callID    string
	itemID    string
	outputIdx int
	arguments strings.Builder
}

func NewResponsesClientToolStreamRestorer(mapping ResponsesClientToolMapping) *ResponsesClientToolStreamRestorer {
	return &ResponsesClientToolStreamRestorer{adapter: mapping, calls: make(map[string]*responsesClientToolStreamCall), byOutput: make(map[int]*responsesClientToolStreamCall)}
}

// Restore transforms one upstream SSE event into zero or more client events.
// Returned sequence numbers are continuous even when function argument events
// are suppressed or a custom completion expands into two events.
func (r *ResponsesClientToolStreamRestorer) Restore(event ResponsesStreamEvent) []ResponsesStreamEvent {
	if r == nil {
		return []ResponsesStreamEvent{event}
	}
	if r.conversionErr != nil {
		return nil
	}
	if !r.seenSeq {
		r.nextSeq = event.SequenceNumber
		r.seenSeq = true
	}
	var out []ResponsesStreamEvent
	emit := func(event ResponsesStreamEvent) {
		event.SequenceNumber = r.nextSeq
		r.nextSeq++
		out = append(out, event)
	}

	switch event.Type {
	case "response.output_item.added":
		if call, err := r.recordItem(event); err != nil {
			r.conversionErr = err
			return nil
		} else if call != nil {
			if call.kind == "custom" {
				event.Item.Type = "custom_tool_call"
				event.Item.Input = ""
				event.Item.Arguments = ""
				event.Item.Namespace = ""
			} else {
				event.Item.Type = "tool_search_call"
				event.Item.Name = ""
				event.Item.Arguments = "{}"
				event.Item.Namespace = ""
			}
		}
		emit(r.restoreNamespaceEvent(event))
	case "response.function_call_arguments.delta":
		if call := r.callFor(event); call != nil {
			if err := r.appendCallArguments(call, event.Delta); err != nil {
				r.conversionErr = err
				return nil
			}
			return nil
		}
		emit(r.restoreNamespaceEvent(event))
	case "response.function_call_arguments.done":
		if call := r.callFor(event); call != nil {
			if event.Arguments != "" {
				if err := r.setCallArguments(call, event.Arguments); err != nil {
					r.conversionErr = err
					return nil
				}
			}
			if call.kind == "custom" {
				input := extractCustomToolCallInput(call.arguments.String())
				if input != "" {
					emit(ResponsesStreamEvent{Type: "response.custom_tool_call_input.delta", OutputIndex: call.outputIdx, ItemID: call.itemID, Delta: input})
				}
				emit(r.restoreNamespaceEvent(ResponsesStreamEvent{Type: "response.custom_tool_call_input.done", OutputIndex: call.outputIdx, ItemID: call.itemID, CallID: call.callID, Name: call.name, Input: input}))
			}
			return out
		}
		emit(r.restoreNamespaceEvent(event))
	case "response.output_item.done":
		if call, err := r.recordItem(event); err != nil {
			r.conversionErr = err
			return nil
		} else if call != nil {
			if call.kind == "custom" {
				event.Item.Type = "custom_tool_call"
				event.Item.Input = extractCustomToolCallInput(call.arguments.String())
				event.Item.Arguments = ""
				event.Item.Namespace = ""
			} else {
				event.Item.Type = "tool_search_call"
				event.Item.Name = ""
				event.Item.Arguments = call.arguments.String()
				if strings.TrimSpace(event.Item.Arguments) == "" {
					event.Item.Arguments = "{}"
				}
				event.Item.Namespace = ""
			}
			r.deleteCall(call)
		}
		emit(r.restoreNamespaceEvent(event))
	default:
		// response.completed carries the non-stream representation.
		if event.Response != nil {
			restoreResponsesOutputClientTools(event.Response.Output, &r.adapter)
		}
		emit(r.restoreNamespaceEvent(event))
	}
	return out
}

// RestoreEvent restores one Responses SSE JSON data payload. Custom tool
// completions can expand to multiple payloads and proxy argument deltas can be
// intentionally dropped, hence the slice return value.
func (r *ResponsesClientToolStreamRestorer) RestoreEvent(payload []byte) ([][]byte, bool, error) {
	if len(payload) == 0 {
		return nil, false, nil
	}
	var wire struct {
		Type     string `json:"type"`
		Sequence int    `json:"sequence_number"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, false, err
	}
	if isResponsesClientToolTerminalEvent(wire.Type) {
		restored, changed, err := RestoreResponsesClientToolPayload(payload, r.adapter)
		if err != nil {
			return nil, false, err
		}
		return r.resequenceRaw(restored, wire.Sequence, changed)
	}
	if !clientToolLifecycleEvent(wire.Type) {
		return r.resequenceRaw(payload, wire.Sequence, false)
	}
	if !r.clientToolEventPayload(payload) {
		return r.resequenceRaw(payload, wire.Sequence, false)
	}
	var event ResponsesStreamEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, false, err
	}
	events := r.Restore(event)
	if r.conversionErr != nil {
		return nil, false, r.conversionErr
	}
	if len(events) == 1 {
		unchanged, err := json.Marshal(events[0])
		if err == nil && bytes.Equal(bytes.TrimSpace(unchanged), bytes.TrimSpace(payload)) {
			return [][]byte{payload}, false, nil
		}
	}
	result := make([][]byte, 0, len(events))
	for _, restored := range events {
		encoded, err := json.Marshal(restored)
		if err != nil {
			return nil, false, err
		}
		result = append(result, encoded)
	}
	return result, true, nil
}

func (r *ResponsesClientToolStreamRestorer) clientToolEventPayload(payload []byte) bool {
	var raw struct {
		ItemID      string `json:"item_id"`
		CallID      string `json:"call_id"`
		Name        string `json:"name"`
		OutputIndex int    `json:"output_index"`
		Item        *struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
		} `json:"item"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return false
	}
	if raw.Item != nil {
		switch raw.Item.Type {
		case "function_call":
			_, namespaceTool := r.adapter.NamespaceTools[raw.Item.Name]
			return r.adapter.CustomTools[raw.Item.Name] || (r.adapter.ToolSearch && raw.Item.Name == toolSearchProxyName) || namespaceTool || r.calls[raw.Item.ID] != nil || r.calls[raw.Item.CallID] != nil
		case "custom_tool_call":
			_, namespaceTool := r.adapter.NamespaceTools[raw.Item.Name]
			return namespaceTool
		default:
			return false
		}
	}
	if _, namespaceTool := r.adapter.NamespaceTools[raw.Name]; namespaceTool {
		return true
	}
	if r.calls[raw.ItemID] != nil || r.calls[raw.CallID] != nil || r.byOutput[raw.OutputIndex] != nil {
		return true
	}
	return false
}

func clientToolLifecycleEvent(typ string) bool {
	switch typ {
	case "response.output_item.added", "response.output_item.done",
		"response.function_call_arguments.delta", "response.function_call_arguments.done",
		"response.custom_tool_call_input.delta", "response.custom_tool_call_input.done":
		return true
	default:
		return false
	}
}

// resequenceRaw deliberately keeps opaque upstream event fields untouched.
func (r *ResponsesClientToolStreamRestorer) resequenceRaw(payload []byte, sequence int, changed bool) ([][]byte, bool, error) {
	if !r.seenSeq {
		r.nextSeq, r.seenSeq = sequence, true
	}
	if r.nextSeq == sequence && !changed {
		r.nextSeq++
		return [][]byte{payload}, false, nil
	}
	encoded, err := sjson.SetBytes(payload, "sequence_number", r.nextSeq)
	if err != nil {
		return nil, false, err
	}
	r.nextSeq++
	return [][]byte{encoded}, true, nil
}

func (r *ResponsesClientToolStreamRestorer) recordItem(event ResponsesStreamEvent) (*responsesClientToolStreamCall, error) {
	if event.Item == nil || event.Item.Type != "function_call" {
		return nil, nil
	}
	name := event.Item.Name
	kind := ""
	if r.adapter.CustomTools[name] {
		kind = "custom"
	} else if r.adapter.ToolSearch && name == toolSearchProxyName {
		kind = "tool_search"
	}
	if kind == "" {
		return nil, nil
	}
	for label, value := range map[string]string{"item id": event.Item.ID, "call id": event.Item.CallID, "tool name": name} {
		if len(value) > maxResponsesClientToolRetainedMetadataBytes {
			return nil, fmt.Errorf("responses client-tool %s exceeds %d-byte retained-metadata limit", label, maxResponsesClientToolRetainedMetadataBytes)
		}
	}
	key := event.Item.ID
	if key == "" {
		key = event.Item.CallID
	}
	call := r.calls[key]
	if call == nil {
		if r.trackedCalls >= maxResponsesClientToolTrackedCalls {
			return nil, fmt.Errorf("responses client-tool stream exceeds %d tracked calls", maxResponsesClientToolTrackedCalls)
		}
		call = &responsesClientToolStreamCall{kind: kind, name: name, callID: event.Item.CallID, itemID: event.Item.ID, outputIdx: event.OutputIndex}
		r.trackedCalls++
		r.calls[key] = call
		if call.callID != "" {
			r.calls[call.callID] = call
		}
		r.byOutput[call.outputIdx] = call
	}
	if event.Item.Arguments != "" {
		if err := r.setCallArguments(call, event.Item.Arguments); err != nil {
			return nil, err
		}
	}
	return call, nil
}

func (r *ResponsesClientToolStreamRestorer) appendCallArguments(call *responsesClientToolStreamCall, fragment string) error {
	if len(fragment) > maxResponsesClientToolRetainedArgumentBytes-r.retainedArgumentBytes {
		return fmt.Errorf("responses client-tool arguments exceed %d-byte retained-state limit", maxResponsesClientToolRetainedArgumentBytes)
	}
	_, _ = call.arguments.WriteString(fragment)
	r.retainedArgumentBytes += len(fragment)
	return nil
}

func (r *ResponsesClientToolStreamRestorer) setCallArguments(call *responsesClientToolStreamCall, value string) error {
	retainedWithoutCall := r.retainedArgumentBytes - call.arguments.Len()
	if len(value) > maxResponsesClientToolRetainedArgumentBytes-retainedWithoutCall {
		return fmt.Errorf("responses client-tool arguments exceed %d-byte retained-state limit", maxResponsesClientToolRetainedArgumentBytes)
	}
	call.arguments.Reset()
	_, _ = call.arguments.WriteString(value)
	r.retainedArgumentBytes = retainedWithoutCall + len(value)
	return nil
}

func (r *ResponsesClientToolStreamRestorer) deleteCall(call *responsesClientToolStreamCall) {
	if call == nil {
		return
	}
	r.retainedArgumentBytes -= call.arguments.Len()
	if r.retainedArgumentBytes < 0 {
		r.retainedArgumentBytes = 0
	}
	delete(r.calls, call.itemID)
	delete(r.calls, call.callID)
	delete(r.byOutput, call.outputIdx)
	if r.trackedCalls > 0 {
		r.trackedCalls--
	}
}

func (r *ResponsesClientToolStreamRestorer) callFor(event ResponsesStreamEvent) *responsesClientToolStreamCall {
	if call := r.calls[event.ItemID]; call != nil {
		return call
	}
	if call := r.byOutput[event.OutputIndex]; call != nil {
		return call
	}
	for _, call := range r.calls {
		if (event.CallID != "" && call.callID == event.CallID) || (event.ItemID == "" && event.Name != "" && call.name == event.Name) {
			return call
		}
	}
	return nil
}

func (r *ResponsesClientToolStreamRestorer) restoreNamespaceEvent(event ResponsesStreamEvent) ResponsesStreamEvent {
	if len(r.adapter.NamespaceTools) == 0 {
		return event
	}
	if event.Item != nil && isNamespaceQualifiedCallType(event.Item.Type) {
		if name, ok := r.adapter.NamespaceTools[event.Item.Name]; ok {
			event.Item.Name, event.Item.Namespace = name.Name, name.Namespace
		}
	}
	switch event.Type {
	case "response.function_call_arguments.delta", "response.function_call_arguments.done",
		"response.custom_tool_call_input.delta", "response.custom_tool_call_input.done":
		if name, ok := r.adapter.NamespaceTools[event.Name]; ok {
			event.Name = name.Name
		}
	}
	return event
}

func restoreResponsesOutputClientTools(outputs []ResponsesOutput, adapter *ResponsesClientToolMapping) {
	for index := range outputs {
		output := &outputs[index]
		if output.Type != "function_call" {
			continue
		}
		if adapter.CustomTools[output.Name] {
			output.Type = "custom_tool_call"
			output.Input = extractCustomToolCallInput(output.Arguments)
			output.Arguments = ""
			output.Namespace = ""
		} else if adapter.ToolSearch && output.Name == toolSearchProxyName {
			output.Type = "tool_search_call"
			output.Name = ""
			output.Namespace = ""
		}
		if isNamespaceQualifiedCallType(output.Type) {
			if name, ok := adapter.NamespaceTools[output.Name]; ok {
				output.Name, output.Namespace = name.Name, name.Namespace
			}
		}
	}
}
