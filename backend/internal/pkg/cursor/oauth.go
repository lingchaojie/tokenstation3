package cursor

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	EndpointExchangeUserAPIKey = "/auth/exchange_user_api_key"
	EndpointOAuthToken         = "/oauth/token"
	EndpointAuthPoll           = "/auth/poll"
	DeepLinkLoginURL           = "https://cursor.com/loginDeepControl"
)

const (
	CredentialSourceCookie   = "cookie"
	CredentialSourceAPIKey   = "api_key"
	CredentialSourceDeepLink = "deep_link"
)

type TokenResponse struct {
	AccessToken  string
	RefreshToken string
	AuthID       string
	ExpiresIn    int64
}

func (t *TokenResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		AccessToken      string `json:"accessToken"`
		AccessTokenSnake string `json:"access_token"`
		RefreshToken     string `json:"refreshToken"`
		RefreshSnake     string `json:"refresh_token"`
		AuthID           string `json:"authId"`
		AuthIDSnake      string `json:"auth_id"`
		ExpiresIn        int64  `json:"expiresIn"`
		ExpiresInSnake   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	t.AccessToken = firstNonEmptyString(raw.AccessToken, raw.AccessTokenSnake)
	t.RefreshToken = firstNonEmptyString(raw.RefreshToken, raw.RefreshSnake)
	t.AuthID = firstNonEmptyString(raw.AuthID, raw.AuthIDSnake)
	t.ExpiresIn = raw.ExpiresIn
	if t.ExpiresIn == 0 {
		t.ExpiresIn = raw.ExpiresInSnake
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func NewDeepLinkChallenge() (verifier, challenge, id string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("cursor: generate verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	id = uuid.NewString()
	return verifier, challenge, id, nil
}

func BuildLoginDeepControlURL(challenge, id string) string {
	query := url.Values{}
	query.Set("challenge", challenge)
	query.Set("uuid", id)
	query.Set("mode", "login")
	return DeepLinkLoginURL + "?" + query.Encode()
}

func JWTExpiry(raw string) (time.Time, bool) {
	token, _ := ParseToken(raw)
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

func IsUserAPIKey(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "crsr_")
}
