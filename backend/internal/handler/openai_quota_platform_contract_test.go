package handler

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCursorQuotaRequestPlatformAndMessagesDispatch(t *testing.T) {
	cursorKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformCursor}}
	require.Equal(t, service.PlatformCursor, openAICompatibleRequestPlatform(context.Background(), cursorKey))
	require.Equal(t, service.PlatformCursor, openAICompatibleRequestPlatform(
		context.WithValue(context.Background(), ctxkey.ForcePlatform, service.PlatformCursor),
		&service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}},
	))
	require.Equal(t, service.PlatformCursor, openAICompatibleRequestPlatform(
		context.WithValue(context.Background(), ctxkey.Platform, service.PlatformCursor), nil,
	))
	require.Equal(t, service.PlatformOpenAI, openAICompatibleRequestPlatform(
		context.WithValue(context.Background(), ctxkey.ForcePlatform, service.PlatformOpenAI), cursorKey,
	))
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil, cursorKey))
	require.Equal(t, service.PlatformCursor, service.QuotaPlatform(
		context.WithValue(context.Background(), ctxkey.ForcePlatform, service.PlatformCursor), cursorKey,
	))
}

func TestCursorBillingEndpointUsesActualForwardResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	account := &service.Account{Platform: service.PlatformCursor, Type: service.AccountTypeOAuth}

	require.Equal(t, EndpointResponses, DeriveUpstreamEndpoint(EndpointChatCompletions, c.Request.URL.Path, service.PlatformCursor))
	require.Equal(t, "cursor:/agent.v1.AgentService/Run", resolveOpenAIUpstreamEndpoint(c, account, &service.OpenAIForwardResult{
		UpstreamEndpoint: "cursor:/agent.v1.AgentService/Run",
	}))
}

func TestOpenAIRecordUsageInputsCarryQuotaPlatform(t *testing.T) {
	files := []string{
		"openai_gateway_handler.go",
		"openai_chat_completions.go",
		"openai_embeddings.go",
		"openai_images.go",
	}

	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
			require.NoError(t, err)

			var missing []token.Position
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.CompositeLit)
				if !ok || !isOpenAIRecordUsageInputLiteral(literal.Type) {
					return true
				}
				if !compositeLiteralHasKey(literal, "QuotaPlatform") {
					missing = append(missing, fset.Position(literal.Lbrace))
				}
				return true
			})

			require.Empty(t, missing, "OpenAI usage post-billing must receive request-time QuotaPlatform")
		})
	}
}

func isOpenAIRecordUsageInputLiteral(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "service" && selector.Sel.Name == "OpenAIRecordUsageInput"
}

func compositeLiteralHasKey(literal *ast.CompositeLit, key string) bool {
	for _, elt := range literal.Elts {
		pair, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		ident, ok := pair.Key.(*ast.Ident)
		if ok && ident.Name == key {
			return true
		}
	}
	return false
}
