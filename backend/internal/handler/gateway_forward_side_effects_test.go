package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGatewayForwardSideEffectsExactOnceAcrossOutcomes(t *testing.T) {
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
