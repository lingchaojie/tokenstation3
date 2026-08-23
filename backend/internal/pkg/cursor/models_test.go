package cursor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAvailableModelsUsesRawUnaryProtobufAndParsesVariants(t *testing.T) {
	var param Writer
	param.WriteString(fieldParamID, "reasoning")
	param.WriteString(fieldParamValue, "high")

	var variant Writer
	variant.WriteMessage(fieldVariantParams, param.Bytes())
	variant.WriteString(fieldVariantDisplayName, "Thinking")
	variant.WriteBool(fieldVariantIsMaxMode, true)
	variant.WriteBool(fieldVariantIsDefaultMaxConfig, true)
	variant.WriteBool(fieldVariantIsDefaultNonMax, true)
	variant.WriteString(fieldVariantDisplayNameOutside, "Thinking outside")
	variant.WriteString(fieldVariantVariantString, "thinking")

	var model Writer
	model.WriteString(fieldModelName, "gpt-5")
	model.WriteBool(fieldModelSupportsImages, true)
	model.WriteBool(fieldModelSupportsMaxMode, true)
	model.WriteInt64(fieldModelContextTokenLimit, 200000)
	model.WriteInt64(fieldModelMaxModeContextLimit, 400000)
	model.WriteString(fieldModelClientDisplayName, "GPT-5")
	model.WriteString(fieldModelServerModelName, "gpt-5-server")
	model.WriteBool(fieldModelSupportsNonMaxMode, true)
	model.WriteMessage(fieldModelParameterizedVariant, variant.Bytes())

	var response Writer
	response.WriteMessage(fieldRespModels, model.Bytes())
	models, err := ParseAvailableModelsResponse(response.Bytes())
	require.NoError(t, err)
	require.Equal(t, []Model{{
		Name: "gpt-5", SupportsImages: true, SupportsMaxMode: true,
		ContextTokenLimit: 200000, MaxModeContextTokenLimit: 400000,
		ClientDisplayName: "GPT-5", ServerModelName: "gpt-5-server", SupportsNonMaxMode: true,
		ParameterizedVariants: []Variant{{
			Params: []Param{{ID: "reasoning", Value: "high"}}, DisplayName: "Thinking",
			IsMaxMode: true, IsDefaultMaxConfig: true, IsDefaultNonMaxConfig: true,
			DisplayNameOutsidePicker: "Thinking outside", VariantString: "thinking",
		}},
	}}, models)

	require.Empty(t, EncodeAvailableModelsRequest(false, false), "unary request is raw protobuf, not a Connect envelope")
	body := EncodeAvailableModelsRequest(true, true)
	fields, err := Decode(body)
	require.NoError(t, err)
	require.True(t, fields.Bool(fieldReqUseModelParameters))
	require.True(t, fields.Bool(fieldReqDoNotUseMarkdown))
}

func TestParseAvailableModelsRejectsMalformedRawProtobuf(t *testing.T) {
	_, err := ParseAvailableModelsResponse([]byte{0x12, 0x05, 0x0a})
	require.Error(t, err)

	// A Connect envelope is not accepted for this unary RPC.
	_, err = ParseAvailableModelsResponse([]byte{0, 0, 0, 0, 0})
	require.Error(t, err)
}

func TestDefaultModelIDsMatchPinnedForkFallback(t *testing.T) {
	require.Equal(t, []string{
		"auto",
		"cursor-small",
		"composer-2.5",
		"composer-2.5-fast",
		"claude-4.5-sonnet",
		"claude-4.6-sonnet",
		"claude-opus-4.8",
		"gpt-5",
		"gpt-5.6-sol",
		"gemini-3-pro",
		"gemini-3.5-flash",
		"deepseek-v3.1",
		"grok-4.6",
	}, DefaultModelIDs())
}
