package service

import (
	"strings"

	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

// SanitizeStoredCredentials normalizes provider credential sources and strips
// secrets that must never be persisted after conversion to OAuth tokens.
// Call from admin create/update/import/apply-oauth paths.
//
// Cookie is always stripped: bulk paths may pass an empty platform label, and
// session-jar residue must never sit next to OAuth tokens on any platform.
// The platform argument is retained for call-site clarity / future scrubbing.
func SanitizeStoredCredentials(platform string, creds map[string]any) map[string]any {
	if creds == nil {
		return nil
	}
	if platform == PlatformCursor {
		accessToken, _ := creds["access_token"].(string)
		if cursorpkg.IsUserAPIKey(accessToken) {
			existingAPIKey, _ := creds["api_key"].(string)
			if strings.TrimSpace(existingAPIKey) == "" {
				creds["api_key"] = strings.TrimSpace(accessToken)
			}
			delete(creds, "access_token")
		}
	}
	for _, key := range []string{
		"password", "sso_token", "sso", "sso-rw", "clearTextPassword", "cookie",
	} {
		delete(creds, key)
	}
	return creds
}
