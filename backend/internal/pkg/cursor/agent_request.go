package cursor

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	AgentModeAgent int32 = 1
	AgentModeAsk   int32 = 2
	AgentModePlan  int32 = 3
)

const (
	AgentDefaultModel = "default"
	AgentDefaultCwd   = "/tmp"
	AgentMCPProvider  = "sub2api"
)

const (
	AgentDelayAfterRunRequest = 1500 * time.Millisecond
	AgentDelayAfterContext    = 800 * time.Millisecond
	AgentDelayAfterMarker     = 400 * time.Millisecond
	AgentHeartbeatInterval    = 5 * time.Second
)

const agentKvAckCount = 8

const (
	fieldAgentClientRunRequest         = 1
	fieldAgentClientExecMessage        = 2
	fieldAgentClientKvMessage          = 3
	fieldAgentClientExecControlMessage = 5
	fieldAgentClientHeartbeat          = 7

	fieldAgentRunConversationState       = 1
	fieldAgentRunAction                  = 2
	fieldAgentRunMcpTools                = 4
	fieldAgentRunConversationID          = 5
	fieldAgentRunCustomSystemPrompt      = 8
	fieldAgentRunRequestedModel          = 9
	fieldAgentRunExcludeWorkspaceContext = 12
	fieldAgentRunSelectedSubagentModels  = 14
	fieldAgentRunConversationGroupID     = 16

	fieldAgentActionUserMessage        = 1
	fieldAgentUserMessageActionMessage = 1
	fieldAgentUserMessageText          = 1
	fieldAgentUserMessageID            = 2
	fieldAgentUserMessageContext       = 3
	fieldAgentUserMessageMode          = 4

	fieldAgentModelID         = 1
	fieldAgentModelMaxMode    = 2
	fieldAgentModelParameters = 3
	fieldAgentModelParamID    = 1
	fieldAgentModelParamValue = 2

	fieldAgentMcpToolsList       = 1
	fieldAgentMcpToolName        = 1
	fieldAgentMcpToolDescription = 2
	fieldAgentMcpToolInputSchema = 3
	fieldAgentMcpToolProvider    = 4
	fieldAgentMcpToolToolName    = 5

	fieldAgentSelectedImages         = 1
	fieldAgentSelectedImageUUID      = 2
	fieldAgentSelectedImagePath      = 3
	fieldAgentSelectedImageDimension = 4
	fieldAgentSelectedImageMimeType  = 7
	fieldAgentSelectedImageData      = 8
	fieldAgentSelectedImageDimWidth  = 1
	fieldAgentSelectedImageDimHeight = 2

	fieldAgentExecRequestContextResult = 10
	fieldAgentRequestContextSuccess    = 1
	fieldAgentRequestContextInner      = 1
	fieldAgentRequestContextEnv        = 4

	fieldAgentEnvOSVersion         = 1
	fieldAgentEnvWorkspacePaths    = 2
	fieldAgentEnvShell             = 3
	fieldAgentEnvTimeZone          = 10
	fieldAgentEnvProjectFolder     = 11
	fieldAgentEnvSandboxSupported  = 14
	fieldAgentEnvSandboxNetDefault = 16
	fieldAgentEnvComputerUse       = 19
	fieldAgentEnvWorkingDirIsHome  = 20
	fieldAgentEnvProcessWorkingDir = 21
	fieldAgentEnvUnknown22         = 22

	fieldAgentExecControlStreamClose = 1
	fieldAgentKvID                   = 1
	fieldAgentKvSetBlobResult        = 3
)

type AgentTool struct {
	Name               string
	Description        string
	InputSchema        any
	ProviderIdentifier string
}

type AgentImage struct {
	Data     []byte
	MimeType string
	Path     string
	UUID     string
	Width    int32
	Height   int32
}

type AgentEnv struct {
	OSVersion string
	Shell     string
	TimeZone  string
	Cwd       string
}

func (env AgentEnv) resolved(cwd string) AgentEnv {
	if env.OSVersion == "" {
		env.OSVersion = "linux"
	}
	if env.Shell == "" {
		env.Shell = "bash"
	}
	if env.TimeZone == "" {
		env.TimeZone = "UTC"
	}
	if env.Cwd == "" {
		env.Cwd = cwd
	}
	if env.Cwd == "" {
		env.Cwd = AgentDefaultCwd
	}
	return env
}

type AgentRunParams struct {
	Prompt         string
	Model          string
	MaxMode        bool
	SystemPrompt   string
	Mode           int32
	Tools          []AgentTool
	Images         []AgentImage
	ConversationID string
	Cwd            string
	MessageID      string
	Env            AgentEnv
}

