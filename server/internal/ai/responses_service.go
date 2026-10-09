package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	openai "github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// responsesService shares the Responses wire protocol while keeping each
// provider's endpoint, authentication, headers, and reasoning contract separate.
type responsesService struct {
	client          openai.Client
	model           shared.ResponsesModel
	toolServer      *mcp.ToolServer
	convID          string
	sessionID       string
	grokOAuth       bool
	openAI          bool
	reasoningEffort shared.ReasoningEffort
}

func NewGrokOAuthService(token, model, conversationID string, toolServer *mcp.ToolServer) *responsesService {
	if strings.TrimSpace(model) == "" {
		model = "grok-4.6"
	}
	baseURL := strings.TrimSpace(os.Getenv("GROK_OAUTH_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://cli-chat-proxy.grok.com/v1"
	}
	if strings.TrimSpace(conversationID) == "" {
		conversationID = uuid.NewString()
	}
	return &responsesService{
		client: openai.NewClient(
			openaioption.WithAPIKey(token),
			openaioption.WithBaseURL(baseURL),
			openaioption.WithHTTPClient(newHostedProviderHTTPClient(httpProviderStreamTimeout)),
			openaioption.WithRequestTimeout(httpProviderStreamTimeout),
		),
		model:      shared.ResponsesModel(model),
		grokOAuth:  true,
		toolServer: toolServer,
		convID:     conversationID,
		sessionID:  uuid.NewString(),
	}
}

func (s *openAIService) responsesAdapter() *responsesService {
	effort := shared.ReasoningEffort(s.reasoningEffort)
	// GPT-6.1 Sol cannot disable reasoning. Preserve the saved profile pin,
	// but use its lowest supported effort for an inherited none/minimal pin.
	if effort == "none" || effort == "minimal" {
		effort = shared.ReasoningEffortLow
	}
	return &responsesService{client: s.client, model: shared.ResponsesModel(s.model), toolServer: s.toolServer, openAI: true, reasoningEffort: effort}
}

func (s *responsesService) SendMessage(ctx context.Context, history transcript, chatCtx ChatContext, cb StreamCallbacks) (transcript, error) {
	finalHistory := cloneTranscript(history)
	items := s.inputItems(history)
	tools := s.responseTools(s.toolServer.GetToolsForRole(chatCtx.Role))
	watch := &carouselWatch{}
	instructions := systemPrompt + "\n\n" + dynamicContext(chatCtx)

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		message, _, stop, err := s.responseTurn(ctx, instructions, items, tools, iteration == maxToolIterations-1, httpProviderMaxOutputTokens, cb)
		if err != nil {
			return finalHistory, err
		}
		if strings.TrimSpace(message.Refusal) != "" {
			return finalHistory, fmt.Errorf("provider responses: model refused the response")
		}
		if len(message.ToolCalls) == 0 {
			if stop == StopReasonMaxOut && cb.OnText != nil {
				cb.OnText("\n\n_(Reply truncated at the length limit - ask me to continue.)_")
			}
			if message.Content != "" {
				finalHistory = append(finalHistory, openAIMessageToTranscript(message))
			}
			if iteration < maxToolIterations-2 && watch.shouldNudge(message.Content) {
				items = append(items, s.inputItems(transcript{openAIMessageToTranscript(message)})...)
				nudge := watch.markNudged()
				items = append(items, responses.ResponseInputItemParamOfMessage(nudge, responses.EasyInputMessageRoleUser))
				finalHistory = append(finalHistory, textTranscriptMessage(agentRoleUser, nudge))
				cb.OnText = nil
				continue
			}
			return finalHistory, nil
		}
		if stop != StopReasonToolUse {
			return finalHistory, fmt.Errorf("provider responses: unexpected output with function calls")
		}
		if s.toolServer == nil {
			return finalHistory, fmt.Errorf("provider responses: model requested tools but no tool server is configured")
		}

		items = append(items, s.inputItems(transcript{openAIMessageToTranscript(message)})...)
		finalHistory = append(finalHistory, openAIMessageToTranscript(message))
		var toolResultBlocks []transcriptBlock
		for _, toolCall := range message.ToolCalls {
			_, transcriptBlock, toolErr := (&openAIService{toolServer: s.toolServer}).runOpenAITool(ctx, toolCall, chatCtx, cb, watch)
			if toolErr != nil {
				return finalHistory, toolErr
			}
			items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(toolCall.ID, transcriptBlock.Content))
			toolResultBlocks = append(toolResultBlocks, transcriptBlock)
		}
		if len(toolResultBlocks) == 0 {
			return finalHistory, fmt.Errorf("provider responses: model requested tool use but sent no complete function calls")
		}
		finalHistory = append(finalHistory, transcriptMessage{Role: agentRoleUser, Content: toolResultBlocks})
	}
	return finalHistory, fmt.Errorf("provider responses: agent loop exceeded %d iterations", maxToolIterations)
}

