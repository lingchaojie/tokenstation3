package service

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/stretchr/testify/require"
)

func cursorAgentEvents(events ...cursorpkg.AgentEvent) <-chan cursorpkg.AgentEvent {
	ch := make(chan cursorpkg.AgentEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch
}

func TestConsumeCursorAgentEventsHandlesContentControlUsageAndTerminal(t *testing.T) {
	var deltas []cursorDelta
	start := time.Now().Add(-25 * time.Millisecond)
	outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventHeartbeat},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTokenDelta, Usage: &cursorpkg.AgentUsage{OutputTokens: 2}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventType(999), Text: "must be ignored"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventThinking, Text: "reason"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "answer"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventThinkingEnd, Usage: &cursorpkg.AgentUsage{ThinkingDurationMs: 12}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true, Usage: &cursorpkg.AgentUsage{
			InputTokens: 11, OutputTokens: 7, CacheReadTokens: 3, CacheWriteTokens: 2,
		}},
	), start, 0, func(delta cursorDelta) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "answer", outcome.content)
	require.Equal(t, "reason", outcome.reasoning)
	require.Equal(t, "stop", outcome.finishReason)
	require.True(t, outcome.providerTerminal)
	require.EqualValues(t, 2, outcome.tokenDeltaOutput)
	require.NotNil(t, outcome.usage)
	require.NotNil(t, outcome.firstTokenMs)
	require.GreaterOrEqual(t, *outcome.firstTokenMs, 0)
	require.Len(t, deltas, 2)
	require.Equal(t, cursorDeltaReasoning, deltas[0].kind)
	require.Equal(t, cursorDeltaText, deltas[1].kind)
}

func TestConsumeCursorAgentEventsTerminalTruthRequiresProviderTurnEndedEvent(t *testing.T) {
	t.Run("explicit provider turn ended without usage is terminal", func(t *testing.T) {
		outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
			cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true},
		), time.Now(), 0, nil)
		require.NoError(t, err)
		require.True(t, outcome.providerTerminal)
		require.Nil(t, outcome.usage)
	})

	t.Run("synthetic turn ended is not terminal", func(t *testing.T) {
		outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
			cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded},
		), time.Now(), 0, nil)
		require.NoError(t, err)
		require.False(t, outcome.providerTerminal)
		require.Nil(t, outcome.usage)
	})

	t.Run("channel eof is not terminal", func(t *testing.T) {
		outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
			cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "prefix"},
		), time.Now(), 0, nil)
		require.NoError(t, err)
		require.False(t, outcome.providerTerminal)
		require.Equal(t, "prefix", outcome.content)
	})

	t.Run("usage facts without turn ended are not terminal", func(t *testing.T) {
		outcome := cursorChatOutcome{usage: &cursorpkg.AgentUsage{InputTokens: 4, OutputTokens: 2}}
		require.False(t, outcome.providerTerminal)
		usage := resolveCursorUsage(cursorInputEstimate{text: "fallback"}, outcome)
		require.Equal(t, 4, usage.InputTokens)
		require.Equal(t, 2, usage.OutputTokens)
	})
}

func TestConsumeCursorAgentEventsPreservesPartialOutcomeOnErrorOrCancellation(t *testing.T) {
	for _, terminalErr := range []error{
		&cursorpkg.AgentError{Code: "internal", HTTPStatus: http.StatusBadGateway},
		context.Canceled,
		context.DeadlineExceeded,
		errors.New("reader reset"),
	} {
		outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
			cursorpkg.AgentEvent{Type: cursorpkg.AgentEventThinking, Text: "partial reasoning"},
			cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "partial text"},
			cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{
				ID: "call_partial", Name: "lookup", Arguments: `{"q":"partial"}`,
			}},
			cursorpkg.AgentEvent{Type: cursorpkg.AgentEventError, Err: terminalErr},
		), time.Now(), 0, nil)
		require.ErrorIs(t, err, terminalErr)
		require.Equal(t, "partial reasoning", outcome.reasoning)
		require.Equal(t, "partial text", outcome.content)
		require.Len(t, outcome.toolCalls, 1)
		require.False(t, outcome.providerTerminal)
	}
}

