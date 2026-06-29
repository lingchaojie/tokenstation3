//go:build unit

package handler

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKiroReferenceHandlersAttachFullGroupToParsedRequest(t *testing.T) {
	files := []string{
		"gateway_handler.go",
		"gateway_handler_chat_completions.go",
		"gateway_handler_responses.go",
	}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			src, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Contains(t, string(src), "parsedReq.GroupID = apiKey.GroupID")
			require.Contains(t, string(src), "parsedReq.Group = apiKey.Group")

			groupIDIndex := strings.Index(string(src), "parsedReq.GroupID = apiKey.GroupID")
			groupIndex := strings.Index(string(src), "parsedReq.Group = apiKey.Group")
			require.Greater(t, groupIndex, groupIDIndex, "full group should be attached right after group id")
		})
	}
}
