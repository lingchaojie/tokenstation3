package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
)

type WebChatResponseCapture struct {
	gin.ResponseWriter
	body            bytes.Buffer
	maxCaptureBytes int
}

func NewWebChatResponseCapture(w gin.ResponseWriter, maxCaptureBytes int) *WebChatResponseCapture {
	return &WebChatResponseCapture{ResponseWriter: w, maxCaptureBytes: maxCaptureBytes}
}

func (w *WebChatResponseCapture) Write(p []byte) (int, error) {
	w.capture(p)
	return w.ResponseWriter.Write(p)
}

func (w *WebChatResponseCapture) WriteString(s string) (int, error) {
	w.capture([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

func (w *WebChatResponseCapture) Body() []byte {
	return append([]byte(nil), w.body.Bytes()...)
}

func (w *WebChatResponseCapture) capture(p []byte) {
	if w.maxCaptureBytes <= 0 || w.body.Len() >= w.maxCaptureBytes {
		return
	}
	remaining := w.maxCaptureBytes - w.body.Len()
	if len(p) > remaining {
		_, _ = w.body.Write(p[:remaining])
		return
	}
	_, _ = w.body.Write(p)
}

type WebChatArtifactCandidate struct {
	Filename    string
	ContentType string
	Body        []byte
	Source      string
}

func ExtractAssistantTextFromChatCompletions(body []byte, streamed bool) string {
	if streamed {
		var b strings.Builder
		scanner := bufio.NewScanner(bytes.NewReader(body))
		maxTokenSize := len(body)
		if maxTokenSize < 64<<10 {
			maxTokenSize = 64 << 10
		}
		if maxTokenSize > 4<<20 {
			maxTokenSize = 4 << 20
		}
		scanner.Buffer(make([]byte, 0, 64<<10), maxTokenSize)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			var chunk chatCompletionChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil || len(chunk.Choices) == 0 {
				continue
			}
			b.WriteString(chunk.Choices[0].Delta.Content)
		}
		return b.String()
	}

	var response chatCompletionResponse
	if err := json.Unmarshal(body, &response); err != nil || len(response.Choices) == 0 {
		return ""
	}
	return chatCompletionContentText(response.Choices[0].Message.Content)
}

func ExtractArtifactsFromChatCompletions(_ []byte, _ bool) []WebChatArtifactCandidate {
	return nil
}

type chatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content any `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func chatCompletionContentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, part := range v {
			partMap, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, _ := partMap["text"].(string); text != "" {
				b.WriteString(text)
			}
		}
		return b.String()
	default:
		return ""
	}
}
