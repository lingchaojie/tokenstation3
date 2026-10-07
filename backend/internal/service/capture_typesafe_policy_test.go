package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestB8deTypeSafeCaptureRequiresExplicitOptIn(t *testing.T) {
	defaults := DefaultCaptureRuntimePolicy()
	defaults.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(defaults)
	require.NoError(t, err)
	require.False(t, compiled.Match("typesafe", CaptureOutcomeSuccess, 9, nil))

	for _, platforms := range []string{`{"grok":true}`, `{"grok":true,"typesafe":false}`, `{"grok":true,"typesafe":true}`} {
		t.Run(platforms, func(t *testing.T) {
			policy, err := DecodeCaptureRuntimePolicy([]byte(`{"version":1,"enabled":true,"platforms":` + platforms + `,"outcomes":{"success":true},"content":{"raw_response":true},"user_ids":[9],"group_ids":[7]}`))
			require.NoError(t, err)
			// Exercise the persisted representation, not a second copy of the matcher.
			encoded, err := json.Marshal(policy)
			require.NoError(t, err)
			restored, err := DecodeCaptureRuntimePolicy(encoded)
			require.NoError(t, err)
			compiled, err := CompileCaptureRuntimePolicy(restored)
			require.NoError(t, err)
			group, otherGroup := int64(7), int64(8)
			want := platforms == `{"grok":true,"typesafe":true}`
			content, allowed := compiled.DecideForModel("typesafe", "jev-latest", CaptureOutcomeSuccess, 9, &group)
			require.Equal(t, want, allowed)
			if want {
				require.Equal(t, CaptureContentPolicy{RawResponse: true}, content)
			}
			require.False(t, compiled.Match("typesafe", CaptureOutcomeSuccess, 8, &group))
			require.False(t, compiled.Match("typesafe", CaptureOutcomeSuccess, 9, &otherGroup))
			require.False(t, compiled.Match("typesafe", CaptureOutcomeSuccess, 9, nil))
			require.False(t, compiled.Match("typesafe", CaptureOutcomeTerminalError, 9, &group))
			require.Equal(t, want, compiled.Match("typesafe", captureOutcomeClientDisconnect, 9, &group))
			require.True(t, compiled.Match("grok", CaptureOutcomeSuccess, 9, &group), "existing platforms are independent")
			restored.Enabled = false
			compiled, err = CompileCaptureRuntimePolicy(restored)
			require.NoError(t, err)
			require.False(t, compiled.Match("typesafe", CaptureOutcomeSuccess, 9, &group))
		})
	}
}
