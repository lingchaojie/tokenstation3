package kiro

import (
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// UpstreamException describes a validated AWS EventStream exception or error.
// The native response capture owns the raw frame. These fields are used for
// classification and must be normalized before inclusion in a client response.
type UpstreamException struct {
	Type    string
	Message string
	Reason  string
	Code    string
}

func (*UpstreamException) Error() string { return "kiro upstream exception" }

const (
	kiroOutputLimitException = "ContentLengthExceededException"
	kiroExceptionFieldLimit  = 4096
)

func (m *eventStreamMessage) upstreamException() *UpstreamException {
	var typ string
	switch m.messageType {
	case "exception":
		typ = m.exceptionType
	case "error":
		typ = m.errorCode
	default:
		return nil
	}

	exc := &UpstreamException{Type: strings.TrimSpace(typ), Message: strings.TrimSpace(m.errorMessage)}
	if gjson.ValidBytes(m.Payload) {
		payload := gjson.ParseBytes(m.Payload)
		if exc.Message == "" {
			exc.Message = firstExceptionPayloadString(payload, "message", "Message", "errorMessage", "error.message")
		}
		if exc.Type == "" {
			exc.Type = firstExceptionPayloadString(payload, "__type", "type", "errorCode", "code", "error.type")
			if i := strings.LastIndexByte(exc.Type, '#'); i >= 0 {
				exc.Type = exc.Type[i+1:]
			}
		}
		exc.Reason = firstExceptionPayloadString(payload, "reason", "error.reason")
		exc.Code = firstExceptionPayloadString(payload, "code", "errorCode", "error.code")
	} else if exc.Message == "" {
		exc.Message = strings.TrimSpace(boundedExceptionField(string(m.Payload[:min(len(m.Payload), kiroExceptionFieldLimit)])))
	}
	if exc.Type == "" {
		exc.Type = "UnknownUpstreamError"
	}
	exc.Type = boundedExceptionField(exc.Type)
	exc.Message = boundedExceptionField(exc.Message)
	return exc
}

func firstExceptionPayloadString(payload gjson.Result, paths ...string) string {
	for _, path := range paths {
		value := payload.Get(path)
		if value.Type == gjson.String {
			if s := strings.TrimSpace(value.String()); s != "" {
				return boundedExceptionField(s)
			}
		}
	}
	return ""
}

func boundedExceptionField(s string) string {
	s = s[:min(len(s), kiroExceptionFieldLimit)]
	return strings.ToValidUTF8(s, string(utf8.RuneError))
}