func TestConsumeCursorAgentEventsParallelToolIndexesAreStable(t *testing.T) {
	var deltas []cursorDelta
	outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCallStarted, ToolCall: &cursorpkg.AgentToolCall{ID: "built-in-control"}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCallArgs, Text: "control args", ToolCall: &cursorpkg.AgentToolCall{ID: "built-in-control"}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{
			ID: "call_b", Name: "time", Arguments: `{"tz":"UTC"}`,
		}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{
			ID: "call_a", Name: "weather", Arguments: `{"city":"Paris"}`,
		}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{
			ID: "call_b", Name: "time", Arguments: `{"tz":"UTC"}`,
		}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded},
	), time.Now(), 0, func(delta cursorDelta) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "tool_calls", outcome.finishReason)
	require.Len(t, outcome.toolCalls, 2)
	require.Equal(t, "call_b", outcome.toolCalls[0].ID)
	require.Equal(t, 0, *outcome.toolCalls[0].Index)
	require.Equal(t, "call_a", outcome.toolCalls[1].ID)
	require.Equal(t, 1, *outcome.toolCalls[1].Index)
	require.Len(t, deltas, 2, "duplicate complete calls and partial built-in controls are not replayed")
	require.Equal(t, 0, deltas[0].toolIndex)
	require.Equal(t, 1, deltas[1].toolIndex)
}

func TestConsumeCursorAgentEventsNormalizesToolIdentityBeforeStorageAndDelta(t *testing.T) {
	var deltas []cursorDelta
	outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{
			ID: "call_cursor_0", Name: "preserved", Arguments: `{}`,
		}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{
			Name: "synthesized", Arguments: `{"value":1}`,
		}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{
			ID: "call_empty_name", Name: "   ", Arguments: `{}`,
		}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true},
	), time.Now(), 0, func(delta cursorDelta) error {
		deltas = append(deltas, delta)
		return nil
	})

	require.NoError(t, err)
	require.Len(t, outcome.toolCalls, 2, "an empty public tool name must be suppressed")
	require.Len(t, deltas, 2)
	require.Equal(t, "call_cursor_0", outcome.toolCalls[0].ID, "nonempty upstream IDs remain authoritative")
	require.Equal(t, outcome.toolCalls[0].ID, deltas[0].toolID)
	require.NotEmpty(t, outcome.toolCalls[1].ID)
	require.NotEqual(t, outcome.toolCalls[0].ID, outcome.toolCalls[1].ID,
		"a synthesized-looking upstream ID must not collide with a generated ID")
	require.Equal(t, outcome.toolCalls[1].ID, deltas[1].toolID,
		"normalization must happen once before buffered storage and streaming emission")
}

func TestConsumeCursorAgentEventsConcurrentTurnsDoNotShareToolIndexes(t *testing.T) {
	const turns = 32
	var wg sync.WaitGroup
	errs := make(chan error, turns)
	for turn := 0; turn < turns; turn++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
				cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{ID: "one", Name: "one", Arguments: `{}`}},
				cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{ID: "two", Name: "two", Arguments: `{}`}},
				cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded},
			), time.Now(), 0, nil)
			if err != nil {
				errs <- err
				return
			}
			if len(outcome.toolCalls) != 2 || outcome.toolCalls[0].Index == nil || *outcome.toolCalls[0].Index != 0 ||
				outcome.toolCalls[1].Index == nil || *outcome.toolCalls[1].Index != 1 {
				errs <- errors.New("unstable request-local tool indexes")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestConsumeCursorAgentEventsLocalLimitIsUnicodeSafeAndNotProviderTerminal(t *testing.T) {
	var deltas []cursorDelta
	outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventThinking, Text: "思考"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: strings.Repeat("世界", 100)},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded, Usage: &cursorpkg.AgentUsage{OutputTokens: 999}},
	), time.Now(), 3, func(delta cursorDelta) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.NoError(t, err)
	require.True(t, outcome.truncated)
	require.Equal(t, "length", outcome.finishReason)
	require.False(t, outcome.providerTerminal)
	require.Nil(t, outcome.usage, "unconsumed authoritative usage cannot leak past a local cut")
	require.True(t, utf8.ValidString(outcome.content))
	require.True(t, utf8.ValidString(outcome.reasoning))
	require.LessOrEqual(t, estimateCursorOutputTokens(outcome), 3)
	require.NotEmpty(t, deltas)
}

