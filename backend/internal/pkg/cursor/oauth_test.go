package cursor

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTokenResponseAcceptsSnakeAndCamelCase(t *testing.T) {
	var got TokenResponse
	require.NoError(t, json.Unmarshal([]byte(`{"access_token":"a","refreshToken":"r","auth_id":"id","expires_in":3600}`), &got))
	require.Equal(t, "a", got.AccessToken)
	require.Equal(t, "r", got.RefreshToken)
	require.Equal(t, "id", got.AuthID)
	require.EqualValues(t, 3600, got.ExpiresIn)
}

func TestDeepLinkChallengeAndURLUsePKCEAndState(t *testing.T) {
	verifier, challenge, id, err := NewDeepLinkChallenge()
	require.NoError(t, err)
	require.NotEmpty(t, verifier)
	require.NotEmpty(t, id)
	sum := sha256.Sum256([]byte(verifier))
	require.Equal(t, base64.RawURLEncoding.EncodeToString(sum[:]), challenge)

	parsed, err := url.Parse(BuildLoginDeepControlURL(challenge, id))
	require.NoError(t, err)
	require.Equal(t, "https", parsed.Scheme)
	require.Equal(t, "cursor.com", parsed.Host)
	require.Equal(t, "/loginDeepControl", parsed.Path)
	require.Equal(t, challenge, parsed.Query().Get("challenge"))
	require.Equal(t, id, parsed.Query().Get("uuid"))
	require.Equal(t, "login", parsed.Query().Get("mode"))
}

func TestJWTExpiryAcceptsCookieAndRejectsMalformedClaims(t *testing.T) {
	exp := time.Now().Add(30 * time.Minute).Unix()
	jwt := makeJWT(t, map[string]any{"sub": "auth0|user_1", "exp": exp})
	for _, raw := range []string{jwt, "user_1::" + jwt, "user_1%3A%3A" + jwt} {
		got, ok := JWTExpiry(raw)
		require.True(t, ok)
		require.Equal(t, exp, got.Unix())
	}

	for _, raw := range []string{"not-a-jwt", makeJWT(t, map[string]any{"exp": 0}), "a.%%%.c"} {
		_, ok := JWTExpiry(raw)
		require.False(t, ok)
	}
}

func TestIsUserAPIKeyDetectsOnlyCRSRPrefix(t *testing.T) {
	require.True(t, IsUserAPIKey("crsr_abc"))
	require.True(t, IsUserAPIKey("  crsr_abc  "))
	require.False(t, IsUserAPIKey("CRSR_abc"))
	require.False(t, IsUserAPIKey("sk-abc"))
}
