package cursor

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

const MaxImageBytes = 16 << 20

var ErrNotImageDataURI = errors.New("cursor: not a base64 image data URI")

func ParseImageDataURI(uri string) (AgentImage, error) {
	raw := strings.TrimSpace(uri)
	if !strings.HasPrefix(strings.ToLower(raw), "data:") {
		return AgentImage{}, ErrNotImageDataURI
	}
	comma := strings.IndexByte(raw, ',')
	if comma < 0 {
		return AgentImage{}, ErrNotImageDataURI
	}
	header := strings.ToLower(raw[len("data:"):comma])
	payload := raw[comma+1:]
	if !strings.Contains(header, ";base64") {
		return AgentImage{}, ErrNotImageDataURI
	}
	mediaType, _, _ := strings.Cut(header, ";")
	if mediaType != "" && !strings.HasPrefix(mediaType, "image/") {
		return AgentImage{}, fmt.Errorf("cursor: unsupported data URI media type %q: %w", mediaType, ErrNotImageDataURI)
	}
	data, err := decodeImageBase64(payload)
	if err != nil {
		return AgentImage{}, fmt.Errorf("cursor: decode image data URI: %w", err)
	}
	if len(data) == 0 {
		return AgentImage{}, ErrNotImageDataURI
	}
	if len(data) > MaxImageBytes {
		return AgentImage{}, fmt.Errorf("cursor: image is %d bytes, over the %d byte limit", len(data), MaxImageBytes)
	}
	result := AgentImage{Data: data, MimeType: mediaType}
	if config, _, configErr := image.DecodeConfig(bytes.NewReader(data)); configErr == nil {
		result.Width = int32(config.Width)
		result.Height = int32(config.Height)
	}
	return result, nil
}

func decodeImageBase64(payload string) ([]byte, error) {
	cleaned := strings.NewReplacer("\n", "", "\r", "", " ", "", "\t", "").Replace(payload)
	if data, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
		return data, nil
	}
	return base64.RawStdEncoding.DecodeString(strings.TrimRight(cleaned, "="))
}