func TestConsumeCursorAgentEventsDoesNotEmitIndivisibleToolBeyondLocalLimit(t *testing.T) {
	var deltas []cursorDelta
	outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCall, ToolCall: &cursorpkg.AgentToolCall{
			ID: "call_expensive", Name: "expensive_lookup", Arguments: `{"query":"a tool call larger than one token"}`,
		}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTurnEnded, ProviderTerminal: true},
	), time.Now(), 1, func(delta cursorDelta) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.NoError(t, err)
	require.True(t, outcome.truncated)
	require.Equal(t, "length", outcome.finishReason)
	require.False(t, outcome.providerTerminal)
	require.Empty(t, outcome.toolCalls, "an indivisible tool call must not cross the local token ceiling")
	require.Empty(t, deltas)
}

func TestConsumeCursorAgentEventsFirstTokenIsSetOnceAndIgnoresControl(t *testing.T) {
	start := time.Now().Add(-time.Second)
	outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventHeartbeat},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventTokenDelta, Usage: &cursorpkg.AgentUsage{OutputTokens: 1}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventToolCallStarted, ToolCall: &cursorpkg.AgentToolCall{ID: "control"}},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "first"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventThinking, Text: "second"},
	), start, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, outcome.firstTokenMs)
	require.GreaterOrEqual(t, *outcome.firstTokenMs, 900)
	require.Less(t, *outcome.firstTokenMs, 2000)
}

func TestResolveCursorUsageAuthoritativeFallbackAndSaturation(t *testing.T) {
	t.Run("authoritative provider usage including zero", func(t *testing.T) {
		usage := resolveCursorUsage(cursorInputEstimate{text: "large fallback"}, cursorChatOutcome{
			usage: &cursorpkg.AgentUsage{}, providerTerminal: true,
		})
		require.Equal(t, OpenAIUsage{}, usage)
	})

	t.Run("negative provider counts clamp and overflow saturates", func(t *testing.T) {
		usage := resolveCursorUsage(cursorInputEstimate{}, cursorChatOutcome{usage: &cursorpkg.AgentUsage{
			InputTokens: -1, OutputTokens: math.MaxInt64, CacheReadTokens: -9, CacheWriteTokens: math.MaxInt64,
		}})
		require.Zero(t, usage.InputTokens)
		require.Equal(t, int(^uint(0)>>1), usage.OutputTokens)
		require.Zero(t, usage.CacheReadInputTokens)
		require.Equal(t, int(^uint(0)>>1), usage.CacheCreationInputTokens)
	})

	t.Run("fallback includes text reasoning tools images and token deltas", func(t *testing.T) {
		outcome := cursorChatOutcome{
			content: "answer", reasoning: "thinking", tokenDeltaOutput: 100,
			toolCalls: []apicompat.ChatToolCall{{Function: apicompat.ChatFunctionCall{Name: "lookup", Arguments: `{"q":"x"}`}}},
		}
		usage := resolveCursorUsage(cursorInputEstimate{text: "prompt", imageTokens: 1500}, outcome)
		require.Equal(t, estimateTokensForText("prompt")+1500, usage.InputTokens)
		require.Equal(t, 100, usage.OutputTokens, "token deltas dominate a smaller material estimate")
	})

	t.Run("truncated output ignores provider output tail", func(t *testing.T) {
		outcome := cursorChatOutcome{
			content: "delivered", truncated: true,
			usage: &cursorpkg.AgentUsage{InputTokens: 12, OutputTokens: 999},
		}
		usage := resolveCursorUsage(cursorInputEstimate{text: "prompt"}, outcome)
		require.Equal(t, 12, usage.InputTokens)
		require.Equal(t, estimateTokensForText("delivered"), usage.OutputTokens)
	})
}

