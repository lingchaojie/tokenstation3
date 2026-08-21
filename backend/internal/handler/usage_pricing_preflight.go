package handler

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	usagePricingUnavailableCode    = "usage_pricing_unavailable"
	usagePricingUnavailableMessage = "Unable to price upstream usage"
)

func finalizeGatewayUsagePricingValidation(c *gin.Context, result *service.ForwardResult, err error) bool {
	if err == nil {
		return true
	}
	if result != nil {
		if !result.CaptureTerminalError && !result.UpstreamFailed {
			result.CaptureResponseComplete = true
		}
		result.CaptureTerminalError = true
	}
	stream := result != nil && result.Stream
	service.MarkOpsPostResponseFailure(
		c,
		"api_error",
		usagePricingUnavailableCode,
		usagePricingUnavailableMessage,
		http.StatusBadGateway,
		stream,
	)
	return false
}

func finalizeOpenAIUsagePricingValidation(c *gin.Context, result *service.OpenAIForwardResult, err error) bool {
	if err == nil {
		return true
	}
	if result != nil {
		if !result.CaptureTerminalError && !result.UpstreamFailed {
			result.CaptureResponseComplete = true
		}
		result.CaptureTerminalError = true
	}
	stream := result != nil && result.Stream
	service.MarkOpsPostResponseFailure(
		c,
		"api_error",
		usagePricingUnavailableCode,
		usagePricingUnavailableMessage,
		http.StatusBadGateway,
		stream,
	)
	return false
}

func (h *GatewayHandler) validateGatewayUsagePricing(c *gin.Context, input *service.RecordUsageInput) bool {
	err := h.gatewayService.ValidateUsagePricing(requestContext(c), input)
	if finalizeGatewayUsagePricingValidation(c, input.Result, err) {
		return true
	}
	logUsagePricingPreflightFailure(c, input.Result.RequestID, input.Result.Model, input.Account, err)
	return false
}

func (h *GatewayHandler) validateGatewayUsagePricingWithLongContext(c *gin.Context, input *service.RecordUsageLongContextInput) bool {
	err := h.gatewayService.ValidateUsagePricingWithLongContext(requestContext(c), input)
	if finalizeGatewayUsagePricingValidation(c, input.Result, err) {
		return true
	}
	logUsagePricingPreflightFailure(c, input.Result.RequestID, input.Result.Model, input.Account, err)
	return false
}

func (h *OpenAIGatewayHandler) validateOpenAIUsagePricing(c *gin.Context, input *service.OpenAIRecordUsageInput) bool {
	err := h.gatewayService.ValidateUsagePricing(requestContext(c), input)
	if finalizeOpenAIUsagePricingValidation(c, input.Result, err) {
		return true
	}
	logUsagePricingPreflightFailure(c, input.Result.RequestID, input.Result.Model, input.Account, err)
	return false
}

func requestContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil {
		return c.Request.Context()
	}
	return context.Background()
}

func logUsagePricingPreflightFailure(c *gin.Context, requestID, model string, account *service.Account, err error) {
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	logger.FromContext(requestContext(c)).Error(
		"usage pricing preflight failed",
		zap.String("request_id", requestID),
		zap.String("model", model),
		zap.Int64("account_id", accountID),
		zap.Error(err),
	)
}
