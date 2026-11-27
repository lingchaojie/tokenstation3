package kiro

import (
	"bytes"
	"container/list"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
)

// Our compatibility signature uses a fixed protobuf layout. Older Claude
// clients can send it back verbatim, but it is not an upstream signature.
func isSyntheticThinkingSignature(value string) bool {
	const syntheticSize = 167
	if len(value) != base64.StdEncoding.EncodedLen(syntheticSize) {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(raw) == syntheticSize &&
		bytes.HasPrefix(raw, []byte{0x12, 0xa4, 0x01, 0x0a, 0x02, 0x18, 0x02, 0x12, 0x0c}) &&
		raw[21] == 0x1a && raw[22] == 0x20 && raw[55] == 0x22 && raw[56] == 0x6e
}

const signatureCacheMaxSize = 1000

type sigCacheEntry struct {
	key   string
	value string
}

var (
	signatureLRU      = list.New()
	signatureCacheMap = make(map[string]*list.Element)
	signatureCacheMu  sync.Mutex
)

func thinkingSignature(content, model, messageID string) string {
	if content == "" {
		return ""
	}

	cacheKey := hashKey(content + ":" + model + ":" + messageID)

	signatureCacheMu.Lock()
	if elem, ok := signatureCacheMap[cacheKey]; ok {
		signatureLRU.MoveToFront(elem)
		signatureCacheMu.Unlock()
		if entry, ok := elem.Value.(*sigCacheEntry); ok && entry != nil {
			return entry.value
		}
		return ""
	}
	signatureCacheMu.Unlock()

	sig := generateClaudeSignature(content, model, messageID)

	signatureCacheMu.Lock()
	for signatureLRU.Len() >= signatureCacheMaxSize {
		if oldest := signatureLRU.Back(); oldest != nil {
			if entry, ok := oldest.Value.(*sigCacheEntry); ok && entry != nil {
				delete(signatureCacheMap, entry.key)
			}
			signatureLRU.Remove(oldest)
		}
	}
	entry := &sigCacheEntry{key: cacheKey, value: sig}
	elem := signatureLRU.PushFront(entry)
	signatureCacheMap[cacheKey] = elem
	signatureCacheMu.Unlock()

	return sig
}

func generateClaudeSignature(thinkingContent, model, messageID string) string {
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}

	keyMaterial := deriveSignatureKey(model, messageID)
	mac := hmac.New(sha256.New, keyMaterial)
	_, _ = mac.Write([]byte(thinkingContent))
	hmacResult := mac.Sum(nil)

	fillerKey := hmac.New(sha256.New, keyMaterial)
	_, _ = fillerKey.Write([]byte("payload"))
	_, _ = fillerKey.Write([]byte(thinkingContent))
	filler := fillerKey.Sum(nil)
	for len(filler) < 110 {
		next := hmac.New(sha256.New, keyMaterial)
		_, _ = next.Write(filler)
		_, _ = next.Write([]byte{byte(len(filler))})
		filler = append(filler, next.Sum(nil)...)
	}
	filler = filler[:110]

	inner := make([]byte, 0, 164)
	inner = append(inner, 0x0a, 0x02, 0x18, 0x02)
	inner = append(inner, 0x12, 0x0c)
	inner = append(inner, nonce...)
	inner = append(inner, 0x1a, 0x20)
	inner = append(inner, hmacResult...)
	inner = append(inner, 0x22, 0x6e)
	inner = append(inner, filler...)

	body := make([]byte, 0, 3+len(inner))
	body = append(body, 0x12, 0xa4, 0x01)
	body = append(body, inner...)
	return base64.StdEncoding.EncodeToString(body)
}

func deriveSignatureKey(model, messageID string) []byte {
	mac := hmac.New(sha256.New, []byte("anthropic-thinking-signature-v2"))
	_, _ = mac.Write([]byte(model))
	_, _ = mac.Write([]byte(":"))
	_, _ = mac.Write([]byte(messageID))
	return mac.Sum(nil)
}

func hashKey(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:16])
}
