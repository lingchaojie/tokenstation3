package cursor

import (
	"sort"
	"strings"
	"testing"
)

func TestBuildAgentHeadersSendsExactlyTenCLIHeaders(t *testing.T) {
	headers := BuildAgentHeaders("jwt-value", "cli-2026.01.01-abcdef0", true, "req-1")
	want := map[string]string{
		"Authorization":            "Bearer jwt-value",
		"Content-Type":             "application/connect+proto",
		"Connect-Protocol-Version": "1",
		"Connect-Accept-Encoding":  "gzip",
		"X-Cursor-Client-Version":  "cli-2026.01.01-abcdef0",
		"X-Cursor-Client-Type":     "cli",
		"X-Ghost-Mode":             "true",
		"X-Request-Id":             "req-1",
		"X-Original-Request-Id":    "req-1",
		"User-Agent":               "connect-es/1.6.1",
	}

	if len(headers) != 10 {
		got := make([]string, 0, len(headers))
		for name := range headers {
			got = append(got, name)
		}
		sort.Strings(got)
		t.Fatalf("header count = %d, want exactly 10: %v", len(headers), got)
	}
	for name, value := range want {
		if got := headers.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	for _, forbidden := range []string{
		"x-cursor-checksum", "x-client-key", "x-session-id",
		"x-cursor-client-os", "x-cursor-client-arch", "x-cursor-client-device-type",
		"x-cursor-timezone", "x-new-onboarding-completed", "x-cursor-config-version",
	} {
		if headers.Get(forbidden) != "" {
			t.Errorf("%s must not be sent to AgentService", forbidden)
		}
	}
}

func TestBuildAgentHeadersDefaultsAndCookieTokens(t *testing.T) {
	for name, token := range map[string]string{
		"decoded": "user_123::jwt-value",
		"escaped": "user_123%3A%3Ajwt-value",
	} {
		headers := BuildAgentHeaders(token, "", false, "")
		if got := headers.Get("Authorization"); got != "Bearer jwt-value" {
			t.Errorf("%s authorization = %q", name, got)
		}
		if got := headers.Get("X-Cursor-Client-Version"); got != "cli-2026.08.11-e8db854" {
			t.Errorf("default client version = %q", got)
		}
		if got := headers.Get("X-Ghost-Mode"); got != "false" {
			t.Errorf("ghost mode = %q", got)
		}
		requestID := headers.Get("X-Request-Id")
		if requestID == "" || headers.Get("X-Original-Request-Id") != requestID {
			t.Errorf("request ids = %q/%q", requestID, headers.Get("X-Original-Request-Id"))
		}
	}
}

func TestDefaultCLIClientVersionAndParser(t *testing.T) {
	if DefaultCLIClientVersion != "cli-2026.08.11-e8db854" {
		t.Fatalf("DefaultCLIClientVersion = %q", DefaultCLIClientVersion)
	}
	script := "#!/bin/sh\nVERSION=cli-2026.08.11-e8db854\n"
	if got := ParseCLIVersionFromInstallScript(script); got != DefaultCLIClientVersion {
		t.Errorf("parsed version = %q", got)
	}
	if got := ParseCLIVersionFromInstallScript("echo 2.6.22"); got != "" {
		t.Errorf("legacy version parsed as %q", got)
	}
	if !strings.HasPrefix(DefaultCLIClientVersion, "cli-") {
		t.Errorf("default version is not a CLI build: %q", DefaultCLIClientVersion)
	}
}

func TestAgentRunURL(t *testing.T) {
	const endpoint = "/agent.v1.AgentService/Run"
	for name, tc := range map[string]struct{ in, want string }{
		"default": {"", "https://agentn.global.api5.cursor.sh" + endpoint},
		"plain":   {"https://agent.api5.cursor.sh", "https://agent.api5.cursor.sh" + endpoint},
		"padded":  {" https://agentn.us.api5.cursor.sh/ ", "https://agentn.us.api5.cursor.sh" + endpoint},
	} {
		if got := AgentRunURL(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