func (params AgentRunParams) resolved() AgentRunParams {
	if params.Model == "" {
		params.Model = AgentDefaultModel
	}
	if params.Mode == 0 {
		params.Mode = AgentModeAgent
	}
	if params.ConversationID == "" {
		params.ConversationID = uuid.NewString()
	}
	if params.MessageID == "" {
		params.MessageID = uuid.NewString()
	}
	if params.Cwd == "" {
		params.Cwd = AgentDefaultCwd
	}
	params.Env = params.Env.resolved(params.Cwd)
	return params
}

type FramePlan struct {
	Label      string
	Payload    []byte
	DelayAfter time.Duration
}

func BuildRunFrameSequence(params AgentRunParams) []FramePlan {
	params = params.resolved()
	plans := []FramePlan{
		{Label: "run_request", Payload: EncodeAgentRunRequest(params), DelayAfter: AgentDelayAfterRunRequest},
		{Label: "request_context_env", Payload: EncodeRequestContextEnvFrame(params.Env), DelayAfter: AgentDelayAfterContext},
		{Label: "exec_stream_close", Payload: EncodeStreamClose(), DelayAfter: AgentDelayAfterMarker},
		{Label: "kv_set_blob_ack", Payload: EncodeKvSetBlobAck(0), DelayAfter: AgentDelayAfterMarker},
	}
	for id := uint32(1); id <= agentKvAckCount; id++ {
		plans = append(plans, FramePlan{
			Label: fmt.Sprintf("kv_set_blob_ack#%d", id), Payload: EncodeKvSetBlobAck(id), DelayAfter: AgentDelayAfterMarker,
		})
	}
	return plans
}

func EncodeAgentRunRequest(params AgentRunParams) []byte {
	params = params.resolved()
	var request Writer
	request.WriteString(fieldAgentRunConversationState, "")
	request.WriteMessage(fieldAgentRunAction, encodeAgentConversationAction(params))
	request.WriteBytes(fieldAgentRunMcpTools, encodeAgentMcpTools(params.Tools))
	request.WriteString(fieldAgentRunConversationID, params.ConversationID)
	if params.SystemPrompt != "" {
		request.WriteString(fieldAgentRunCustomSystemPrompt, params.SystemPrompt)
	}
	request.WriteMessage(fieldAgentRunRequestedModel, encodeAgentRequestedModel(params.Model, params.MaxMode))
	request.WriteInt32(fieldAgentRunExcludeWorkspaceContext, 0)
	request.WriteMessage(fieldAgentRunSelectedSubagentModels, encodeAgentModelID(AgentDefaultModel))
	request.WriteMessage(fieldAgentRunSelectedSubagentModels, encodeAgentRequestedModel(params.Model, params.MaxMode))
	request.WriteString(fieldAgentRunConversationGroupID, params.ConversationID)

	var message Writer
	message.WriteMessage(fieldAgentClientRunRequest, request.Bytes())
	return message.Bytes()
}

func encodeAgentConversationAction(params AgentRunParams) []byte {
	var userMessage Writer
	userMessage.WriteString(fieldAgentUserMessageText, params.Prompt)
	userMessage.WriteString(fieldAgentUserMessageID, params.MessageID)
	if context := encodeAgentSelectedContext(params.Images); len(context) > 0 {
		userMessage.WriteBytes(fieldAgentUserMessageContext, context)
	} else {
		userMessage.WriteString(fieldAgentUserMessageContext, "")
	}
	userMessage.WriteInt32(fieldAgentUserMessageMode, params.Mode)

	var action Writer
	action.WriteMessage(fieldAgentUserMessageActionMessage, userMessage.Bytes())
	var conversation Writer
	conversation.WriteMessage(fieldAgentActionUserMessage, action.Bytes())
	return conversation.Bytes()
}

func encodeAgentModelID(model string) []byte {
	var writer Writer
	writer.WriteString(fieldAgentModelID, model)
	return writer.Bytes()
}

func encodeAgentRequestedModel(model string, maxMode bool) []byte {
	var writer Writer
	writer.WriteString(fieldAgentModelID, model)
	if maxMode {
		writer.WriteBool(fieldAgentModelMaxMode, true)
	}
	var parameter Writer
	parameter.WriteString(fieldAgentModelParamID, "fast")
	parameter.WriteString(fieldAgentModelParamValue, "false")
	writer.WriteMessage(fieldAgentModelParameters, parameter.Bytes())
	return writer.Bytes()
}

