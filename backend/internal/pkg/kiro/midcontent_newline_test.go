package kiro

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamEventStreamPreservesMidContentBlankLines(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	for _, chunk := range []string{"# Heading", "\n\n", "Body"} {
		_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": chunk},
		}))
	}

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "gpt-5.6-sol", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Contains(t, out.String(), `"text":"\n\nBody"`)
	require.Contains(t, out.String(), `"text":"# Heading"`)
}
