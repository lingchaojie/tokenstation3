package openai

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Official Codex 0.169.0 descriptors captured on 2026-11-17, tracked in
// Wei-Shaw/sub2api at 2af3f1e15. Live account metadata may override them.
//
//go:embed codex_gpt62_sol.json
var CodexGPT62SolMetadata []byte

//go:embed codex_gpt62_astra.json
var CodexGPT62AstraMetadata []byte

// GPT62ModelID recognizes the two published models and local effort/compact
// spellings. Disabled efforts remain identifiable so validation can reject them.
func GPT62ModelID(model string) string {
	canonical := CanonicalizeOpenAIModelAliasSpelling(model)
	for _, base := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		if canonical == base {
			return base
		}
		if suffix, ok := strings.CutPrefix(canonical, base+"-"); ok {
			switch suffix {
			case "none", "minimal", "low", "medium", "high", "xhigh", "max", "openai-compact":
				return base
			}
		}
	}
	return ""
}

func codexGPT62Instructions(metadata []byte) string {
	var descriptor struct {
		ModelMessages struct {
			InstructionsTemplate string `json:"instructions_template"`
		} `json:"model_messages"`
	}
	if err := json.Unmarshal(metadata, &descriptor); err != nil {
		panic(err)
	}
	return descriptor.ModelMessages.InstructionsTemplate
}

// RequiresResponsesForToolCalls identifies the models whose reasoning tool
// calls cannot be sent to a Chat-Completions-only upstream.
func RequiresResponsesForToolCalls(model string) bool {
	return GPT62ModelID(model) != "" || IsGPT61SolModelSpelling(model)
}

// ValidateGPTReasoningEffort retains the existing Sol scale for GPT-6.2.
func ValidateGPTReasoningEffort(model, effort string) error {
	if base := GPT62ModelID(model); base != "" {
		switch strings.ToLower(strings.TrimSpace(effort)) {
		case "none", "minimal":
			return fmt.Errorf("%s does not support reasoning effort %q; use low, medium, high, xhigh or max", base, effort)
		}
		return nil
	}
	return ValidateGPT61SolReasoningEffort(model, effort)
}
