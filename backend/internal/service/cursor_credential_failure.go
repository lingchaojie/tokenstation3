package service

import (
	"errors"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
)

const CursorCredentialUnavailableClientMessage = "No healthy Cursor account is currently available"

const CursorClientVersionRejectedClientMessage = "Cursor upstream rejected the advertised client version; update the Cursor client-version setting"

const (
	CursorCredentialReasonMissing        GatewayFailureReason = "cursor_credential_missing"
	CursorCredentialReasonExpired        GatewayFailureReason = "cursor_credential_expired"
	CursorCredentialReasonRefreshFailed  GatewayFailureReason = "cursor_credential_refresh_failed"
	CursorCredentialReasonProviderConfig GatewayFailureReason = "cursor_provider_config"
	CursorCredentialReasonWebSession     GatewayFailureReason = "cursor_credential_web_session"
	CursorCredentialReasonClientVersion  GatewayFailureReason = "cursor_client_version_rejected"
)

type cursorCredentialFailureSnapshotError struct {
	cause error
}

func (e *cursorCredentialFailureSnapshotError) Error() string { return e.cause.Error() }
func (e *cursorCredentialFailureSnapshotError) Unwrap() error { return e.cause }

func withCursorCredentialFailureSnapshot(err error, account *Account) error {
	if err == nil || account == nil || !account.IsCursorOAuth() {
		return err
	}
	var existing *cursorCredentialFailureSnapshotError
	if errors.As(err, &existing) {
		return err
	}
	return &cursorCredentialFailureSnapshotError{cause: err}
}

type cursorCredentialFailureClass struct {
	scope   GatewayFailureScope
	reason  GatewayFailureReason
	action  NextAccountAction
	message string
}

func classifyCursorCredentialFailure(err error) cursorCredentialFailureClass {
	message := ""
	if err != nil {
		message = strings.ToLower(err.Error())
	}
	stableReason := strings.ToLower(strings.TrimSpace(infraerrors.Reason(err)))
	contains := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(stableReason, value) || strings.Contains(message, value) {
				return true
			}
		}
		return false
	}

	switch {
	case errors.Is(err, errCursorAccessTokenMissing), errors.Is(err, errCursorCredentialsMissing):
		return cursorCredentialFailureClass{scope: GatewayFailureScopeAccount, reason: CursorCredentialReasonMissing, action: NextAccountRetry, message: "Cursor credentials are missing"}
	case errors.Is(err, errCursorWebSessionNotUpgraded), contains("cursor_oauth_web_session_unauthorized", "cursor_oauth_web_session_not_upgraded", "cursor_oauth_web_session_pending"):
		return cursorCredentialFailureClass{scope: GatewayFailureScopeAccount, reason: CursorCredentialReasonWebSession, action: NextAccountRetry, message: "Cursor web session could not be upgraded to a client token"}
	case errors.Is(err, errCursorAccessTokenExpired), errors.Is(err, errCursorAccessTokenRejected):
		return cursorCredentialFailureClass{scope: GatewayFailureScopeAccount, reason: CursorCredentialReasonExpired, action: NextAccountRetry, message: "Cursor access token is expired"}
	case errors.Is(err, errCursorRefreshNotConfigured), contains("cursor_oauth_client_not_configured", "cursor oauth refresh is not configured"):
		return cursorCredentialFailureClass{scope: GatewayFailureScopeProvider, reason: CursorCredentialReasonProviderConfig, action: NextAccountStop, message: "Cursor OAuth provider configuration is unavailable"}
	default:
		return cursorCredentialFailureClass{scope: GatewayFailureScopeAccount, reason: CursorCredentialReasonRefreshFailed, action: NextAccountRetry, message: "Cursor credential refresh is temporarily unavailable"}
	}
}

func (s *OpenAIGatewayService) newCursorCredentialFailover(c *gin.Context, account *Account, err error) *UpstreamFailoverError {
	class := classifyCursorCredentialFailure(err)
	if strings.TrimSpace(class.message) == "" {
		class.message = "Cursor credentials are unavailable"
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:  PlatformCursor,
		AccountID: accountID,
		Stage:     string(GatewayFailureStageAccountAuth),
		Scope:     string(class.scope),
		Reason:    string(class.reason),
		Kind:      "credential_failover",
		Message:   class.message,
	})
	return &UpstreamFailoverError{
		Stage:             GatewayFailureStageAccountAuth,
		Scope:             class.scope,
		Reason:            class.reason,
		NextAccountAction: class.action,
		ClientStatusCode:  http.StatusServiceUnavailable,
		ClientMessage:     CursorCredentialUnavailableClientMessage,
	}
}
