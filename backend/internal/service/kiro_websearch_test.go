//go:build unit

package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildKiroWebSearchMCPRequest_UsesUnderscoredMetaKeys(t *testing.T) {
	req := buildKiroWebSearchMCPRequest("golang concurrency")

	body, err := json.Marshal(req)
	require.NoError(t, err)

	require.Equal(t, "tools/call", gjson.GetBytes(body, "method").String())
	require.Equal(t, "web_search", gjson.GetBytes(body, "params.name").String())
	require.Equal(t, "golang concurrency", gjson.GetBytes(body, "params.arguments.query").String())
	require.True(t, gjson.GetBytes(body, "params.arguments._meta._isValid").Bool())
	require.Equal(t, "query", gjson.GetBytes(body, "params.arguments._meta._activePath.0").String())
	require.Equal(t, "query", gjson.GetBytes(body, "params.arguments._meta._completedPaths.0.0").String())
	require.False(t, gjson.GetBytes(body, "params.arguments._meta.isValid").Exists())
	require.False(t, gjson.GetBytes(body, "params.arguments._meta.activePath").Exists())
	require.False(t, gjson.GetBytes(body, "params.arguments._meta.completedPaths").Exists())
}

func TestKiroMCPResponseReadsUseFunctionalCeiling(t *testing.T) {
	short, err := readUpstreamResponseBodyLimited(strings.NewReader("short"), 5)
	require.NoError(t, err)
	require.Equal(t, []byte("short"), short)

	oversized, err := readUpstreamResponseBodyLimited(strings.NewReader("toolong"), 5)
	require.Nil(t, oversized)
	require.ErrorIs(t, err, ErrUpstreamResponseBodyTooLarge)

	readErr := errors.New("forced short read")
	broken := &geminiPartialThenErrorBody{data: []byte("ok"), err: readErr}
	body, err := readUpstreamResponseBodyLimited(broken, 5)
	require.Nil(t, body)
	require.ErrorIs(t, err, readErr)
}

func TestWriteAnthropicMessageStart_UsesCacheEmulationUsage(t *testing.T) {
	var out bytes.Buffer
	err := writeAnthropicMessageStart(&out, "msg_test", "claude-sonnet-4-6", 100, &kiroCacheEmulationUsage{
		InputTokens:              25,
		CacheCreationInputTokens: 75,
		CacheReadInputTokens:     0,
	})
	require.NoError(t, err)
	body := out.String()
	require.Contains(t, body, `"input_tokens":25`)
	require.Contains(t, body, `"cache_creation_input_tokens":75`)
	require.Contains(t, body, `"cache_read_input_tokens":0`)
}
