package kiro

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKiroToolResultAcceptsInputText(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"exec","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"input_text","text":"command output"}]}]}
		]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "gpt-5.6-sol", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	require.Contains(t, string(result.Payload), "command output")
}
