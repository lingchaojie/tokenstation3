package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type KiroOAuthHandler struct {
	kiroOAuthService *service.KiroOAuthService
}

func NewKiroOAuthHandler(kiroOAuthService *service.KiroOAuthService) *KiroOAuthHandler {
	return &KiroOAuthHandler{kiroOAuthService: kiroOAuthService}
}

type KiroGenerateAuthURLRequest struct {
	Method  string `json:"method"`
	ProxyID *int64 `json:"proxy_id"`
}

func (h *KiroOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req KiroGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}

	result, err := h.kiroOAuthService.GenerateAuthURL(c.Request.Context(), service.KiroOAuthStartInput{
		Method:  req.Method,
		ProxyID: req.ProxyID,
	})
	if err != nil {
		response.BadRequest(c, "生成授权流程失败: "+err.Error())
		return
	}

	response.Success(c, result)
}

type KiroExchangeCodeRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	State     string `json:"state" binding:"required"`
	Code      string `json:"code" binding:"required"`
	ProxyID   *int64 `json:"proxy_id"`
}

func (h *KiroOAuthHandler) ExchangeCode(c *gin.Context) {
	var req KiroExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}

	tokenInfo, err := h.kiroOAuthService.ExchangeCode(c.Request.Context(), &service.KiroOAuthExchangeCodeInput{
		SessionID: req.SessionID,
		State:     req.State,
		Code:      req.Code,
		ProxyID:   req.ProxyID,
	})
	if err != nil {
		response.BadRequest(c, "Token 交换失败: "+err.Error())
		return
	}

	response.Success(c, tokenInfo)
}

type KiroPollDeviceCodeRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	ProxyID   *int64 `json:"proxy_id"`
}

func (h *KiroOAuthHandler) PollDeviceCode(c *gin.Context) {
	var req KiroPollDeviceCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求无效: "+err.Error())
		return
	}

	result, err := h.kiroOAuthService.PollDeviceCode(c.Request.Context(), service.KiroOAuthPollInput{
		SessionID: req.SessionID,
		ProxyID:   req.ProxyID,
	})
	if err != nil {
		response.BadRequest(c, "授权状态查询失败: "+err.Error())
		return
	}

	response.Success(c, result)
}
