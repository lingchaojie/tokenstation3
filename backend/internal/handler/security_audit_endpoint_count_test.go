package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSecurityAuditCoverageRemainsAtEstablishedGatewayEndpoints(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	require.True(t, ok)

	counts := map[string]int{}
	for _, name := range []string{"gateway_handler.go", "openai_gateway_handler.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(here), name), nil, 0)
		require.NoError(t, err)
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok {
				counts[selector.Sel.Name]++
			}
			return true
		})
	}

	require.Equal(t, 3, counts["checkSecurityAudit"], "one Messages and two OpenAI HTTP endpoints")
	require.Equal(t, 2, counts["checkSecurityAuditStage"], "first and subsequent WS turns only")
	require.Zero(t, counts["checkContentModeration"], "handlers must use the injected legacy-only coordinator")
}
