package service

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func enableCaptureForTest(t *testing.T, c *gin.Context) {
	t.Helper()
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
}
