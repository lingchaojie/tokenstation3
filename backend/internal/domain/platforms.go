package domain

// 平台清单：平台的单一数据来源。"所有平台"类列表（分组平台、用户额度、
// 调度快照、定价匹配等）与"某网关族"类列表都从这里派生；新增平台
// 登记一条即可在这些位置生效，无需逐处修改或新增数据库约束迁移。
//
// 依赖具体实现的能力（OAuth、渠道监控、余额 / Coding Plan 探测、用量窗口、
// Codex 模型目录等）仍由各自的代码显式列出，不在此处声明。

// PlatformGateway 标识平台请求由哪个网关服务处理。
type PlatformGateway string

const (
	// PlatformGatewayAnthropic 由 GatewayService 处理（Claude Messages 协议，以及 TypeSafe 的 /v1/systemone）。
	PlatformGatewayAnthropic PlatformGateway = "anthropic"
	// PlatformGatewayOpenAI 由 OpenAIGatewayService 处理（OpenAI、Grok 与多协议 API Key 供应商）。
	PlatformGatewayOpenAI PlatformGateway = "openai"
	// PlatformGatewayGemini 由 Gemini 兼容网关处理。
	PlatformGatewayGemini PlatformGateway = "gemini"
	// PlatformGatewayAntigravity 由 Antigravity 网关处理。
	PlatformGatewayAntigravity PlatformGateway = "antigravity"
)

// PlatformSpec 描述一个具体平台。
type PlatformSpec struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"display_name"`
	Gateway     PlatformGateway `json:"gateway"`
	// CNProvider 标识国产单一厂商（Kimi / 智谱 / DeepSeek / MiniMax），共享国产
	// 供应商的余额 / Coding Plan 等能力。
	CNProvider bool `json:"cn_provider"`
	// LiteLLMProvider 是定价目录中的 provider 名；空表示不支持从定价目录同步模型。
	LiteLLMProvider string `json:"litellm_provider,omitempty"`
}

// platformList 的顺序即平台的展示顺序。
var platformList = []PlatformSpec{
	{ID: PlatformAnthropic, DisplayName: "Anthropic", Gateway: PlatformGatewayAnthropic, LiteLLMProvider: "anthropic"},
	{ID: PlatformOpenAI, DisplayName: "OpenAI", Gateway: PlatformGatewayOpenAI, LiteLLMProvider: "openai"},
	{ID: PlatformGemini, DisplayName: "Gemini", Gateway: PlatformGatewayGemini, LiteLLMProvider: "gemini"},
	{ID: PlatformAntigravity, DisplayName: "Antigravity", Gateway: PlatformGatewayAntigravity, LiteLLMProvider: "anthropic"},
	{ID: PlatformKiro, DisplayName: "Kiro", Gateway: PlatformGatewayAnthropic, LiteLLMProvider: "anthropic"},
	{ID: PlatformGrok, DisplayName: "Grok", Gateway: PlatformGatewayOpenAI, LiteLLMProvider: "xai"},
	{ID: PlatformKimi, DisplayName: "Kimi", Gateway: PlatformGatewayOpenAI, CNProvider: true, LiteLLMProvider: "moonshot"},
	{ID: PlatformZhipu, DisplayName: "Zhipu GLM", Gateway: PlatformGatewayOpenAI, CNProvider: true, LiteLLMProvider: "zhipu"},
	{ID: PlatformDeepseek, DisplayName: "DeepSeek", Gateway: PlatformGatewayOpenAI, CNProvider: true, LiteLLMProvider: "deepseek"},
	{ID: PlatformMiniMax, DisplayName: "MiniMax", Gateway: PlatformGatewayOpenAI, CNProvider: true, LiteLLMProvider: "minimax"},
	{ID: PlatformOpenCodeGo, DisplayName: "OpenCode", Gateway: PlatformGatewayOpenAI, LiteLLMProvider: "opencode-go"},
	{ID: PlatformTypeSafe, DisplayName: "TypeSafe / Jev", Gateway: PlatformGatewayAnthropic, LiteLLMProvider: "typesafe"},
	{ID: PlatformCommandCode, DisplayName: "Command Code", Gateway: PlatformGatewayOpenAI},
	{ID: PlatformCline, DisplayName: "Cline", Gateway: PlatformGatewayOpenAI},
}

var platformIndex = func() map[string]int {
	index := make(map[string]int, len(platformList))
	for i, spec := range platformList {
		index[spec.ID] = i
	}
	return index
}()

// Platforms 返回全部具体平台（副本），按展示顺序。
func Platforms() []PlatformSpec {
	out := make([]PlatformSpec, len(platformList))
	copy(out, platformList)
	return out
}

// LookupPlatform 返回具体平台的登记信息；未知值返回 false。
func LookupPlatform(id string) (PlatformSpec, bool) {
	i, ok := platformIndex[id]
	if !ok {
		return PlatformSpec{}, false
	}
	return platformList[i], true
}

// IsConcretePlatform 报告 id 是否为已登记的具体平台。
func IsConcretePlatform(id string) bool {
	_, ok := platformIndex[id]
	return ok
}

// IsGroupPlatform 报告 id 是否可作为分组平台（具体平台）。
func IsGroupPlatform(id string) bool {
	return IsConcretePlatform(id)
}

// ConcretePlatformIDs 返回全部具体平台标识，按展示顺序。
func ConcretePlatformIDs() []string {
	return PlatformIDsWhere(func(PlatformSpec) bool { return true })
}

// PlatformIDsWhere 返回满足条件的具体平台标识，按展示顺序。
func PlatformIDsWhere(match func(PlatformSpec) bool) []string {
	out := make([]string, 0, len(platformList))
	for _, spec := range platformList {
		if match(spec) {
			out = append(out, spec.ID)
		}
	}
	return out
}

// UsesOpenAIGateway 报告平台是否由 OpenAI 网关处理。
func UsesOpenAIGateway(id string) bool {
	spec, ok := LookupPlatform(id)
	return ok && spec.Gateway == PlatformGatewayOpenAI
}

// IsCNProviderPlatform 报告平台是否为国产单一厂商。
func IsCNProviderPlatform(id string) bool {
	spec, ok := LookupPlatform(id)
	return ok && spec.CNProvider
}
