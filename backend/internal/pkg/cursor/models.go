package cursor

import "errors"

const (
	fieldReqUseModelParameters = 5
	fieldReqDoNotUseMarkdown   = 7
	fieldRespModels            = 2

	fieldModelName                 = 1
	fieldModelSupportsImages       = 10
	fieldModelSupportsMaxMode      = 14
	fieldModelContextTokenLimit    = 15
	fieldModelMaxModeContextLimit  = 16
	fieldModelClientDisplayName    = 17
	fieldModelServerModelName      = 18
	fieldModelSupportsNonMaxMode   = 19
	fieldModelParameterizedVariant = 30

	fieldVariantParams             = 1
	fieldVariantDisplayName        = 2
	fieldVariantIsMaxMode          = 3
	fieldVariantIsDefaultMaxConfig = 4
	fieldVariantIsDefaultNonMax    = 5
	fieldVariantDisplayNameOutside = 8
	fieldVariantVariantString      = 9

	fieldParamID    = 1
	fieldParamValue = 2
)

type Model struct {
	Name                     string
	SupportsImages           bool
	SupportsMaxMode          bool
	ContextTokenLimit        int64
	MaxModeContextTokenLimit int64
	ClientDisplayName        string
	ServerModelName          string
	SupportsNonMaxMode       bool
	ParameterizedVariants    []Variant
}

type Variant struct {
	Params                   []Param
	DisplayName              string
	IsMaxMode                bool
	IsDefaultMaxConfig       bool
	IsDefaultNonMaxConfig    bool
	DisplayNameOutsidePicker string
	VariantString            string
}

type Param struct {
	ID    string
	Value string
}

func EncodeAvailableModelsRequest(useModelParameters, doNotUseMarkdown bool) []byte {
	var writer Writer
	if useModelParameters {
		writer.WriteBool(fieldReqUseModelParameters, true)
	}
	if doNotUseMarkdown {
		writer.WriteBool(fieldReqDoNotUseMarkdown, true)
	}
	return writer.Bytes()
}

func ParseAvailableModelsResponse(data []byte) ([]Model, error) {
	fields, err := Decode(data)
	if err != nil {
		return nil, err
	}
	if len(fields[0]) != 0 {
		return nil, errors.New("cursor: AvailableModels response is not raw protobuf")
	}
	rawModels := fields.AllBytes(fieldRespModels)
	models := make([]Model, 0, len(rawModels))
	for _, raw := range rawModels {
		model, err := parseModel(raw)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, nil
}

func parseModel(data []byte) (Model, error) {
	fields, err := Decode(data)
	if err != nil {
		return Model{}, err
	}
	model := Model{
		Name: fields.String(fieldModelName), SupportsImages: fields.Bool(fieldModelSupportsImages),
		SupportsMaxMode: fields.Bool(fieldModelSupportsMaxMode), ContextTokenLimit: fields.Int64(fieldModelContextTokenLimit),
		MaxModeContextTokenLimit: fields.Int64(fieldModelMaxModeContextLimit), ClientDisplayName: fields.String(fieldModelClientDisplayName),
		ServerModelName: fields.String(fieldModelServerModelName), SupportsNonMaxMode: fields.Bool(fieldModelSupportsNonMaxMode),
	}
	for _, raw := range fields.AllBytes(fieldModelParameterizedVariant) {
		variant, err := parseVariant(raw)
		if err != nil {
			return Model{}, err
		}
		model.ParameterizedVariants = append(model.ParameterizedVariants, variant)
	}
	return model, nil
}

func parseVariant(data []byte) (Variant, error) {
	fields, err := Decode(data)
	if err != nil {
		return Variant{}, err
	}
	variant := Variant{
		DisplayName: fields.String(fieldVariantDisplayName), IsMaxMode: fields.Bool(fieldVariantIsMaxMode),
		IsDefaultMaxConfig: fields.Bool(fieldVariantIsDefaultMaxConfig), IsDefaultNonMaxConfig: fields.Bool(fieldVariantIsDefaultNonMax),
		DisplayNameOutsidePicker: fields.String(fieldVariantDisplayNameOutside), VariantString: fields.String(fieldVariantVariantString),
	}
	for _, raw := range fields.AllBytes(fieldVariantParams) {
		paramFields, err := Decode(raw)
		if err != nil {
			return Variant{}, err
		}
		variant.Params = append(variant.Params, Param{ID: paramFields.String(fieldParamID), Value: paramFields.String(fieldParamValue)})
	}
	return variant, nil
}

func DefaultModelIDs() []string {
	return []string{
		"auto", "cursor-small", "composer-2.5", "composer-2.5-fast",
		"claude-4.5-sonnet", "claude-4.6-sonnet", "claude-opus-4.8",
		"gpt-5", "gpt-5.6-sol", "gemini-3-pro", "gemini-3.5-flash",
		"deepseek-v3.1", "grok-4.6",
	}
}
