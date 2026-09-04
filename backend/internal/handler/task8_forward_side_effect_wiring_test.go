package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTask8ProductionForwardersUseOrderedDualSideEffects is a source-level
// wiring guard for the production finalizers. Runtime ordering, panic, nil,
// eligibility, and sync.Once behavior are covered by the side-effect helper
// tests; this guard prevents a production callsite from silently reverting to
// the legacy single callback that mixed capture and usage.
func TestTask8ProductionForwardersUseOrderedDualSideEffects(t *testing.T) {
	want := map[string]struct {
		constructor string
		count       int
	}{
		"gateway_handler.go":                  {constructor: "newGatewayForwardSideEffectSubmitterWithEffects", count: 1},
		"gateway_handler_chat_completions.go": {constructor: "newGatewayForwardSideEffectSubmitterWithEffects", count: 1},
		"gateway_handler_responses.go":        {constructor: "newGatewayForwardSideEffectSubmitterWithEffects", count: 1},
		"openai_gateway_handler.go":           {constructor: "newOpenAIForwardSideEffectSubmitterWithEffects", count: 2},
	}
	legacy := map[string]struct{}{
		"newGatewayForwardSideEffectSubmitter": {},
		"newOpenAIForwardSideEffectSubmitter":  {},
	}

	for filename, expectation := range want {
		t.Run(filename, func(t *testing.T) {
			parsed, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
			require.NoError(t, err)

			constructorCalls := 0
			legacyCalls := 0
			ast.Inspect(parsed, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				ident, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				if ident.Name == expectation.constructor {
					constructorCalls++
				}
				if _, ok := legacy[ident.Name]; ok {
					legacyCalls++
				}
				return true
			})

			require.Equal(t, expectation.count, constructorCalls)
			require.Zero(t, legacyCalls)
		})
	}
}
