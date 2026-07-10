package kiro

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/anthropictokenizer"
)

const (
	kiroEstimatedTokensPerMessage = 4
	kiroEstimatedTokensPerTool    = 8
	kiroEstimatedTokensPerImage   = 1600
)

func estimateKiroPayloadInputTokens(payload KiroPayload) int {
	total := estimateKiroUserMessageTokens(payload.ConversationState.CurrentMessage.UserInputMessage)
	for _, message := range payload.ConversationState.History {
		if message.UserInputMessage != nil {
			total += estimateKiroUserMessageTokens(*message.UserInputMessage)
		}
		if message.AssistantResponseMessage != nil {
			total += estimateKiroAssistantMessageTokens(*message.AssistantResponseMessage)
		}
	}
	return max(total, 1)
}

func estimateKiroUserMessageTokens(message KiroUserInputMessage) int {
	total := kiroEstimatedTokensPerMessage + anthropictokenizer.CountTokens(message.Content)
	total += len(message.Images) * kiroEstimatedTokensPerImage
	if message.UserInputMessageContext == nil {
		return total
	}
	for _, result := range message.UserInputMessageContext.ToolResults {
		total += kiroEstimatedTokensPerTool
		for _, content := range result.Content {
			total += anthropictokenizer.CountTokens(content.Text)
		}
	}
	for _, wrapper := range message.UserInputMessageContext.Tools {
		spec := wrapper.ToolSpecification
		total += kiroEstimatedTokensPerTool
		total += anthropictokenizer.CountTokens(spec.Name)
		total += anthropictokenizer.CountTokens(spec.Description)
		total += countKiroSemanticJSONTokens(spec.InputSchema.JSON)
	}
	return total
}

func estimateKiroAssistantMessageTokens(message KiroAssistantResponseMessage) int {
	total := kiroEstimatedTokensPerMessage + anthropictokenizer.CountTokens(message.Content)
	for _, toolUse := range message.ToolUses {
		total += kiroEstimatedTokensPerTool
		total += anthropictokenizer.CountTokens(toolUse.Name)
		total += countKiroSemanticJSONTokens(toolUse.Input)
	}
	return total
}

func countKiroSemanticJSONTokens(value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return anthropictokenizer.CountTokens(string(encoded))
}
