package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	CaptureRuntimePolicyVersion    = 1
	SettingKeyCaptureRuntimePolicy = "capture_runtime_policy"
)

type CaptureOutcome string

const (
	CaptureOutcomeSuccess       CaptureOutcome = "success"
	CaptureOutcomeTerminalError CaptureOutcome = "terminal_error"
)

type CapturePlatformPolicy struct {
	Anthropic   bool `json:"anthropic"`
	Kiro        bool `json:"kiro"`
	OpenAI      bool `json:"openai"`
	Gemini      bool `json:"gemini"`
	Antigravity bool `json:"antigravity"`
	Grok        bool `json:"grok"`
}

type CaptureOutcomePolicy struct {
	Success       bool `json:"success"`
	TerminalError bool `json:"terminal_error"`
}

type CaptureContentPolicy struct {
	RawRequest      bool `json:"raw_request"`
	RawResponse     bool `json:"raw_response"`
	RequestHeaders  bool `json:"request_headers"`
	ResponseHeaders bool `json:"response_headers"`
}

type CaptureRuntimePolicy struct {
	Version   int                   `json:"version"`
	Enabled   bool                  `json:"enabled"`
	Platforms CapturePlatformPolicy `json:"platforms"`
	Outcomes  CaptureOutcomePolicy  `json:"outcomes"`
	Content   CaptureContentPolicy  `json:"content"`
	GroupIDs  []int64               `json:"group_ids"`
	UserIDs   []int64               `json:"user_ids"`
}

func DefaultCaptureRuntimePolicy() CaptureRuntimePolicy {
	return CaptureRuntimePolicy{
		Version: CaptureRuntimePolicyVersion,
		Platforms: CapturePlatformPolicy{
			Anthropic:   true,
			Kiro:        true,
			OpenAI:      false,
			Gemini:      true,
			Antigravity: true,
			Grok:        true,
		},
		Outcomes: CaptureOutcomePolicy{
			Success:       true,
			TerminalError: true,
		},
		Content: CaptureContentPolicy{
			RawRequest:      true,
			RawResponse:     true,
			RequestHeaders:  true,
			ResponseHeaders: true,
		},
		GroupIDs: []int64{},
		UserIDs:  []int64{},
	}
}

func DecodeCaptureRuntimePolicy(data []byte) (CaptureRuntimePolicy, error) {
	var policy CaptureRuntimePolicy
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return CaptureRuntimePolicy{}, fmt.Errorf("decode capture runtime policy: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return CaptureRuntimePolicy{}, fmt.Errorf("decode capture runtime policy: multiple JSON values")
		}
		return CaptureRuntimePolicy{}, fmt.Errorf("decode capture runtime policy trailing data: %w", err)
	}
	return ValidateAndNormalizeCaptureRuntimePolicy(policy)
}

func ValidateAndNormalizeCaptureRuntimePolicy(policy CaptureRuntimePolicy) (CaptureRuntimePolicy, error) {
	if policy.Version != CaptureRuntimePolicyVersion {
		return CaptureRuntimePolicy{}, fmt.Errorf("capture runtime policy version must be %d", CaptureRuntimePolicyVersion)
	}
	groups, err := normalizeCapturePolicyIDs("group_ids", policy.GroupIDs)
	if err != nil {
		return CaptureRuntimePolicy{}, err
	}
	users, err := normalizeCapturePolicyIDs("user_ids", policy.UserIDs)
	if err != nil {
		return CaptureRuntimePolicy{}, err
	}
	policy.GroupIDs = groups
	policy.UserIDs = users
	return policy, nil
}

func normalizeCapturePolicyIDs(field string, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return []int64{}, nil
	}
	unique := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("%s must contain only positive IDs", field)
		}
		unique[id] = struct{}{}
	}
	result := make([]int64, 0, len(unique))
	for id := range unique {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

type CompiledCapturePolicy struct {
	enabled   bool
	platforms CapturePlatformPolicy
	outcomes  CaptureOutcomePolicy
	content   CaptureContentPolicy
	groupIDs  map[int64]struct{}
	userIDs   map[int64]struct{}
}

func CompileCaptureRuntimePolicy(policy CaptureRuntimePolicy) (CompiledCapturePolicy, error) {
	normalized, err := ValidateAndNormalizeCaptureRuntimePolicy(policy)
	if err != nil {
		return CompiledCapturePolicy{}, err
	}
	compiled := CompiledCapturePolicy{
		enabled:   normalized.Enabled,
		platforms: normalized.Platforms,
		outcomes:  normalized.Outcomes,
		content:   normalized.Content,
		groupIDs:  make(map[int64]struct{}, len(normalized.GroupIDs)),
		userIDs:   make(map[int64]struct{}, len(normalized.UserIDs)),
	}
	for _, id := range normalized.GroupIDs {
		compiled.groupIDs[id] = struct{}{}
	}
	for _, id := range normalized.UserIDs {
		compiled.userIDs[id] = struct{}{}
	}
	return compiled, nil
}

func (p CompiledCapturePolicy) Enabled() bool { return p.enabled }

func (p CompiledCapturePolicy) Match(platform string, outcome CaptureOutcome, userID int64, groupID *int64) bool {
	_, ok := p.Decide(platform, outcome, userID, groupID)
	return ok
}

func (p CompiledCapturePolicy) Decide(platform string, outcome CaptureOutcome, userID int64, groupID *int64) (CaptureContentPolicy, bool) {
	if !p.enabled {
		return CaptureContentPolicy{}, false
	}
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "anthropic":
		if !p.platforms.Anthropic {
			return CaptureContentPolicy{}, false
		}
	case "kiro":
		if !p.platforms.Kiro {
			return CaptureContentPolicy{}, false
		}
	case "openai":
		if !p.platforms.OpenAI {
			return CaptureContentPolicy{}, false
		}
	case "gemini":
		if !p.platforms.Gemini {
			return CaptureContentPolicy{}, false
		}
	case "antigravity":
		if !p.platforms.Antigravity {
			return CaptureContentPolicy{}, false
		}
	case "grok":
		if !p.platforms.Grok {
			return CaptureContentPolicy{}, false
		}
	default:
		return CaptureContentPolicy{}, false
	}

	switch outcome {
	case CaptureOutcomeSuccess:
		if !p.outcomes.Success {
			return CaptureContentPolicy{}, false
		}
	case CaptureOutcomeTerminalError:
		if !p.outcomes.TerminalError {
			return CaptureContentPolicy{}, false
		}
	default:
		return CaptureContentPolicy{}, false
	}

	if len(p.groupIDs) > 0 {
		if groupID == nil {
			return CaptureContentPolicy{}, false
		}
		if _, exists := p.groupIDs[*groupID]; !exists {
			return CaptureContentPolicy{}, false
		}
	}
	if len(p.userIDs) > 0 {
		if _, exists := p.userIDs[userID]; !exists {
			return CaptureContentPolicy{}, false
		}
	}
	return p.content, true
}
