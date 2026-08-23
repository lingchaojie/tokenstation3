package cursor

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	DefaultAgentBaseURL     = "https://agentn.global.api5.cursor.sh"
	AgentBaseURLDirect      = "https://agent.api5.cursor.sh"
	AgentBaseURLRegionUS    = "https://agentn.us.api5.cursor.sh"
	EndpointAgentRun        = "/agent.v1.AgentService/Run"
	EndpointGetUsableModels = "/agent.v1.AgentService/GetUsableModels"
	AgentClientType         = "cli"
	AgentAcceptEncoding     = "gzip"
	ConnectProtocolVersion  = "1"
)

const (
	agentConnectContentType = "application/connect+proto"
	agentUserAgent          = "connect-es/1.6.1"
)

var DefaultCLIClientVersion = "cli-2026.08.11-e8db854"

var cliVersionPattern = regexp.MustCompile(`cli-20\d{2}\.\d{2}\.\d{2}-[0-9a-f]{7,40}`)

func ParseCLIVersionFromInstallScript(script string) string {
	return cliVersionPattern.FindString(script)
}

func AgentRunURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultAgentBaseURL
	}
	return base + EndpointAgentRun
}

// BuildAgentHeaders emits the captured CLI's exact ten-header set. In
// particular it does not add the desktop/api2 machine identity block.
func BuildAgentHeaders(token, clientVersion string, ghost bool, requestID string) http.Header {
	if clientVersion = strings.TrimSpace(clientVersion); clientVersion == "" {
		clientVersion = DefaultCLIClientVersion
	}
	if requestID = strings.TrimSpace(requestID); requestID == "" {
		requestID = uuid.NewString()
	}

	headers := make(http.Header, 10)
	headers.Set("authorization", "Bearer "+agentBearerToken(token))
	headers.Set("content-type", agentConnectContentType)
	headers.Set("connect-protocol-version", ConnectProtocolVersion)
	headers.Set("connect-accept-encoding", AgentAcceptEncoding)
	headers.Set("x-cursor-client-version", clientVersion)
	headers.Set("x-cursor-client-type", AgentClientType)
	headers.Set("x-ghost-mode", strconv.FormatBool(ghost))
	headers.Set("x-request-id", requestID)
	headers.Set("x-original-request-id", requestID)
	headers.Set("user-agent", agentUserAgent)
	return headers
}

// agentBearerToken accepts both a bare JWT and Cursor's userId::JWT cookie
// form, including the browser's percent-encoded separator.
func agentBearerToken(raw string) string {
	raw = strings.TrimSpace(raw)
	const encodedSeparator = "%3A%3A"
	for index := 0; index+len(encodedSeparator) <= len(raw); index++ {
		if strings.EqualFold(raw[index:index+len(encodedSeparator)], encodedSeparator) {
			raw = raw[:index] + "::" + raw[index+len(encodedSeparator):]
			break
		}
	}
	if index := strings.Index(raw, "::"); index >= 0 {
		return strings.TrimSpace(raw[index+2:])
	}
	return raw
}
