package cursor

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultBaseURL          = "https://api2.cursor.sh"
	EndpointAvailableModels = "/aiserver.v1.AiService/AvailableModels"
	EndpointStreamChat      = "/aiserver.v1.ChatService/StreamUnifiedChatWithTools"
	ContentTypeProto        = "application/proto"
	ContentTypeConnectProto = "application/connect+proto"
)

const (
	DefaultClientVersion = "2.6.22"
	DefaultClientType    = "ide"
	DefaultDeviceType    = "desktop"
	DefaultUserAgent     = "connect-es/1.6.1"
	DefaultTimezone      = "UTC"
)

type ClientProfile struct {
	Version             string
	Type                string
	OS                  string
	Arch                string
	OSVersion           string
	DeviceType          string
	Timezone            string
	UserAgent           string
	GhostMode           bool
	OnboardingCompleted bool
	MachineID           string
	MacMachineID        string
}

func DefaultProfile() ClientProfile {
	return ClientProfile{
		Version: DefaultClientVersion, Type: DefaultClientType,
		OS: hostOS(), Arch: hostArch(), DeviceType: DefaultDeviceType,
		Timezone: hostTimezone(), UserAgent: DefaultUserAgent, GhostMode: true,
	}
}

func (p ClientProfile) Resolved() ClientProfile {
	d := DefaultProfile()
	p.Version = firstNonBlank(p.Version, d.Version)
	p.Type = firstNonBlank(p.Type, d.Type)
	p.OS = firstNonBlank(p.OS, d.OS)
	p.Arch = firstNonBlank(p.Arch, d.Arch)
	p.DeviceType = firstNonBlank(p.DeviceType, d.DeviceType)
	p.Timezone = firstNonBlank(p.Timezone, d.Timezone)
	p.UserAgent = firstNonBlank(p.UserAgent, d.UserAgent)
	p.OSVersion = headerSafe(strings.TrimSpace(p.OSVersion))
	p.MachineID = headerSafe(strings.TrimSpace(p.MachineID))
	p.MacMachineID = headerSafe(strings.TrimSpace(p.MacMachineID))
	return p
}

func firstNonBlank(value, fallback string) string {
	if value = headerSafe(strings.TrimSpace(value)); value != "" {
		return value
	}
	return fallback
}

func headerSafe(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if c := value[i]; c >= 0x20 && c < 0x7f {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func hostOS() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

func hostArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return runtime.GOARCH
	}
}

func hostTimezone() string {
	name := time.Local.String()
	if name == "" || name == "Local" {
		return DefaultTimezone
	}
	return name
}

// ParseToken accepts bare JWTs and both decoded and URL-encoded Cursor cookie values.
func ParseToken(raw string) (token string, uid string) {
	raw = decodeCookieSeparator(strings.TrimSpace(raw))
	if raw == "" {
		return "", ""
	}
	if idx := strings.Index(raw, "::"); idx >= 0 {
		uid = strings.TrimSpace(raw[:idx])
		token = strings.TrimSpace(raw[idx+2:])
		if uid == "" {
			uid = ExtractUserID(token)
		}
		return token, uid
	}
	return raw, ExtractUserID(raw)
}

func ExtractUserID(jwt string) string {
	parts := strings.Split(strings.TrimSpace(jwt), ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	sub := strings.TrimSpace(claims.Sub)
	if idx := strings.LastIndex(sub, "|"); idx >= 0 {
		return sub[idx+1:]
	}
	return sub
}

func decodeJWTSegment(segment string) ([]byte, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(segment); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(segment)
}

func sha256hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func machineID(token string) string    { return sha256hex(token + "machineId") }
func macMachineID(token string) string { return sha256hex(token + "macMachineId") }

func (p ClientProfile) machineIDFor(token string) string {
	if id := headerSafe(strings.TrimSpace(p.MachineID)); id != "" {
		return id
	}
	return machineID(token)
}

func (p ClientProfile) macMachineIDFor(token string) string {
	if id := headerSafe(strings.TrimSpace(p.MacMachineID)); id != "" {
		return id
	}
	return macMachineID(token)
}

func (p ClientProfile) Checksum(token string) string { return p.ChecksumAt(token, time.Now()) }

func (p ClientProfile) ChecksumAt(token string, at time.Time) string {
	jwt, _ := ParseToken(token)
	return obfuscatedTimestamp(at) + p.machineIDFor(jwt) + "/" + p.macMachineIDFor(jwt)
}

func ClientKey(token string) string { return sha256hex(token) }

func SessionID(token string) string {
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(token)).String()
}

func Checksum(token string) string { return ChecksumAt(token, time.Now()) }

func ChecksumAt(token string, at time.Time) string {
	return obfuscatedTimestamp(at) + machineID(token) + "/" + macMachineID(token)
}

func obfuscatedTimestamp(at time.Time) string {
	ts := at.UnixMilli() / 1_000_000
	b := []byte{byte(ts >> 40), byte(ts >> 32), byte(ts >> 24), byte(ts >> 16), byte(ts >> 8), byte(ts)}
	key := byte(165)
	for i := range b {
		b[i] = byte((int(b[i]^key) + i%256) & 0xff)
		key = b[i]
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func EncodeChecksumStd(token string, at time.Time) string {
	ts := at.UnixMilli() / 1_000_000
	b := []byte{byte(ts >> 40), byte(ts >> 32), byte(ts >> 24), byte(ts >> 16), byte(ts >> 8), byte(ts)}
	key := byte(165)
	for i := range b {
		b[i] = byte((int(b[i]^key) + i%256) & 0xff)
		key = b[i]
	}
	return base64.RawStdEncoding.EncodeToString(b) + machineID(token) + "/" + macMachineID(token)
}

func BuildHeaders(token, contentType string) http.Header {
	return BuildHeadersWithProfile(token, contentType, DefaultProfile())
}

func BuildHeadersWithProfile(token, contentType string, profile ClientProfile) http.Header {
	jwt, _ := ParseToken(token)
	p := profile.Resolved()
	requestID := uuid.NewString()
	streaming := contentType == ContentTypeConnectProto

	h := make(http.Header)
	h.Set("authorization", "Bearer "+jwt)
	h.Set("content-type", contentType)
	h.Set("connect-protocol-version", "1")
	h.Set("connect-accept-encoding", "gzip")
	if streaming {
		h.Set("connect-content-encoding", "gzip")
		h.Set("x-cursor-streaming", "true")
	}
	h.Set("user-agent", p.UserAgent)
	h.Set("x-cursor-checksum", p.Checksum(jwt))
	h.Set("x-client-key", ClientKey(jwt))
	h.Set("x-session-id", SessionID(jwt))
	h.Set("x-request-id", requestID)
	h.Set("x-amzn-trace-id", "Root="+requestID)
	h.Set("x-cursor-config-version", uuid.NewString())
	h.Set("x-cursor-client-version", p.Version)
	h.Set("x-cursor-client-type", p.Type)
	h.Set("x-cursor-client-os", p.OS)
	h.Set("x-cursor-client-arch", p.Arch)
	h.Set("x-cursor-client-device-type", p.DeviceType)
	if p.OSVersion != "" {
		h.Set("x-cursor-client-os-version", p.OSVersion)
	}
	h.Set("x-cursor-timezone", p.Timezone)
	h.Set("x-ghost-mode", boolHeader(p.GhostMode))
	h.Set("x-new-onboarding-completed", boolHeader(p.OnboardingCompleted))
	return h
}

func boolHeader(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
