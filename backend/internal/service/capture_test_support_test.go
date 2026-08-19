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
	// Service tests exercise capture mechanics across model families; keep the
	// production Anthropic/Kiro model allowlists isolated from these fixtures.
	policy.ModelAllowlists.Anthropic = []string{}
	policy.ModelAllowlists.Kiro = []string{}
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
}
