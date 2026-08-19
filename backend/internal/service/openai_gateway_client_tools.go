package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIResponsesClientToolMappingContextKey = "openai_responses_client_tool_mapping"

func hasOpenAIResponsesClientToolMapping(mapping apicompat.ResponsesClientToolMapping) bool {
	return len(mapping.CustomTools) > 0 || mapping.ToolSearch || len(mapping.NamespaceTools) > 0
}

func adaptOpenAIResponsesClientTools(body []byte) ([]byte, apicompat.ResponsesClientToolMapping, error) {
	if !needsOpenAIResponsesClientToolAdaptation(body) {
		return body, apicompat.ResponsesClientToolMapping{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var requestBody map[string]any
	if err := decoder.Decode(&requestBody); err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, fmt.Errorf("decode OpenAI Responses client tools: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return body, apicompat.ResponsesClientToolMapping{}, fmt.Errorf("decode OpenAI Responses client tools trailing data: %w", err)
	}
	mapping, changed, err := apicompat.AdaptResponsesClientTools(requestBody)
	if err != nil || !changed {
		return body, mapping, err
	}
	rebuilt, err := marshalOpenAIUpstreamJSON(requestBody)
	if err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, fmt.Errorf("encode OpenAI Responses client tools: %w", err)
	}
	return rebuilt, mapping, nil
}

func needsOpenAIResponsesClientToolAdaptation(body []byte) bool {
	needed := false
	var visit func(gjson.Result)
	visit = func(value gjson.Result) {
		if needed {
			return
		}
		if value.IsObject() {
			switch strings.TrimSpace(value.Get("type").String()) {
			case "custom", "custom_tool_call", "custom_tool_call_output", "tool_search", "tool_search_call", "tool_search_output":
				needed = true
				return
			}
		}
		if value.IsObject() || value.IsArray() {
			value.ForEach(func(_, child gjson.Result) bool { visit(child); return !needed })
		}
	}
	visit(gjson.ParseBytes(body))
	return needed
}

func openAIResponsesClientToolMapping(c *gin.Context) (apicompat.ResponsesClientToolMapping, bool) {
	if c == nil {
		return apicompat.ResponsesClientToolMapping{}, false
	}
	v, ok := c.Get(openAIResponsesClientToolMappingContextKey)
	m, typed := v.(apicompat.ResponsesClientToolMapping)
	return m, ok && typed && hasOpenAIResponsesClientToolMapping(m)
}

func clearOpenAIResponsesClientToolMapping(c *gin.Context) {
	if c != nil {
		c.Set(openAIResponsesClientToolMappingContextKey, apicompat.ResponsesClientToolMapping{})
	}
}
