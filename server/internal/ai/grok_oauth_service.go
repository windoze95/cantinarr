package ai

import (
	"context"
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

// grokOAuthService calls Grok Build's Responses proxy with the OAuth session
// token. It deliberately does not use api.x.ai or the public API-key service.
type grokOAuthService struct {
	client     openai.Client
	model      shared.ResponsesModel
	toolServer *mcp.ToolServer
	convID     string
	sessionID  string
}

func NewGrokOAuthService(token, model, conversationID string, toolServer *mcp.ToolServer) *grokOAuthService {
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
	return &grokOAuthService{
		client: openai.NewClient(
			openaioption.WithAPIKey(token),
			openaioption.WithBaseURL(baseURL),
			openaioption.WithHTTPClient(newHostedProviderHTTPClient(httpProviderStreamTimeout)),
			openaioption.WithRequestTimeout(httpProviderStreamTimeout),
		),
		model:      shared.ResponsesModel(model),
		toolServer: toolServer,
		convID:     conversationID,
		sessionID:  uuid.NewString(),
	}
}

func (s *grokOAuthService) SendMessage(ctx context.Context, history transcript, chatCtx ChatContext, cb StreamCallbacks) (transcript, error) {
	finalHistory := cloneTranscript(history)
	items := grokOAuthInputItems(history)
	tools := s.responseTools(s.toolServer.GetToolsForRole(chatCtx.Role))
	watch := &carouselWatch{}
	instructions := systemPrompt + "\n\n" + dynamicContext(chatCtx)

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		message, _, stop, err := s.responseTurn(ctx, instructions, items, tools, iteration == maxToolIterations-1, httpProviderMaxOutputTokens, cb)
		if err != nil {
			return finalHistory, err
		}
		if strings.TrimSpace(message.Refusal) != "" {
			return finalHistory, fmt.Errorf("grok oauth responses: model refused the response")
		}
		if len(message.ToolCalls) == 0 {
			if stop == StopReasonMaxOut && cb.OnText != nil {
				cb.OnText("\n\n_(Reply truncated at the length limit - ask me to continue.)_")
			}
			if message.Content != "" {
				finalHistory = append(finalHistory, openAIMessageToTranscript(message))
			}
			if iteration < maxToolIterations-2 && watch.shouldNudge(message.Content) {
				items = append(items, grokOAuthAssistantItems(message)...)
				nudge := watch.markNudged()
				items = append(items, responses.ResponseInputItemParamOfMessage(nudge, responses.EasyInputMessageRoleUser))
				finalHistory = append(finalHistory, textTranscriptMessage(agentRoleUser, nudge))
				cb.OnText = nil
				continue
			}
			return finalHistory, nil
		}
		if stop != StopReasonToolUse {
			return finalHistory, fmt.Errorf("grok oauth responses: unexpected output with function calls")
		}
		if s.toolServer == nil {
			return finalHistory, fmt.Errorf("grok oauth responses: model requested tools but no tool server is configured")
		}

		items = append(items, grokOAuthAssistantItems(message)...)
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
			return finalHistory, fmt.Errorf("grok oauth responses: model requested tool use but sent no complete function calls")
		}
		finalHistory = append(finalHistory, transcriptMessage{Role: agentRoleUser, Content: toolResultBlocks})
	}
	return finalHistory, fmt.Errorf("grok oauth responses: agent loop exceeded %d iterations", maxToolIterations)
}

func (s *grokOAuthService) NextTurn(ctx context.Context, p TurnParams) (TurnResult, error) {
	maxTokens := int64(turnMaxTokens(p))
	message, usage, stop, err := s.responseTurn(ctx, p.System, grokOAuthInputItems(p.History.toPrivate()), s.responseTools(p.Tools), p.ForceNoTools, maxTokens, StreamCallbacks{})
	if err != nil {
		return TurnResult{}, err
	}
	if strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 {
		return TurnResult{}, fmt.Errorf("grok oauth responses: response contained no text or function calls")
	}
	return TurnResult{
		Message:    exportMessage(openAIMessageToTranscript(message)),
		Usage:      usage,
		StopReason: stop,
	}, nil
}

func (s *grokOAuthService) responseTurn(
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
	if len(tools) > 0 {
		choice := responses.ToolChoiceOptionsAuto
		if forceNoTools {
			choice = responses.ToolChoiceOptionsNone
		}
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{OfToolChoiceMode: param.NewOpt(choice)}
	}

	requestID := uuid.NewString()
	stream := s.client.Responses.NewStreaming(ctx, params,
		openaioption.WithHeader("x-grok-conv-id", s.convID),
		openaioption.WithHeader("x-grok-req-id", requestID),
		openaioption.WithHeader("x-grok-model-override", string(s.model)),
		openaioption.WithHeader("x-grok-session-id", s.sessionID),
		openaioption.WithHeader("x-grok-agent-id", "cantinarr"),
		openaioption.WithHeader("x-grok-client-identifier", "cantinarr"),
	)
	defer stream.Close()

	var response responses.Response
	var gotResponse bool
	var failure string
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
			failure = strings.TrimSpace(event.Message)
			if failure == "" {
				failure = strings.TrimSpace(event.Code)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return openAIMessage{}, Usage{}, "", fmt.Errorf("grok oauth responses stream: %w", err)
	}
	if failure != "" {
		return openAIMessage{}, Usage{}, "", fmt.Errorf("grok oauth responses stream: %s", failure)
	}
	if !gotResponse {
		return openAIMessage{}, Usage{}, "", fmt.Errorf("grok oauth responses stream: response ended without a terminal event")
	}

	message := openAIMessage{Role: agentRoleAssistant, Content: response.OutputText()}
	for _, item := range response.Output {
		if item.Type != "function_call" {
			continue
		}
		call := item.AsFunctionCall()
		if strings.TrimSpace(call.Name) == "" || strings.TrimSpace(call.CallID) == "" {
			return openAIMessage{}, Usage{}, "", fmt.Errorf("grok oauth responses: function call omitted name or call ID")
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
		return openAIMessage{}, Usage{}, "", errors.New("grok oauth responses: response contained no text or function calls")
	}
	return message, usage, stop, nil
}

func (s *grokOAuthService) responseTools(tools []mcp.Tool) []responses.ToolUnionParam {
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

func grokOAuthAssistantItems(message openAIMessage) responses.ResponseInputParam {
	items := make(responses.ResponseInputParam, 0, len(message.ToolCalls)+1)
	if message.Content != "" {
		items = append(items, responses.ResponseInputItemParamOfMessage(message.Content, responses.EasyInputMessageRoleAssistant))
	}
	for _, call := range message.ToolCalls {
		items = append(items, responses.ResponseInputItemParamOfFunctionCall(call.Function.Arguments, call.ID, call.Function.Name))
	}
	return items
}

var _ TurnRunner = (*grokOAuthService)(nil)
