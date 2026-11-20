package service

import (
	"slices"
	"strings"
)

const (
	vertexGlobalLocation = "global"
	vertexLocationUS     = "us"
	vertexLocationEU     = "eu"

	// vertexMultiRegionPriceMultiplier applies to Gemini requests sent to a US/EU multi-region endpoint.
	vertexMultiRegionPriceMultiplier = 1.1
)

// vertexModelLocations 列出 Vertex Gemini 模型可用的区域。Gemini 账号区域配置为多区域（us/eu）时，
// 表内声明支持该多区域的模型走对应多区域端点，其余模型（含未列出的模型）走 global。
// 账号区域为 global 或单区域时、以及非 Gemini 平台账号不查此表。
var vertexModelLocations = map[string][]string{
	"gemini-3.1-flash-image": {vertexGlobalLocation, vertexLocationUS, vertexLocationEU},
	"gemini-3.1-flash-lite":  {vertexGlobalLocation, vertexLocationUS, vertexLocationEU},
	"gemini-3.5-flash":       {vertexGlobalLocation, vertexLocationUS, vertexLocationEU},
	"gemini-3.5-flash-lite":  {vertexGlobalLocation, vertexLocationUS, vertexLocationEU},
	"gemini-3.6-flash":       {vertexGlobalLocation, vertexLocationUS, vertexLocationEU},
	"gemini-3.7-flash":       {vertexGlobalLocation, vertexLocationUS, vertexLocationEU},
	"gemini-3.8-flash":       {vertexGlobalLocation, vertexLocationUS, vertexLocationEU},
	"gemini-4-argon":         {vertexGlobalLocation, vertexLocationUS, vertexLocationEU},
}

// isVertexMultiRegion 判断区域是否为 Vertex 多区域（us/eu）。
func isVertexMultiRegion(location string) bool {
	return location == vertexLocationUS || location == vertexLocationEU
}

// vertexAPIHost 返回区域对应的 Vertex AI API 主机：global 用全局主机，多区域用 aiplatform.{loc}.rep 主机，
// 单区域用 {loc}-aiplatform 主机。
func vertexAPIHost(location string) string {
	switch {
	case location == vertexGlobalLocation:
		return vertexGlobalHost
	case isVertexMultiRegion(location):
		return "aiplatform." + location + ".rep.googleapis.com"
	default:
		return location + "-aiplatform.googleapis.com"
	}
}

// vertexModelSupportsMultiRegion 判断模型是否在 vertexModelLocations 中声明支持该多区域。
func vertexModelSupportsMultiRegion(model, location string) bool {
	if !isVertexMultiRegion(location) {
		return false
	}
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	return slices.Contains(vertexModelLocations[strings.TrimSpace(model)], location)
}

// resolveVertexModelLocation 返回模型在账号区域下实际使用的区域：账号为多区域而模型不支持该多区域时用 global。
func resolveVertexModelLocation(accountLocation, model string) string {
	if isVertexMultiRegion(accountLocation) && !vertexModelSupportsMultiRegion(model, accountLocation) {
		return vertexGlobalLocation
	}
	return accountLocation
}

// vertexGeminiRequestLocation 返回 Vertex Gemini 请求实际发往的区域：未配置 project_id 的 API Key 账号
// 走 Express mode 全局端点；其余按 VertexLocation(model)。
func vertexGeminiRequestLocation(account *Account, model string) string {
	if account.IsVertexAPIKey() && account.VertexProjectID() == "" {
		return vertexGlobalLocation
	}
	return account.VertexLocation(model)
}

// VertexUpstreamPriceMultiplier returns the factor for the resolved request
// location, including an explicit account override. Other locations use 1.
func VertexUpstreamPriceMultiplier(account *Account, model string) float64 {
	if account == nil || account.Platform != PlatformGemini || !account.IsVertexServiceAccount() {
		return 1
	}
	if isVertexMultiRegion(vertexGeminiRequestLocation(account, model)) {
		return vertexMultiRegionPriceMultiplier
	}
	return 1
}
