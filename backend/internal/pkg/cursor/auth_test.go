package cursor

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, err := json.Marshal(claims)
	require.NoError(t, err)
	return header + "." + base64.RawURLEncoding.EncodeToString(payloadJSON) + ".sig"
}

func TestParseTokenNormalizesCredentialForms(t *testing.T) {
	jwt := makeJWT(t, map[string]any{"sub": "auth0|user_999"})

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"decoded cookie", "user_999::" + jwt},
		{"encoded cookie uppercase", "user_999%3A%3A" + jwt},
		{"encoded cookie lowercase", "user_999%3a%3a" + jwt},
		{"bare jwt", jwt},
		{"empty cookie user", "::" + jwt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, uid := ParseToken(tc.raw)
			require.Equal(t, jwt, token)
			require.Equal(t, "user_999", uid)
		})
	}
}

func TestParseTokenRejectsMalformedJWTForDerivedUser(t *testing.T) {
	for _, raw := range []string{"", "opaque", "a.%%%.c", "a." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":7}`)) + ".c"} {
		token, uid := ParseToken(raw)
		require.Equal(t, strings.TrimSpace(raw), token)
		require.Empty(t, uid)
	}
}

func TestChecksumAndHeaderIdentity(t *testing.T) {
	const token = "some-jwt-token"
	checksum := ChecksumAt(token, time.Unix(1_700_000_000, 0))
	require.Equal(t, "paaotEjtc81f350a4c0316e22407600f9647c717b89f7f7d58cb7f008450f32a7cba3167/093f5cd2d20affc90a44ee697754b74925cfe78799accad6cdb0c55442f7e617", checksum)
	require.Len(t, checksum, 137)
	left, right, ok := strings.Cut(checksum, "/")
	require.True(t, ok)
	require.Len(t, left, 72)
	require.Len(t, right, 64)
	_, err := hex.DecodeString(left[8:])
	require.NoError(t, err)
	_, err = hex.DecodeString(right)
	require.NoError(t, err)
	require.Equal(t, machineID(token), left[8:])
	require.Equal(t, macMachineID(token), right)

	jwt := makeJWT(t, map[string]any{"sub": "auth0|user_1"})
	h := BuildHeaders("user_1::"+jwt, ContentTypeConnectProto)
	require.Equal(t, "Bearer "+jwt, h.Get("authorization"))
	require.Equal(t, ContentTypeConnectProto, h.Get("content-type"))
	require.Equal(t, "1", h.Get("connect-protocol-version"))
	require.Equal(t, "gzip", h.Get("connect-accept-encoding"))
	require.Equal(t, "gzip", h.Get("connect-content-encoding"))
	require.Equal(t, "true", h.Get("x-cursor-streaming"))
	require.Equal(t, DefaultUserAgent, h.Get("user-agent"))
	require.Equal(t, DefaultClientVersion, h.Get("x-cursor-client-version"))
	require.Equal(t, DefaultClientType, h.Get("x-cursor-client-type"))
	require.Equal(t, DefaultDeviceType, h.Get("x-cursor-client-device-type"))
	require.Equal(t, "true", h.Get("x-ghost-mode"))
	require.Equal(t, "false", h.Get("x-new-onboarding-completed"))
	require.Equal(t, ClientKey(jwt), h.Get("x-client-key"))
	require.Equal(t, SessionID(jwt), h.Get("x-session-id"))

	requestID := h.Get("x-request-id")
	_, err = uuid.Parse(requestID)
	require.NoError(t, err)
	require.Equal(t, "Root="+requestID, h.Get("x-amzn-trace-id"))
	_, err = uuid.Parse(h.Get("x-cursor-config-version"))
	require.NoError(t, err)
}

func TestBuildHeadersKeepsUnaryAvailableModelsRaw(t *testing.T) {
	h := BuildHeaders(makeJWT(t, map[string]any{"sub": "user_1"}), ContentTypeProto)
	require.Equal(t, ContentTypeProto, h.Get("content-type"))
	require.Empty(t, h.Get("connect-content-encoding"))
	require.Empty(t, h.Get("x-cursor-streaming"))
}

func TestBuildHeadersWithProfileUsesStableMachineIdentity(t *testing.T) {
	p := DefaultProfile()
	p.Version = "3.15.19"
	p.OS = "darwin"
	p.Arch = "arm64"
	p.Timezone = "Asia/Shanghai"
	p.MachineID = "machine-fixed"
	p.MacMachineID = "mac-fixed"

	h := BuildHeadersWithProfile("user::token", ContentTypeConnectProto, p)
	require.Equal(t, "3.15.19", h.Get("x-cursor-client-version"))
	require.Equal(t, "darwin", h.Get("x-cursor-client-os"))
	require.Equal(t, "arm64", h.Get("x-cursor-client-arch"))
	require.Equal(t, "Asia/Shanghai", h.Get("x-cursor-timezone"))
	require.True(t, strings.HasSuffix(h.Get("x-cursor-checksum"), "machine-fixed/mac-fixed"))
}
