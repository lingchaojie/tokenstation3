package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGatewayForwardSideEffectsAtMostOnceAcrossOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		result      *service.ForwardResult
		wantUsage   int
		wantCapture int
	}{
		{name: "pre-output failover", result: nil, wantUsage: 0, wantCapture: 0},
		{name: "committed partial", result: &service.ForwardResult{CaptureResponse: []byte("partial")}, wantUsage: 1, wantCapture: 1},
		{name: "success", result: &service.ForwardResult{CaptureResponse: []byte("complete")}, wantUsage: 1, wantCapture: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageCalls := 0
			captureCalls := 0
			submitter := newGatewayForwardSideEffectSubmitter(func(result *service.ForwardResult) {
				usageCalls++
				if result.CaptureResponse != nil {
					captureCalls++
				}
			})

			submitter.Submit(tt.result)
			submitter.Submit(tt.result)

			require.Equal(t, tt.wantUsage, usageCalls)
			require.Equal(t, tt.wantCapture, captureCalls)
		})
	}
}

func TestGatewayForwardSideEffectsCallbackFailureIsNotRetriedInProcess(t *testing.T) {
	calls := 0
	submitter := newGatewayForwardSideEffectSubmitter(func(*service.ForwardResult) {
		calls++
		panic("usage/capture callback failed")
	})

	require.Panics(t, func() { submitter.Submit(&service.ForwardResult{}) })
	require.NotPanics(t, func() { submitter.Submit(&service.ForwardResult{}) })
	require.Equal(t, 1, calls)
}

func TestOpenAIForwardSideEffectsRepeatedFinalizeInvokesCallbackAtMostOnce(t *testing.T) {
	calls := 0
	submitter := newOpenAIForwardSideEffectSubmitter(func(*service.OpenAIForwardResult) { calls++ })
	result := &service.OpenAIForwardResult{}

	submitter.Submit(result)
	submitter.Submit(result)

	require.Equal(t, 1, calls)
}

func TestGatewayForwardCoupledSideEffectsIndependentSpiesAcrossTerminalOutcomes(t *testing.T) {
	tests := []struct {
		name      string
		result    *service.ForwardResult
		wantUsage int
		wantCap   int
	}{
		{name: "pre-output failure", result: nil},
		{name: "upstream failure", result: &service.ForwardResult{UpstreamFailed: true}, wantCap: 1},
		{name: "committed partial", result: &service.ForwardResult{CaptureResponse: []byte("partial")}, wantUsage: 1, wantCap: 1},
		{name: "success", result: &service.ForwardResult{CaptureResponse: []byte("success")}, wantUsage: 1, wantCap: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageCalls, captureCalls := 0, 0
			usage := func(*service.ForwardResult) { usageCalls++ }
			capture := func(*service.ForwardResult) { captureCalls++ }
			submitter := newGatewayForwardSideEffectSubmitterWithEffects(capture, usage, func(result *service.ForwardResult) bool {
				return !result.UpstreamFailed
			})
			submitter.Submit(tt.result)
			submitter.Submit(tt.result)
			require.Equal(t, tt.wantUsage, usageCalls)
			require.Equal(t, tt.wantCap, captureCalls)
			require.LessOrEqual(t, usageCalls, 1)
			require.LessOrEqual(t, captureCalls, 1)
		})
	}
}

func TestGatewayForwardCoupledSideEffectsIndependentFailureMatrix(t *testing.T) {
	t.Run("capture succeeds then usage fails", func(t *testing.T) {
		usageCalls, captureCalls := 0, 0
		submitter := newGatewayForwardSideEffectSubmitterWithEffects(func(*service.ForwardResult) {
			captureCalls++
		}, func(*service.ForwardResult) {
			usageCalls++
			panic("usage failed after capture")
		}, nil)
		result := &service.ForwardResult{}
		require.Panics(t, func() { submitter.Submit(result) })
		require.NotPanics(t, func() { submitter.Submit(result) })
		require.Equal(t, 1, captureCalls)
		require.Equal(t, 1, usageCalls)
	})

	t.Run("capture fails before usage is reachable", func(t *testing.T) {
		usageCalls, captureCalls := 0, 0
		submitter := newGatewayForwardSideEffectSubmitterWithEffects(func(*service.ForwardResult) {
			captureCalls++
			panic("capture failed before usage")
		}, func(*service.ForwardResult) { usageCalls++ }, nil)
		result := &service.ForwardResult{}
		require.Panics(t, func() { submitter.Submit(result) })
		require.NotPanics(t, func() { submitter.Submit(result) })
		require.Equal(t, 1, captureCalls)
		require.Zero(t, usageCalls)
	})
}

func TestOpenAIForwardCoupledSideEffectsIndependentSpies(t *testing.T) {
	tests := []struct {
		name      string
		result    *service.OpenAIForwardResult
		wantUsage int
		wantCap   int
	}{
		{name: "pre-output failure", result: nil},
		{name: "upstream failure", result: &service.OpenAIForwardResult{UpstreamFailed: true}, wantCap: 1},
		{name: "committed partial", result: &service.OpenAIForwardResult{}, wantUsage: 1, wantCap: 1},
		{name: "success", result: &service.OpenAIForwardResult{}, wantUsage: 1, wantCap: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageCalls, captureCalls := 0, 0
			submitter := newOpenAIForwardSideEffectSubmitterWithEffects(
				func(*service.OpenAIForwardResult) { captureCalls++ },
				func(*service.OpenAIForwardResult) { usageCalls++ },
				func(result *service.OpenAIForwardResult) bool { return !result.UpstreamFailed },
			)
			submitter.Submit(tt.result)
			submitter.Submit(tt.result)
			require.Equal(t, tt.wantUsage, usageCalls)
			require.Equal(t, tt.wantCap, captureCalls)
			require.LessOrEqual(t, usageCalls, 1)
			require.LessOrEqual(t, captureCalls, 1)
		})
	}
}
