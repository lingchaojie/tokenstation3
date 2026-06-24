package service

import "strings"

func schedulablePlatformsForRequest(platform string, hasForcePlatform bool) []string {
	if hasForcePlatform {
		return []string{platform}
	}
	switch platform {
	case PlatformOpenAI:
		return []string{PlatformOpenAI, PlatformKiro}
	case PlatformAnthropic:
		return []string{PlatformAnthropic, PlatformAntigravity, PlatformKiro}
	case PlatformGemini:
		return []string{PlatformGemini, PlatformAntigravity}
	default:
		return []string{platform}
	}
}

func isKiroBridgeAccountAllowed(account *Account, nativePlatform string, requestedModel string) bool {
	if account == nil || !account.IsKiro() {
		return true
	}
	if nativePlatform == PlatformKiro {
		return true
	}
	model := strings.ToLower(strings.TrimSpace(requestedModel))
	if model == "" {
		return false
	}
	if nativePlatform == PlatformAnthropic {
		return isKiroAnthropicMessagesModel(model)
	}
	return accountHasExplicitModelMapping(account, requestedModel)
}

func isKiroAnthropicMessagesModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "auto" ||
		model == "kiro-auto" ||
		strings.HasPrefix(model, "claude-") ||
		strings.HasPrefix(model, "kiro-claude-")
}

func accountHasExplicitModelMapping(account *Account, requestedModel string) bool {
	if account == nil || strings.TrimSpace(requestedModel) == "" {
		return false
	}
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		return false
	}
	return mappingSupportsRequestedModel(mapping, requestedModel)
}

func openAICompatibleAccountMatchesRequestPlatform(ctxPlatform string, account *Account) bool {
	if account == nil {
		return false
	}
	if account.Platform == ctxPlatform {
		return true
	}
	return ctxPlatform == PlatformOpenAI && account.Platform == PlatformKiro
}