func (s *responsesService) NextTurn(ctx context.Context, p TurnParams) (TurnResult, error) {
	maxTokens := int64(turnMaxTokens(p))
	probe := *s
	if s.openAI && p.DisableReasoning {
		probe.reasoningEffort = shared.ReasoningEffortLow
		if maxTokens < openAIValidationReasoningMaxTokens {
			maxTokens = openAIValidationReasoningMaxTokens
		}
	}
	message, usage, stop, err := probe.responseTurn(ctx, p.System, s.inputItems(p.History.toPrivate()), s.responseTools(p.Tools), p.ForceNoTools, maxTokens, StreamCallbacks{})
	if err != nil {
		return TurnResult{}, err
	}
	if strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 {
		return TurnResult{}, fmt.Errorf("provider responses: response contained no text or function calls")
	}
	return TurnResult{
		Message:    exportMessage(openAIMessageToTranscript(message)),
		Usage:      usage,
		StopReason: stop,
	}, nil
}

func (s *responsesService) responseTurn(
	ctx context.Context,
	instructions string,
	items responses.ResponseInputParam,
	tools []responses.ToolUnionParam,
	forceNoTools bool,
	maxTokens int64,
	cb StreamCallbacks,
) (openAIMessage, Usage, string, error) {
	if maxTokens < 1 {
		maxTokens = 1
	}
	params := responses.ResponseNewParams{
		Model:           s.model,
		Instructions:    param.NewOpt(instructions),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: items},
		Tools:           tools,
		MaxOutputTokens: param.NewOpt(maxTokens),
		Store:           param.NewOpt(false),
	}
	if s.openAI {
		params.Reasoning.Effort = s.reasoningEffort
		params.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
	}
	if len(tools) > 0 {
		choice := responses.ToolChoiceOptionsAuto
		if forceNoTools {
			choice = responses.ToolChoiceOptionsNone
		}
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{OfToolChoiceMode: param.NewOpt(choice)}
	}

	var requestOptions []openaioption.RequestOption
	if s.grokOAuth {
		requestOptions = []openaioption.RequestOption{
			openaioption.WithHeader("x-grok-conv-id", s.convID),
			openaioption.WithHeader("x-grok-req-id", uuid.NewString()),
			openaioption.WithHeader("x-grok-model-override", string(s.model)),
			openaioption.WithHeader("x-grok-session-id", s.sessionID),
			openaioption.WithHeader("x-grok-agent-id", "cantinarr"),
			openaioption.WithHeader("x-grok-client-identifier", "cantinarr"),
		}
	}
	stream := s.client.Responses.NewStreaming(ctx, params, requestOptions...)
	defer stream.Close()

	var response responses.Response
	var gotResponse bool
	var failure *responsesStreamError
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			if cb.OnText != nil && event.Delta != "" {
				cb.OnText(event.Delta)
			}
		case "response.completed", "response.incomplete":
			response = event.Response
			gotResponse = true
		case "response.failed", "error":
			failure = &responsesStreamError{Code: strings.TrimSpace(event.Code), Message: strings.TrimSpace(event.Message)}
			if event.Type == "response.failed" {
				failure.Code = string(event.Response.Error.Code)
				failure.Message = event.Response.Error.Message
			}
		}
	}
	if err := stream.Err(); err != nil {
		return openAIMessage{}, Usage{}, "", fmt.Errorf("provider responses stream: %w", err)
	}
	if failure != nil {
		return openAIMessage{}, Usage{}, "", failure
	}
	if !gotResponse {
		return openAIMessage{}, Usage{}, "", fmt.Errorf("provider responses stream: response ended without a terminal event")
	}

	message := openAIMessage{Role: agentRoleAssistant, Content: response.OutputText()}
	if s.openAI {
		// Stateless reasoning turns must replay all output items, including
		// encrypted reasoning and assistant phase, alongside tool results.
		output := make([]json.RawMessage, 0, len(response.Output))
		for _, item := range response.Output {
			output = append(output, json.RawMessage(item.RawJSON()))
		}
		message.ResponsesOutput, _ = json.Marshal(output)
	}
	for _, item := range response.Output {
		if item.Type == "message" {
			for _, content := range item.AsMessage().Content {
				if content.Type == "refusal" {
					message.Refusal += content.Refusal
				}
			}
		}
		if item.Type != "function_call" {
			continue
		}
		call := item.AsFunctionCall()
		if strings.TrimSpace(call.Name) == "" || strings.TrimSpace(call.CallID) == "" {
			return openAIMessage{}, Usage{}, "", fmt.Errorf("provider responses: function call omitted name or call ID")
		}
		if response.Status != responses.ResponseStatusCompleted || (call.Status != "" && call.Status != "completed") || !json.Valid([]byte(call.Arguments)) {
			return openAIMessage{}, Usage{}, "", fmt.Errorf("provider responses: incomplete function call")
		}
		message.ToolCalls = append(message.ToolCalls, openAIToolCall{
			ID:   call.CallID,
			Type: "function",
			Function: openAIFunctionCall{
				Name:      call.Name,
				Arguments: call.Arguments,
			},
		})
	}
	usage := Usage{
		InputTokens:     response.Usage.InputTokens,
		OutputTokens:    response.Usage.OutputTokens,
		CacheReadTokens: response.Usage.InputTokensDetails.CachedTokens,
	}
	stop := StopReasonEndTurn
	if len(message.ToolCalls) > 0 {
		stop = StopReasonToolUse
	} else if response.Status == responses.ResponseStatusIncomplete && response.IncompleteDetails.Reason == "max_output_tokens" {
		stop = StopReasonMaxOut
	}
	if message.Content == "" && len(message.ToolCalls) == 0 {
		return openAIMessage{}, Usage{}, "", errors.New("provider responses: response contained no text or function calls")
	}
	return message, usage, stop, nil
}