func encodeAgentMcpTools(tools []AgentTool) []byte {
	var writer Writer
	for _, tool := range tools {
		writer.WriteMessage(fieldAgentMcpToolsList, encodeAgentMcpToolDefinition(tool))
	}
	return writer.Bytes()
}

func encodeAgentMcpToolDefinition(tool AgentTool) []byte {
	var writer Writer
	writer.WriteString(fieldAgentMcpToolName, tool.Name)
	writer.WriteString(fieldAgentMcpToolDescription, tool.Description)
	writer.WriteBytes(fieldAgentMcpToolInputSchema, encodeProtobufValue(tool.InputSchema))
	provider := tool.ProviderIdentifier
	if provider == "" {
		provider = AgentMCPProvider
	}
	writer.WriteString(fieldAgentMcpToolProvider, provider)
	writer.WriteString(fieldAgentMcpToolToolName, tool.Name)
	return writer.Bytes()
}

func encodeAgentSelectedContext(images []AgentImage) []byte {
	var writer Writer
	for _, image := range images {
		if len(image.Data) > 0 {
			writer.WriteMessage(fieldAgentSelectedImages, encodeAgentSelectedImage(image))
		}
	}
	return writer.Bytes()
}

func encodeAgentSelectedImage(image AgentImage) []byte {
	var writer Writer
	id := image.UUID
	if id == "" {
		id = uuid.NewString()
	}
	writer.WriteString(fieldAgentSelectedImageUUID, id)
	if image.Path != "" {
		writer.WriteString(fieldAgentSelectedImagePath, image.Path)
	}
	if image.Width > 0 && image.Height > 0 {
		var dimension Writer
		dimension.WriteInt32(fieldAgentSelectedImageDimWidth, image.Width)
		dimension.WriteInt32(fieldAgentSelectedImageDimHeight, image.Height)
		writer.WriteMessage(fieldAgentSelectedImageDimension, dimension.Bytes())
	}
	if image.MimeType != "" {
		writer.WriteString(fieldAgentSelectedImageMimeType, image.MimeType)
	}
	writer.WriteBytes(fieldAgentSelectedImageData, image.Data)
	return writer.Bytes()
}

func EncodeRequestContextEnvFrame(env AgentEnv) []byte {
	env = env.resolved("")
	var environment Writer
	environment.WriteString(fieldAgentEnvOSVersion, env.OSVersion)
	environment.WriteString(fieldAgentEnvWorkspacePaths, env.Cwd)
	environment.WriteString(fieldAgentEnvShell, env.Shell)
	environment.WriteString(fieldAgentEnvTimeZone, env.TimeZone)
	environment.WriteString(fieldAgentEnvProjectFolder, env.Cwd)
	environment.WriteBool(fieldAgentEnvSandboxSupported, true)
	environment.WriteBool(fieldAgentEnvSandboxNetDefault, true)
	environment.WriteBool(fieldAgentEnvComputerUse, false)
	environment.WriteBool(fieldAgentEnvWorkingDirIsHome, false)
	environment.WriteString(fieldAgentEnvProcessWorkingDir, env.Cwd)
	environment.WriteInt32(fieldAgentEnvUnknown22, 0)

	var context Writer
	context.WriteMessage(fieldAgentRequestContextEnv, environment.Bytes())
	var success Writer
	success.WriteMessage(fieldAgentRequestContextInner, context.Bytes())
	var result Writer
	result.WriteMessage(fieldAgentRequestContextSuccess, success.Bytes())
	var execution Writer
	execution.WriteMessage(fieldAgentExecRequestContextResult, result.Bytes())
	var message Writer
	message.WriteMessage(fieldAgentClientExecMessage, execution.Bytes())
	return message.Bytes()
}

func EncodeClientHeartbeat() []byte {
	var writer Writer
	writer.WriteBytes(fieldAgentClientHeartbeat, nil)
	return writer.Bytes()
}

func EncodeStreamClose() []byte {
	var control Writer
	control.WriteBytes(fieldAgentExecControlStreamClose, nil)
	var writer Writer
	writer.WriteMessage(fieldAgentClientExecControlMessage, control.Bytes())
	return writer.Bytes()
}

func EncodeKvSetBlobAck(id uint32) []byte {
	var kv Writer
	if id != 0 {
		kv.WriteInt64(fieldAgentKvID, int64(id))
	}
	kv.WriteBytes(fieldAgentKvSetBlobResult, nil)
	var writer Writer
	writer.WriteMessage(fieldAgentClientKvMessage, kv.Bytes())
	return writer.Bytes()
}
