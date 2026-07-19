package handler

import (
	"html"
	"math"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// SettingHandler 公开设置处理器（无需认证）
type SettingHandler struct {
	settingService           *service.SettingService
	pricingService           *service.PricingService
	notificationEmailService *service.NotificationEmailService
	version                  string
}

// NewSettingHandler 创建公开设置处理器
func NewSettingHandler(settingService *service.SettingService, version string) *SettingHandler {
	return &SettingHandler{
		settingService: settingService,
		version:        version,
	}
}

// SetNotificationEmailService attaches the public notification email service without
// changing the constructor signature used by existing tests.
func (h *SettingHandler) SetNotificationEmailService(notificationEmailService *service.NotificationEmailService) {
	h.notificationEmailService = notificationEmailService
}

// SetPricingService attaches the pricing service used by the public homepage.
func (h *SettingHandler) SetPricingService(pricingService *service.PricingService) {
	h.pricingService = pricingService
}

// GetPublicSettings 获取公开设置
// GET /api/v1/settings/public
func (h *SettingHandler) GetPublicSettings(c *gin.Context) {
	settings, err := h.settingService.GetPublicSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.PublicSettings{
		RegistrationEnabled:              settings.RegistrationEnabled,
		EmailVerifyEnabled:               settings.EmailVerifyEnabled,
		ForceEmailOnThirdPartySignup:     settings.ForceEmailOnThirdPartySignup,
		RegistrationEmailSuffixWhitelist: settings.RegistrationEmailSuffixWhitelist,
		PromoCodeEnabled:                 settings.PromoCodeEnabled,
		PasswordResetEnabled:             settings.PasswordResetEnabled,
		InvitationCodeEnabled:            settings.InvitationCodeEnabled,
		TotpEnabled:                      settings.TotpEnabled,
		LoginAgreementEnabled:            settings.LoginAgreementEnabled,
		LoginAgreementMode:               settings.LoginAgreementMode,
		LoginAgreementUpdatedAt:          settings.LoginAgreementUpdatedAt,
		LoginAgreementRevision:           settings.LoginAgreementRevision,
		LoginAgreementDocuments:          publicLoginAgreementDocumentsToDTO(settings.LoginAgreementDocuments),
		TurnstileEnabled:                 settings.TurnstileEnabled,
		TurnstileSiteKey:                 settings.TurnstileSiteKey,
		SiteName:                         settings.SiteName,
		SiteLogo:                         settings.SiteLogo,
		SiteSubtitle:                     settings.SiteSubtitle,
		APIBaseURL:                       settings.APIBaseURL,
		ContactInfo:                      settings.ContactInfo,
		DocURL:                           settings.DocURL,
		HomeContent:                      settings.HomeContent,
		HideCcsImportButton:              settings.HideCcsImportButton,
		PurchaseSubscriptionEnabled:      settings.PurchaseSubscriptionEnabled,
		PurchaseSubscriptionURL:          settings.PurchaseSubscriptionURL,
		TableDefaultPageSize:             settings.TableDefaultPageSize,
		TablePageSizeOptions:             settings.TablePageSizeOptions,
		CustomMenuItems:                  dto.ParseUserVisibleMenuItems(settings.CustomMenuItems),
		CustomEndpoints:                  dto.ParseCustomEndpoints(settings.CustomEndpoints),
		AnnouncementBanners:              dto.ParseAnnouncementBanners(settings.AnnouncementBanners),
		AnnouncementBannerIntervalMs:     settings.AnnouncementBannerIntervalMs,
		DingTalkOAuthEnabled:             settings.DingTalkOAuthEnabled,
		LinuxDoOAuthEnabled:              settings.LinuxDoOAuthEnabled,
		WeChatOAuthEnabled:               settings.WeChatOAuthEnabled,
		WeChatOAuthOpenEnabled:           settings.WeChatOAuthOpenEnabled,
		WeChatOAuthMPEnabled:             settings.WeChatOAuthMPEnabled,
		WeChatOAuthMobileEnabled:         settings.WeChatOAuthMobileEnabled,
		OIDCOAuthEnabled:                 settings.OIDCOAuthEnabled,
		OIDCOAuthProviderName:            settings.OIDCOAuthProviderName,
		GitHubOAuthEnabled:               settings.GitHubOAuthEnabled,
		GoogleOAuthEnabled:               settings.GoogleOAuthEnabled,
		BackendModeEnabled:               settings.BackendModeEnabled,
		PaymentEnabled:                   settings.PaymentEnabled,
		Version:                          h.version,
		ServerTimezone:                   timezone.Name(),
		ServerUTCOffset:                  timezone.UTCOffset(),
		BalanceLowNotifyEnabled:          settings.BalanceLowNotifyEnabled,
		AccountQuotaNotifyEnabled:        settings.AccountQuotaNotifyEnabled,
		BalanceLowNotifyThreshold:        settings.BalanceLowNotifyThreshold,
		BalanceLowNotifyRechargeURL:      settings.BalanceLowNotifyRechargeURL,

		ChannelMonitorEnabled:                settings.ChannelMonitorEnabled,
		ChannelMonitorDefaultIntervalSeconds: settings.ChannelMonitorDefaultIntervalSeconds,

		AvailableChannelsEnabled: settings.AvailableChannelsEnabled,

		AffiliateEnabled: settings.AffiliateEnabled,

		DailyCheckInEnabled: settings.DailyCheckInEnabled,
		DailyCheckInStartAt: settings.DailyCheckInStartAt,
		DailyCheckInEndAt:   settings.DailyCheckInEndAt,

		RiskControlEnabled: settings.RiskControlEnabled,

		AllowUserViewErrorRequests: settings.AllowUserViewErrorRequests,
	})
}

