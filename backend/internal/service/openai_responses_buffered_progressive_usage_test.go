package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBufferedOAuthResponsesUsesRealUsage(t *testing.T) {
	const request = `{"model":"gpt-5.4","instructions":"x","input":"hi","stream":false}`
	const progressive = "data: " + `{"type":"response.output_text.delta","delta":"partial","usage":{"input_tokens":8,"output_tokens":1,"total_tokens":9}}` + "\n\n"
	for _, tc := range []struct {
		name    string
		final   string
		event   string
		input   int
		output  int
		credits float64
	}{
		{name: "zero final", final: `,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, input: 8, output: 1},
		{name: "missing final", input: 8, output: 1},
		{name: "real event usage", final: `,"usage":{}`, event: `,"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}`, input: 10, output: 2},
		{name: "real final usage", final: `,"usage":{"input_tokens":9,"output_tokens":3,"total_tokens":12}`, event: `,"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}`, input: 9, output: 3},
		{name: "final credits with empty token snapshot", final: `,"usage":{"input_tokens":0,"output_tokens":0,"_sub2api_kiro_credits":0.25}`, input: 8, output: 1, credits: 0.25},
	} {
		for _, passthrough := range []bool{false, true} {
			for _, status := range []string{"completed", "incomplete", "cancelled", "canceled"} {
				t.Run(fmt.Sprintf("%s/%s/passthrough=%t", tc.name, status, passthrough), func(t *testing.T) {
					frame := fmt.Sprintf(`{"type":"response.%s","response":{"id":"resp_7978","object":"response","status":"%s","output":[]%s}%s}`, status, status, tc.final, tc.event)
					body := progressive + "data: " + frame + "\n\n"
					rec, _, result := runOpenAIOAuthNonStreamRegression(t, passthrough, request, body)
					require.Equal(t, status, gjson.GetBytes(rec.Body.Bytes(), "status").String())
					require.Equal(t, "partial", gjson.GetBytes(rec.Body.Bytes(), "output.0.content.0.text").String())
					require.Equal(t, tc.input, result.Usage.InputTokens)
					require.Equal(t, tc.output, result.Usage.OutputTokens)
					require.InDelta(t, tc.credits, result.Usage.KiroCredits, 0.000001)
					require.True(t, result.CaptureResponseComplete)
				})
			}
		}
	}
}