func TestCursorFitTextToTokenBudgetNeverSplitsUnicode(t *testing.T) {
	text, cost := cursorFitTextToTokenBudget(strings.Repeat("界", 50), 10)
	require.True(t, utf8.ValidString(text))
	require.Equal(t, 10, len([]rune(text)))
	require.LessOrEqual(t, cost, 10)
}

func TestConsumeCursorAgentEventsStopsOnDownstreamErrorWithPartialMetadata(t *testing.T) {
	writeErr := errors.New("client disconnected")
	calls := 0
	outcome, err := consumeCursorAgentEvents(cursorAgentEvents(
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "one"},
		cursorpkg.AgentEvent{Type: cursorpkg.AgentEventText, Text: "two"},
	), time.Now(), 0, func(cursorDelta) error {
		calls++
		return writeErr
	})
	require.ErrorIs(t, err, writeErr)
	require.Equal(t, 1, calls)
	require.Equal(t, "one", outcome.content)
	require.False(t, outcome.providerTerminal)
}

func TestCursorResponsesAndAnthropicChunkSynthesizerMapsRoleReasoningToolsFinishAndUsage(t *testing.T) {
	var chunks []*apicompat.ChatCompletionsChunk
	synth := newCursorChunkSynthesizer("caller-model", func(chunk *apicompat.ChatCompletionsChunk) {
		chunks = append(chunks, chunk)
	})
	synth.onDelta(cursorDelta{kind: cursorDeltaReasoning, text: "think"})
	synth.onDelta(cursorDelta{kind: cursorDeltaText, text: "answer"})
	synth.onDelta(cursorDelta{kind: cursorDeltaToolCall, toolIndex: 1, toolID: "call_1", toolName: "lookup", toolArguments: `{"q":"x"}`})
	synth.finish("tool_calls", OpenAIUsage{InputTokens: 7, OutputTokens: 5, CacheReadInputTokens: 2, CacheCreationInputTokens: 1})

	require.Len(t, chunks, 5)
	require.Equal(t, "assistant", chunks[0].Choices[0].Delta.Role)
	require.Equal(t, "think", *chunks[1].Choices[0].Delta.ReasoningContent)
	require.Equal(t, "answer", *chunks[2].Choices[0].Delta.Content)
	require.Equal(t, 1, *chunks[3].Choices[0].Delta.ToolCalls[0].Index)
	require.Equal(t, "tool_calls", *chunks[4].Choices[0].FinishReason)
	require.Equal(t, 7, chunks[4].Usage.PromptTokens)
	require.Equal(t, 5, chunks[4].Usage.CompletionTokens)
	require.Equal(t, 2, chunks[4].Usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 1, chunks[4].Usage.PromptTokensDetails.CacheWriteTokens)
	for _, chunk := range chunks {
		require.Equal(t, chunks[0].ID, chunk.ID)
		require.Equal(t, "caller-model", chunk.Model)
	}
}

func TestCursorResponsesAndAnthropicChunkSynthesizerEmitsRoleForZeroDeltaSuccess(t *testing.T) {
	var chunks []*apicompat.ChatCompletionsChunk
	synth := newCursorChunkSynthesizer("caller-model", func(chunk *apicompat.ChatCompletionsChunk) {
		chunks = append(chunks, chunk)
	})
	synth.finish("stop", OpenAIUsage{})
	require.Len(t, chunks, 2)
	require.Equal(t, "assistant", chunks[0].Choices[0].Delta.Role)
	require.Equal(t, "stop", *chunks[1].Choices[0].FinishReason)
}