// GetAlvin returns the public alvin boolean setting.
// GET /api/v1/settings/alvin
func (h *SettingHandler) GetAlvin(c *gin.Context) {
	alvin, err := h.settingService.GetAlvin(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	c.Header("Cache-Control", "no-store")
	response.Success(c, dto.AlvinSettingResponse{Alvin: alvin})
}

// GetPublicModelPricing returns curated public model pricing for the homepage.
// GET /api/v1/settings/model-pricing
func (h *SettingHandler) GetPublicModelPricing(c *gin.Context) {
	if h.pricingService == nil {
		response.InternalError(c, "pricing service is not configured")
		return
	}

	providers := make([]dto.PublicModelPricingProvider, 0, len(publicModelPricingProviders))
	for _, provider := range publicModelPricingProviders {
		models := make([]dto.PublicModelPricingModel, 0, len(provider.models))
		for _, model := range provider.models {
			pricing := h.pricingService.GetCatalogModelPricing(model.id)
			if pricing == nil {
				continue
			}
			models = append(models, dto.PublicModelPricingModel{
				Name:                model.name,
				Model:               model.id,
				InputPerMillion:     perMillion(pricing.InputCostPerToken),
				OutputPerMillion:    perMillion(pricing.OutputCostPerToken),
				CacheReadPerMillion: perMillion(pricing.CacheReadInputTokenCost),
			})
		}
		if len(models) == 0 {
			continue
		}
		providers = append(providers, dto.PublicModelPricingProvider{
			Provider:    provider.name,
			AccentColor: provider.accentColor,
			Models:      models,
		})
	}

	response.Success(c, dto.PublicModelPricingResponse{Providers: providers})
}

// UnsubscribeNotificationEmail handles optional notification email opt-outs.
// GET /api/v1/settings/email-unsubscribe?token=...
func (h *SettingHandler) UnsubscribeNotificationEmail(c *gin.Context) {
	if h.notificationEmailService == nil {
		response.InternalError(c, "notification email service is not configured")
		return
	}
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		response.BadRequest(c, "token is required")
		return
	}
	result, err := h.notificationEmailService.Unsubscribe(c.Request.Context(), token)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	body := "<!doctype html><html><head><meta charset=\"utf-8\"><title>Unsubscribed</title></head><body style=\"font-family:-apple-system,BlinkMacSystemFont,Segoe UI,sans-serif;padding:32px;\"><h1>Unsubscribed</h1><p>You have unsubscribed <strong>" + html.EscapeString(result.Email) + "</strong> from <strong>" + html.EscapeString(result.Event) + "</strong> emails.</p></body></html>"
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(body))
}

type publicModelPricingProvider struct {
	name        string
	accentColor string
	models      []publicModelPricingModel
}

type publicModelPricingModel struct {
	id   string
	name string
}

var publicModelPricingProviders = []publicModelPricingProvider{
	{
		name:        "Anthropic",
		accentColor: "#d97745",
		models: []publicModelPricingModel{
			{id: "claude-opus-4-8", name: "Claude Opus 4.8"},
			{id: "claude-opus-4-7", name: "Claude Opus 4.7"},
			{id: "claude-opus-4-6", name: "Claude Opus 4.6"},
			{id: "claude-sonnet-4-6", name: "Claude Sonnet 4.6"},
			{id: "claude-sonnet-4-5", name: "Claude Sonnet 4.5"},
			{id: "claude-haiku-4-5", name: "Claude Haiku 4.5"},
		},
	},
	{
		name:        "OpenAI",
		accentColor: "#27a644",
		models: []publicModelPricingModel{
			{id: "gpt-5.5", name: "GPT-5.5"},
			{id: "gpt-5.4", name: "GPT-5.4"},
			{id: "gpt-5.4-mini", name: "GPT-5.4 Mini"},
			{id: "gpt-5.3-codex", name: "GPT-5.3 Codex"},
			{id: "gpt-5.2", name: "GPT-5.2"},
			{id: "gpt-5.2-codex", name: "GPT-5.2 Codex"},
			{id: "gpt-5.1", name: "GPT-5.1"},
			{id: "gpt-5.1-codex", name: "GPT-5.1 Codex"},
			{id: "gpt-5", name: "GPT-5"},
			{id: "gpt-5-mini", name: "GPT-5 Mini"},
			{id: "o4-mini", name: "o4 Mini"},
			{id: "o3", name: "o3"},
			{id: "gpt-4.1", name: "GPT-4.1"},
			{id: "gpt-4.1-mini", name: "GPT-4.1 Mini"},
			{id: "gpt-4o", name: "GPT-4o"},
			{id: "gpt-4o-mini", name: "GPT-4o Mini"},
		},
	},
}

func perMillion(costPerToken float64) float64 {
	if costPerToken <= 0 {
		return 0
	}
	return math.Round(costPerToken*1_000_000*1000) / 1000
}

func publicLoginAgreementDocumentsToDTO(items []service.LoginAgreementDocument) []dto.LoginAgreementDocument {
	result := make([]dto.LoginAgreementDocument, 0, len(items))
	for _, item := range items {
		result = append(result, dto.LoginAgreementDocument{
			ID:        item.ID,
			Title:     item.Title,
			ContentMD: item.ContentMD,
		})
	}
	return result
}
