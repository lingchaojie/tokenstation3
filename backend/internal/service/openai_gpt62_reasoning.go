package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// gpt62RequestReasoningEffort runs before lossy conversion/model normalization.
// The actual upstream model qualifies the request; only aliases for that same
// published model can supply an implicit effort. Explicit fields take priority.
func gpt62RequestReasoningEffort(body []byte, upstreamModel string, modelCandidates ...string) (string, error) {
	base := openai.GPT62ModelID(upstreamModel)
	if base == "" {
		return "", nil
	}
	effort := ""
	for _, path := range []string{"reasoning.effort", "reasoning_effort", "output_config.effort"} {
		value := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, path).String()))
		if err := openai.ValidateGPT61ReasoningEffort(base, value); err != nil {
			return "", err
		}
		if effort == "" {
			effort = value
		}
	}
	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()), "disabled") {
		return "", openai.ValidateGPT61ReasoningEffort(base, "none")
	}
	if effort != "" {
		return effort, nil
	}
	for _, model := range append([]string{upstreamModel}, modelCandidates...) {
		if openai.GPT62ModelID(model) != base {
			continue
		}
		suffix := strings.TrimPrefix(openai.CanonicalizeOpenAIModelAliasSpelling(model), base+"-")
		switch suffix {
		case "none", "minimal", "low", "medium", "high", "xhigh", "max":
			if err := openai.ValidateGPT61ReasoningEffort(base, suffix); err != nil {
				return "", err
			}
			return suffix, nil
		}
	}
	return "", nil
}

func normalizeGPT62RequestReasoning(body []byte, upstreamModel, effortPath string, modelCandidates ...string) ([]byte, bool, error) {
	effort, err := gpt62RequestReasoningEffort(body, upstreamModel, modelCandidates...)
	if err != nil {
		return body, false, err
	}
	if effort == "" || gjson.GetBytes(body, effortPath).String() == effort {
		return body, false, nil
	}
	normalized, err := sjson.SetBytes(body, effortPath, effort)
	return normalized, err == nil, err
}
