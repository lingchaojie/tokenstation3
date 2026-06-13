package service

import "strings"

const (
	defaultBrandSiteName     = "LINX2"
	defaultBrandSiteSubtitle = "Link 2 All AI Model"
)

func isLegacyDefaultSiteName(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sub2api", "linx2.ai":
		return true
	default:
		return false
	}
}

func isLegacyDefaultSiteSubtitle(value string) bool {
	switch strings.TrimSpace(value) {
	case "Subscription to API Conversion Platform", "订阅转 API 转换平台", "AI Gateway Platform", "AI 网关平台":
		return true
	default:
		return false
	}
}
