package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdaptResponsesClientTools_NamespaceCustomChild(t *testing.T) {
	req := map[string]any{
		"model": "gpt-5.6-sol",
		"tools": []any{map[string]any{
			"type": "namespace",
			"name": "functions",
			"tools": []any{map[string]any{
				"type": "custom", "name": "exec", "format": map[string]any{"type": "text"},
			}},
		}},
		"input": []any{map[string]any{
			"type": "custom_tool_call", "namespace": "functions", "name": "exec", "input": "ls",
		}},
	}

	mapping, changed, err := AdaptResponsesClientTools(req)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, mapping.CustomTools["functions__exec"])
	require.Equal(t, ResponsesNamespaceName{Namespace: "functions", Name: "exec"}, mapping.NamespaceTools["functions__exec"])

	tools, ok := req["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "functions__exec", tool["name"])
	require.NotNil(t, tool["parameters"])

	input, ok := req["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)
	history, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "functions__exec", history["name"])
	require.NotContains(t, history, "namespace")

	payload := []byte(`{"type":"response.completed","response":{"output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"functions__exec","arguments":"{\"input\":\"ls\"}"}]}}`)
	restored, restoredChanged, err := RestoreResponsesClientToolPayload(payload, mapping)
	require.NoError(t, err)
	require.True(t, restoredChanged)

	var got struct {
		Response struct {
			Output []ResponsesOutput `json:"output"`
		} `json:"response"`
	}
	require.NoError(t, json.Unmarshal(restored, &got))
	require.Len(t, got.Response.Output, 1)
	require.Equal(t, "custom_tool_call", got.Response.Output[0].Type)
	require.Equal(t, "functions", got.Response.Output[0].Namespace)
	require.Equal(t, "exec", got.Response.Output[0].Name)
	require.Equal(t, "ls", got.Response.Output[0].Input)
}

func TestResponsesClientToolStreamRestorer_NamespaceCustomBridgeEvents(t *testing.T) {
	mapping := ResponsesClientToolMapping{
		CustomTools:    map[string]bool{"functions__exec": true},
		NamespaceTools: map[string]ResponsesNamespaceName{"functions__exec": {Namespace: "functions", Name: "exec"}},
	}
	restorer := NewResponsesClientToolStreamRestorer(mapping)

	itemPayloads, _, err := restorer.RestoreEvent([]byte(
		`{"type":"response.output_item.done","sequence_number":1,"output_index":0,"item":{"type":"custom_tool_call","id":"fc_1","call_id":"call_1","name":"functions__exec","input":"ls"}}`,
	))
	require.NoError(t, err)
	require.Len(t, itemPayloads, 1)
	var item struct {
		Item ResponsesOutput `json:"item"`
	}
	require.NoError(t, json.Unmarshal(itemPayloads[0], &item))
	require.Equal(t, "custom_tool_call", item.Item.Type)
	require.Equal(t, "functions", item.Item.Namespace)
	require.Equal(t, "exec", item.Item.Name)

	inputPayloads, _, err := restorer.RestoreEvent([]byte(
		`{"type":"response.custom_tool_call_input.done","sequence_number":2,"output_index":0,"item_id":"fc_1","call_id":"call_1","name":"functions__exec","input":"ls"}`,
	))
	require.NoError(t, err)
	require.Len(t, inputPayloads, 1)
	var inputEvent struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(inputPayloads[0], &inputEvent))
	require.Equal(t, "exec", inputEvent.Name)
}

func TestResponsesItemWire_NamespaceCustomCall(t *testing.T) {
	wire := responsesItemWire(&ResponsesOutput{
		Type: "custom_tool_call", CallID: "call_1", Name: "exec", Namespace: "functions", Input: "ls",
	})
	require.Equal(t, "functions", wire["namespace"])
}