type responsesStreamError struct {
	Code    string
	Message string
}

func (e *responsesStreamError) Error() string {
	return "provider responses stream failed: " + e.Code + ": " + e.Message
}

func (s *responsesService) inputItems(history transcript) responses.ResponseInputParam {
	if !s.openAI {
		return grokOAuthInputItems(history)
	}
	var items responses.ResponseInputParam
	for _, message := range history {
		var output []json.RawMessage
		for _, block := range message.Content {
			if block.Type == blockTypeOpenAIResponsesOutput && json.Unmarshal([]byte(block.Data), &output) == nil && len(output) > 0 {
				break
			}
		}
		if len(output) == 0 {
			items = append(items, grokOAuthInputItems(transcript{message})...)
			continue
		}
		// Sanitization can remove an orphaned tool call. Its opaque copy must
		// not reintroduce that call into a later provider request.
		calls := make(map[string]string)
		for _, block := range message.Content {
			if block.Type == blockTypeToolUse {
				calls[block.ID] = block.Name
			}
		}
		for _, raw := range output {
			var item struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Name   string `json:"name"`
			}
			if json.Unmarshal(raw, &item) != nil {
				continue
			}
			switch item.Type {
			case "reasoning", "message":
			case "function_call":
				if calls[item.CallID] != item.Name || item.Name == "" {
					continue
				}
			default:
				continue
			}
			items = append(items, param.Override[responses.ResponseInputItemUnionParam](raw))
		}
	}
	return items
}

func (s *responsesService) responseTools(tools []mcp.Tool) []responses.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	out := make([]responses.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		parameters := make(map[string]any, len(tool.InputSchema))
		for key, value := range tool.InputSchema {
			parameters[key] = value
		}
		for _, key := range unsupportedToolRootKeywords {
			delete(parameters, key)
		}
		entry := responses.ToolParamOfFunction(tool.Name, parameters, false)
		if tool.Description != "" {
			entry.OfFunction.Description = param.NewOpt(tool.Description)
		}
		out = append(out, entry)
	}
	return out
}

func grokOAuthInputItems(history transcript) responses.ResponseInputParam {
	items := make(responses.ResponseInputParam, 0, len(history))
	for _, message := range history {
		var text strings.Builder
		for _, block := range message.Content {
			if block.Type == blockTypeText {
				text.WriteString(block.Text)
			}
		}
		if text.Len() > 0 {
			role := responses.EasyInputMessageRoleUser
			if message.Role == agentRoleAssistant {
				role = responses.EasyInputMessageRoleAssistant
			}
			items = append(items, responses.ResponseInputItemParamOfMessage(text.String(), role))
		}
		for _, block := range message.Content {
			switch block.Type {
			case blockTypeToolUse:
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(rawJSONString(block.Input), block.ID, block.Name))
			case blockTypeToolResult:
				items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(block.ToolUseID, block.Content))
			}
		}
	}
	return items
}

var _ TurnRunner = (*responsesService)(nil)
