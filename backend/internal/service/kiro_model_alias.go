package service

import (
	"regexp"
	"strings"
)

var kiroClaudeVersionAliasPattern = regexp.MustCompile(`^claude-(?:opus|sonnet|haiku|fable|mythos)-[0-9]+(?:[.-][0-9]+)*(?:-thinking)?$`)

// resolveKiroClaudeAlias matches the alternate spelling of an explicitly
// configured model key after exact and wildcard matching. Mapping targets can
// name private upstream models, so only model keys grant access.
func resolveKiroClaudeAlias(mapping map[string]string, requested string) (string, bool) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if !kiroClaudeVersionAliasPattern.MatchString(requested) {
		return "", false
	}
	alias := strings.ReplaceAll(requested, ".", "-")
	chosen := ""
	for key := range mapping {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if !kiroClaudeVersionAliasPattern.MatchString(normalized) || strings.ReplaceAll(normalized, ".", "-") != alias {
			continue
		}
		if chosen == "" || key < chosen {
			chosen = key
		}
	}
	if chosen == "" {
		return "", false
	}
	return mapping[chosen], true
}
