package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestLargestKiroPayloadFieldUsesOnlyStructuralPaths(t *testing.T) {
	text := strings.Repeat("x", 200)
	args := `{"private-argument-name":"` + strings.Repeat("y", 300) + `"}`
	payload := `{"conversationState":{"history":[{"userInputMessage":{"content":"` + text + `"}},` +
		`{"assistantResponseMessage":{"toolUses":[{"input":` + args + `}]}}]}}`

	path, size := largestKiroPayloadField(gjson.Parse(payload))
	require.Equal(t, "conversationState.history.1.assistantResponseMessage.toolUses.0.input", path)
	require.Equal(t, len(args), size)

	path, size = largestKiroPayloadField(gjson.Parse(`{"conversationState":{"currentMessage":{"userInputMessage":{"content":"` + text + `"}}}}`))
	require.Equal(t, "conversationState.currentMessage.userInputMessage.content", path)
	require.Equal(t, len(text)+2, size)

	path, size = largestKiroPayloadField(gjson.Parse(`{"private-root-field":"` + text + `"}`))
	require.Empty(t, path)
	require.Zero(t, size)
}
