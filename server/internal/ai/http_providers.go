package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
	"google.golang.org/genai"

	"github.com/windoze95/cantinarr-server/internal/mcp"
)

const (
	httpProviderMaxOutputTokens int64 = 16000
	httpProviderStreamTimeout         = 5 * time.Minute
	geminiStreamMaxAttempts           = 3
	geminiRetryBaseDelay              = 100 * time.Millisecond
	geminiMetadataBufferLimit         = 32
)

type openAIService struct {
	client     openai.Client
	model      openai.ChatModel
	toolServer *mcp.ToolServer
}

func NewOpenAIService(apiKey, model string, toolServer *mcp.ToolServer) *openAIService {
	if model == "" {
		model = "gpt-5.5"
	}
	return &openAIService{
		client: openai.NewClient(
			openaioption.WithAPIKey(apiKey),
			openaioption.WithHTTPClient(newCredentialHTTPClient(httpProviderStreamTimeout)),
			openaioption.WithRequestTimeout(httpProviderStreamTimeout),
		),
		model:      openai.ChatModel(model),
		toolServer: toolServer,
	}
}

func (s *openAIService) SendMessage(ctx context.Context, history transcript, chatCtx ChatContext, cb StreamCallbacks) (transcript, error) {
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(systemPrompt + "\n\n" + dynamicContext(chatCtx)),
	}
	messages = append(messages, toOpenAIMessages(history)...)
	tools := toOpenAITools(s.toolServer.GetToolsForRole(chatCtx.Role))
	finalHistory := cloneTranscript(history)

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		params := openAIInteractiveParams(s.model, messages, tools, iteration == maxToolIterations-1)

		message, finishReason, err := s.chatStream(ctx, params, cb)
		if err != nil {
			return finalHistory, err
		}
		if len(message.ToolCalls) == 0 {
			if finishReason == "tool_calls" {
				return finalHistory, fmt.Errorf("model requested tool use but sent no complete tool calls")
			}
			if finishReason == "length" && cb.OnText != nil {
				cb.OnText("\n\n_(Reply truncated at the length limit - ask me to continue.)_")
			}
			if message.Content != "" {
				finalHistory = append(finalHistory, openAIMessageToTranscript(message))
			}
			return finalHistory, nil
		}

		messages = append(messages, openAIMessageToParam(message))
		finalHistory = append(finalHistory, openAIMessageToTranscript(message))
		var toolResultBlocks []transcriptBlock
		for _, toolCall := range message.ToolCalls {
			result, transcriptBlock, toolErr := s.runOpenAITool(ctx, toolCall, chatCtx, cb)
			if toolErr != nil {
				return finalHistory, toolErr
			}
			messages = append(messages, result)
			toolResultBlocks = append(toolResultBlocks, transcriptBlock)
		}
		if len(toolResultBlocks) == 0 {
			return finalHistory, fmt.Errorf("model requested tool use but sent no tool calls")
		}
		finalHistory = append(finalHistory, transcriptMessage{Role: agentRoleUser, Content: toolResultBlocks})
	}

	return finalHistory, fmt.Errorf("agent loop exceeded %d iterations", maxToolIterations)
}

func openAIInteractiveParams(
	model openai.ChatModel,
	messages []openai.ChatCompletionMessageParamUnion,
	tools []openai.ChatCompletionToolUnionParam,
	forceText bool,
) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:               model,
		Messages:            messages,
		MaxCompletionTokens: openai.Int(httpProviderMaxOutputTokens),
		Tools:               tools,
	}
	if forceText && len(tools) > 0 {
		params.ToolChoice.OfAuto = openai.String(string(openai.ChatCompletionToolChoiceOptionAutoNone))
	}
	return params
}

func (s *openAIService) chatStream(ctx context.Context, params openai.ChatCompletionNewParams, cb StreamCallbacks) (openAIMessage, string, error) {
	stream := s.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	acc := openai.ChatCompletionAccumulator{}
	for stream.Next() {
		chunk := stream.Current()
		if !acc.AddChunk(chunk) {
			return openAIMessage{}, "", fmt.Errorf("accumulate openai stream chunk")
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" && cb.OnText != nil {
				cb.OnText(choice.Delta.Content)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return openAIMessage{}, "", fmt.Errorf("openai chat stream: %w", err)
	}
	if len(acc.Choices) == 0 {
		return openAIMessage{}, "", fmt.Errorf("openai chat stream: response had no choices")
	}

	choice := acc.Choices[0]
	message := openAIMessageFromSDK(choice.Message)
	if strings.TrimSpace(message.Refusal) != "" {
		return openAIMessage{}, "", fmt.Errorf("openai chat stream: model refused the response")
	}
	if strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 {
		return openAIMessage{}, "", fmt.Errorf("openai chat stream: response contained no text or tool calls")
	}
	return message, string(choice.FinishReason), nil
}

func (s *openAIService) runOpenAITool(ctx context.Context, toolCall openAIToolCall, chatCtx ChatContext, cb StreamCallbacks) (openai.ChatCompletionMessageParamUnion, transcriptBlock, error) {
	name := toolCall.Function.Name
	if cb.OnToolStart != nil {
		cb.OnToolStart(name, toolLabel(name))
	}

	input := json.RawMessage(toolCall.Function.Arguments)
	if len(input) == 0 || string(input) == "null" {
		input = json.RawMessage("{}")
	}

	result, err := s.toolServer.ExecuteTool(ctx, name, input, mcp.CallContext{
		UserID:          chatCtx.UserID,
		DeviceID:        chatCtx.DeviceID,
		Role:            chatCtx.Role,
		RequireSharedAI: chatCtx.RequireSharedAI,
		Reauthorize:     true,
	})
	if err != nil {
		if cb.OnToolEnd != nil {
			cb.OnToolEnd(name, false)
		}
		if errors.Is(err, mcp.ErrToolAuthorization) {
			return openai.ChatCompletionMessageParamUnion{}, transcriptBlock{}, mcp.ErrToolAuthorization
		}
		content := "Error: " + err.Error()
		return openai.ToolMessage(content, toolCall.ID), transcriptBlock{
			Type:      blockTypeToolResult,
			ToolUseID: toolCall.ID,
			Name:      name,
			Content:   content,
			IsError:   true,
		}, nil
	}
	if result.StructuredData != nil && mcp.ToolsWithUI[name] && cb.OnToolResult != nil {
		cb.OnToolResult(name, result.StructuredData)
	}
	if cb.OnToolEnd != nil {
		cb.OnToolEnd(name, true)
	}
	return openai.ToolMessage(result.Text, toolCall.ID), transcriptBlock{
		Type:      blockTypeToolResult,
		ToolUseID: toolCall.ID,
		Name:      name,
		Content:   result.Text,
	}, nil
}

type openAIMessage struct {
	Role       string
	Content    string
	Refusal    string
	ToolCalls  []openAIToolCall
	ToolCallID string
}

type openAIToolCall struct {
	ID       string
	Type     string
	Function openAIFunctionCall
}

type openAIFunctionCall struct {
	Name      string
	Arguments string
}

func toOpenAIMessages(messages transcript) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case agentRoleAssistant:
			if len(out) == 0 {
				continue
			}
			message := openAIMessage{Role: "assistant"}
			for _, block := range m.Content {
				switch block.Type {
				case blockTypeText:
					message.Content += block.Text
				case blockTypeToolUse:
					message.ToolCalls = append(message.ToolCalls, openAIToolCall{
						ID:   block.ID,
						Type: "function",
						Function: openAIFunctionCall{
							Name:      block.Name,
							Arguments: rawJSONString(block.Input),
						},
					})
				}
			}
			if message.Content != "" || len(message.ToolCalls) > 0 {
				out = append(out, openAIMessageToParam(message))
			}
		default:
			var userText strings.Builder
			for _, block := range m.Content {
				switch block.Type {
				case blockTypeText:
					userText.WriteString(block.Text)
				case blockTypeToolResult:
					out = append(out, openai.ToolMessage(block.Content, block.ToolUseID))
				}
			}
			if userText.Len() > 0 {
				out = append(out, openai.UserMessage(userText.String()))
			}
		}
	}
	return out
}

func openAIMessageToParam(message openAIMessage) openai.ChatCompletionMessageParamUnion {
	assistant := openai.ChatCompletionAssistantMessageParam{}
	if message.Content != "" {
		assistant.Content.OfString = openai.String(message.Content)
	}
	for _, call := range message.ToolCalls {
		if call.Function.Name == "" {
			continue
		}
		assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID: call.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				},
			},
		})
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant}
}

func openAIMessageFromSDK(message openai.ChatCompletionMessage) openAIMessage {
	out := openAIMessage{
		Role:    string(message.Role),
		Content: message.Content,
		Refusal: message.Refusal,
	}
	if out.Role == "" {
		out.Role = "assistant"
	}
	for _, call := range message.ToolCalls {
		if call.Function.Name == "" {
			continue
		}
		callType := string(call.Type)
		if callType == "" {
			callType = "function"
		}
		out.ToolCalls = append(out.ToolCalls, openAIToolCall{
			ID:   call.ID,
			Type: callType,
			Function: openAIFunctionCall{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}

func openAIMessageToTranscript(message openAIMessage) transcriptMessage {
	out := transcriptMessage{Role: agentRoleAssistant}
	if message.Content != "" {
		out.Content = append(out.Content, transcriptBlock{Type: blockTypeText, Text: message.Content})
	}
	for _, call := range message.ToolCalls {
		input := json.RawMessage(call.Function.Arguments)
		if len(input) == 0 || string(input) == "null" {
			input = json.RawMessage("{}")
		}
		out.Content = append(out.Content, transcriptBlock{
			Type:  blockTypeToolUse,
			ID:    call.ID,
			Name:  call.Function.Name,
			Input: input,
		})
	}
	return out
}

func toOpenAITools(tools []mcp.Tool) []openai.ChatCompletionToolUnionParam {
	out := make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, t := range tools {
		parameters := make(openai.FunctionParameters, len(t.InputSchema))
		for key, value := range t.InputSchema {
			parameters[key] = value
		}
		for _, key := range []string{"oneOf", "anyOf", "allOf", "enum", "const", "not"} {
			delete(parameters, key)
		}
		fn := openai.FunctionDefinitionParam{
			Name:       t.Name,
			Parameters: parameters,
		}
		if t.Description != "" {
			fn.Description = openai.String(t.Description)
		}
		out = append(out, openai.ChatCompletionFunctionTool(fn))
	}
	return out
}

type geminiService struct {
	client     *genai.Client
	clientErr  error
	model      string
	toolServer *mcp.ToolServer
}

func NewGeminiService(apiKey, model string, toolServer *mcp.ToolServer) *geminiService {
	if model == "" {
		model = "gemini-3.5-flash"
	}
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:     apiKey,
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: newCredentialHTTPClient(httpProviderStreamTimeout),
		HTTPOptions: genai.HTTPOptions{
			Timeout: genai.Ptr(httpProviderStreamTimeout),
		},
	})
	return &geminiService{
		client:     client,
		clientErr:  err,
		model:      strings.TrimPrefix(model, "models/"),
		toolServer: toolServer,
	}
}

func (s *geminiService) SendMessage(ctx context.Context, history transcript, chatCtx ChatContext, cb StreamCallbacks) (transcript, error) {
	finalHistory := cloneTranscript(history)
	if s.clientErr != nil {
		return finalHistory, fmt.Errorf("gemini client: %w", s.clientErr)
	}

	contents := toGeminiContents(history)
	tools := toGeminiTools(s.toolServer.GetToolsForRole(chatCtx.Role))
	system := &genai.Content{Parts: []*genai.Part{genai.NewPartFromText(systemPrompt + "\n\n" + dynamicContext(chatCtx))}}

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		useTools := iteration != maxToolIterations-1
		resp, err := s.streamGenerate(ctx, contents, &genai.GenerateContentConfig{
			SystemInstruction: system,
			Tools:             enabledGeminiTools(tools, useTools),
			MaxOutputTokens:   int32(httpProviderMaxOutputTokens),
		}, cb)
		if err != nil {
			return finalHistory, err
		}
		if len(resp.Candidates) == 0 || resp.Candidates[0] == nil {
			return finalHistory, fmt.Errorf("gemini: response had no candidates")
		}

		content := resp.Candidates[0].Content
		if content == nil {
			content = genai.NewContentFromParts(nil, genai.RoleModel)
		}
		if content.Role == "" {
			content.Role = string(genai.RoleModel)
		}
		functionCalls := geminiFunctionCalls(content)
		if len(functionCalls) == 0 {
			if resp.Candidates[0].FinishReason == genai.FinishReasonMaxTokens && cb.OnText != nil {
				cb.OnText("\n\n_(Reply truncated at the length limit - ask me to continue.)_")
			}
			if len(content.Parts) > 0 {
				finalHistory = append(finalHistory, geminiContentToTranscript(content))
			}
			return finalHistory, nil
		}

		contents = append(contents, content)
		finalHistory = append(finalHistory, geminiContentToTranscript(content))
		resultParts := make([]*genai.Part, 0, len(functionCalls))
		toolResultBlocks := make([]transcriptBlock, 0, len(functionCalls))
		for _, call := range functionCalls {
			result, transcriptBlock, toolErr := s.runGeminiTool(ctx, call, chatCtx, cb)
			if toolErr != nil {
				return finalHistory, toolErr
			}
			resultParts = append(resultParts, result)
			toolResultBlocks = append(toolResultBlocks, transcriptBlock)
		}
		contents = append(contents, genai.NewContentFromParts(resultParts, genai.RoleUser))
		finalHistory = append(finalHistory, transcriptMessage{Role: agentRoleUser, Content: toolResultBlocks})
	}

	return finalHistory, fmt.Errorf("agent loop exceeded %d iterations", maxToolIterations)
}

func (s *geminiService) streamGenerate(ctx context.Context, contents []*genai.Content, config *genai.GenerateContentConfig, cb StreamCallbacks) (*genai.GenerateContentResponse, error) {
	for attempt := 0; attempt < geminiSemanticMaxAttempts; attempt++ {
		response, emittedText, err := s.streamGenerateOnce(ctx, contents, config, cb)
		if err == nil {
			return response, nil
		}
		if errors.Is(err, errGeminiIncompleteStream) && (emittedText || attempt == geminiSemanticMaxAttempts-1) {
			// Text already delivered cannot be replayed, and a second clean EOF is
			// enough evidence to accept the fully parsed response as an implicit stop.
			return response, nil
		}
		retryable := errors.Is(err, errGeminiIncompleteStream) || isRetryableGeminiError(err)
		if emittedText || !retryable || attempt == geminiSemanticMaxAttempts-1 {
			return nil, err
		}
		if waitErr := waitForGeminiSemanticRetry(ctx, attempt); waitErr != nil {
			return nil, fmt.Errorf("gemini semantic retry: %w", waitErr)
		}
	}
	return nil, errGeminiIncompleteStream
}

// streamGenerateOnce owns fresh aggregation state. An incomplete stream may be
// replayed only before text has reached a callback; function calls remain local
// until this method returns, so retrying them cannot duplicate tool effects.
func (s *geminiService) streamGenerateOnce(ctx context.Context, contents []*genai.Content, config *genai.GenerateContentConfig, cb StreamCallbacks) (*genai.GenerateContentResponse, bool, error) {
	aggregated := &genai.GenerateContentResponse{}
	content := genai.NewContentFromParts(nil, genai.RoleModel)
	var finishReason genai.FinishReason
	var finishMessage string
	sawCandidate := false
	emittedText := false

	err := s.forEachGeminiStream(ctx, contents, config, func(chunk *genai.GenerateContentResponse) {
		if chunk == nil {
			return
		}
		if chunk.PromptFeedback != nil {
			aggregated.PromptFeedback = chunk.PromptFeedback
		}
		if chunk.UsageMetadata != nil {
			aggregated.UsageMetadata = chunk.UsageMetadata
		}
		if len(chunk.Candidates) == 0 || chunk.Candidates[0] == nil {
			return
		}

		sawCandidate = true
		candidate := chunk.Candidates[0]
		if candidate.FinishReason != "" {
			finishReason = candidate.FinishReason
		}
		if candidate.FinishMessage != "" {
			finishMessage = candidate.FinishMessage
		}
		if candidate.Content == nil {
			return
		}
		if candidate.Content.Role != "" {
			content.Role = candidate.Content.Role
		}
		for _, part := range candidate.Content.Parts {
			if part == nil {
				continue
			}
			content.Parts = append(content.Parts, part)
			if part.Text != "" && !part.Thought && cb.OnText != nil {
				emittedText = true
				cb.OnText(part.Text)
			}
		}
	})
	if err != nil {
		return nil, emittedText, fmt.Errorf("gemini stream generate: %w", err)
	}
	if reason := geminiPromptBlockReason(aggregated.PromptFeedback); reason != "" {
		return nil, emittedText, fmt.Errorf("gemini: prompt blocked (%s)", reason)
	}
	if !sawCandidate {
		return nil, emittedText, fmt.Errorf("gemini: response had no candidates")
	}
	if (finishReason == "" || finishReason == genai.FinishReasonUnspecified) && geminiContentHasUsableOutput(content) {
		aggregated.Candidates = []*genai.Candidate{{
			Content: content,
		}}
		return aggregated, emittedText, errGeminiIncompleteStream
	}
	if err := validateGeminiFinishReason(finishReason); err != nil {
		return nil, emittedText, err
	}
	if !geminiContentHasUsableOutput(content) {
		return nil, emittedText, fmt.Errorf("gemini: response contained no text or tool calls")
	}

	aggregated.Candidates = []*genai.Candidate{{
		Content:       content,
		FinishReason:  finishReason,
		FinishMessage: finishMessage,
	}}
	return aggregated, emittedText, nil
}

func (s *geminiService) forEachGeminiStream(ctx context.Context, contents []*genai.Content, config *genai.GenerateContentConfig, consume func(*genai.GenerateContentResponse)) error {
	for attempt := 0; attempt < geminiStreamMaxAttempts; attempt++ {
		sawOutput := false
		pendingMetadata := make([]*genai.GenerateContentResponse, 0, 4)
		var streamErr error
		for chunk, err := range s.client.Models.GenerateContentStream(ctx, s.model, contents, config) {
			if err != nil {
				streamErr = err
				break
			}
			if !sawOutput && !geminiChunkHasOutput(chunk) && len(pendingMetadata) < geminiMetadataBufferLimit {
				pendingMetadata = append(pendingMetadata, chunk)
				continue
			}
			if !sawOutput {
				sawOutput = true
				for _, pending := range pendingMetadata {
					consume(pending)
				}
				pendingMetadata = nil
			}
			consume(chunk)
		}
		if streamErr == nil {
			for _, pending := range pendingMetadata {
				consume(pending)
			}
			return nil
		}
		if sawOutput || attempt == geminiStreamMaxAttempts-1 || !isRetryableGeminiError(streamErr) {
			return streamErr
		}
		timer := time.NewTimer(geminiRetryBaseDelay << attempt)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func geminiChunkHasOutput(chunk *genai.GenerateContentResponse) bool {
	if chunk == nil {
		return false
	}
	for _, candidate := range chunk.Candidates {
		if candidate == nil {
			continue
		}
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part != nil && (part.Text != "" || len(part.ThoughtSignature) > 0 || part.FunctionCall != nil) {
				return true
			}
		}
	}
	return false
}

func isRetryableGeminiError(err error) bool {
	var apiErr genai.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == http.StatusRequestTimeout ||
		apiErr.Code == http.StatusConflict ||
		apiErr.Code == http.StatusTooManyRequests ||
		apiErr.Code >= http.StatusInternalServerError
}

func geminiPromptBlockReason(feedback *genai.GenerateContentResponsePromptFeedback) string {
	if feedback == nil || feedback.BlockReason == "" || feedback.BlockReason == genai.BlockedReasonUnspecified {
		return ""
	}
	return string(feedback.BlockReason)
}

func validateGeminiFinishReason(reason genai.FinishReason) error {
	switch reason {
	case genai.FinishReasonStop, genai.FinishReasonMaxTokens:
		return nil
	case "", genai.FinishReasonUnspecified:
		return fmt.Errorf("gemini: stream ended without a terminal finish reason")
	default:
		return fmt.Errorf("gemini: generation stopped (%s)", reason)
	}
}

func geminiContentHasUsableOutput(content *genai.Content) bool {
	if content == nil {
		return false
	}
	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		if (!part.Thought && strings.TrimSpace(part.Text) != "") || part.FunctionCall != nil {
			return true
		}
	}
	return false
}

func (s *geminiService) runGeminiTool(ctx context.Context, call *genai.FunctionCall, chatCtx ChatContext, cb StreamCallbacks) (*genai.Part, transcriptBlock, error) {
	if cb.OnToolStart != nil {
		cb.OnToolStart(call.Name, toolLabel(call.Name))
	}

	input, err := json.Marshal(call.Args)
	if err != nil || len(input) == 0 || string(input) == "null" {
		input = []byte("{}")
	}

	result, err := s.toolServer.ExecuteTool(ctx, call.Name, json.RawMessage(input), mcp.CallContext{
		UserID:          chatCtx.UserID,
		DeviceID:        chatCtx.DeviceID,
		Role:            chatCtx.Role,
		RequireSharedAI: chatCtx.RequireSharedAI,
		Reauthorize:     true,
	})
	if err != nil {
		if cb.OnToolEnd != nil {
			cb.OnToolEnd(call.Name, false)
		}
		if errors.Is(err, mcp.ErrToolAuthorization) {
			return nil, transcriptBlock{}, mcp.ErrToolAuthorization
		}
		content := "Error: " + err.Error()
		return &genai.Part{FunctionResponse: &genai.FunctionResponse{
				Name:     call.Name,
				ID:       call.ID,
				Response: map[string]any{"error": err.Error()},
			}}, transcriptBlock{
				Type:      blockTypeToolResult,
				ToolUseID: call.ID,
				Name:      call.Name,
				Content:   content,
				IsError:   true,
			}, nil
	}
	if result.StructuredData != nil && mcp.ToolsWithUI[call.Name] && cb.OnToolResult != nil {
		cb.OnToolResult(call.Name, result.StructuredData)
	}
	if cb.OnToolEnd != nil {
		cb.OnToolEnd(call.Name, true)
	}
	return &genai.Part{FunctionResponse: &genai.FunctionResponse{
			Name:     call.Name,
			ID:       call.ID,
			Response: map[string]any{"response": result.Text},
		}}, transcriptBlock{
			Type:      blockTypeToolResult,
			ToolUseID: call.ID,
			Name:      call.Name,
			Content:   result.Text,
		}, nil
}

func toGeminiContents(messages transcript) []*genai.Content {
	out := make([]*genai.Content, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case agentRoleAssistant:
			if len(out) == 0 {
				continue
			}
			content := genai.NewContentFromParts(nil, genai.RoleModel)
			for _, block := range m.Content {
				switch block.Type {
				case blockTypeText:
					if block.Text != "" {
						content.Parts = append(content.Parts, &genai.Part{
							Text: block.Text, ThoughtSignature: append([]byte(nil), block.ThoughtSignature...),
						})
					}
				case blockTypeGeminiThought:
					content.Parts = append(content.Parts, &genai.Part{
						Text: block.Text, Thought: true, ThoughtSignature: append([]byte(nil), block.ThoughtSignature...),
					})
				case blockTypeToolUse:
					content.Parts = append(content.Parts, &genai.Part{
						FunctionCall: &genai.FunctionCall{
							Name: block.Name,
							Args: rawJSONMap(block.Input),
							ID:   block.ID,
						},
						ThoughtSignature: append([]byte(nil), block.ThoughtSignature...),
					})
				}
			}
			if len(content.Parts) > 0 {
				out = append(out, content)
			}
		default:
			content := genai.NewContentFromParts(nil, genai.RoleUser)
			for _, block := range m.Content {
				switch block.Type {
				case blockTypeText:
					if block.Text != "" {
						content.Parts = append(content.Parts, genai.NewPartFromText(block.Text))
					}
				case blockTypeToolResult:
					name := block.Name
					if name == "" {
						name = toolNameForResult(messages, block.ToolUseID)
					}
					content.Parts = append(content.Parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{
						Name:     name,
						ID:       block.ToolUseID,
						Response: geminiToolResponse(block),
					}})
				}
			}
			if len(content.Parts) > 0 {
				out = append(out, content)
			}
		}
	}
	return out
}

func geminiContentToTranscript(content *genai.Content) transcriptMessage {
	out := transcriptMessage{Role: agentRoleAssistant}
	if content == nil {
		return out
	}
	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		signature := append([]byte(nil), part.ThoughtSignature...)
		switch {
		case part.FunctionCall != nil:
			input, err := json.Marshal(part.FunctionCall.Args)
			if err != nil || len(input) == 0 || string(input) == "null" {
				input = []byte("{}")
			}
			out.Content = append(out.Content, transcriptBlock{
				Type:             blockTypeToolUse,
				ID:               part.FunctionCall.ID,
				Name:             part.FunctionCall.Name,
				Input:            json.RawMessage(input),
				ThoughtSignature: signature,
			})
		case part.Thought:
			out.Content = append(out.Content, transcriptBlock{
				Type: blockTypeGeminiThought, Text: part.Text, ThoughtSignature: signature,
			})
		case part.Text != "" || len(signature) > 0:
			out.Content = append(out.Content, transcriptBlock{
				Type: blockTypeText, Text: part.Text, ThoughtSignature: signature,
			})
		}
	}
	return out
}

func geminiToolResponse(block transcriptBlock) map[string]any {
	if block.IsError {
		return map[string]any{"error": strings.TrimPrefix(block.Content, "Error: ")}
	}
	return map[string]any{"response": block.Content}
}

func toolNameForResult(messages transcript, toolUseID string) string {
	for i := len(messages) - 1; i >= 0; i-- {
		for _, block := range messages[i].Content {
			if block.Type == blockTypeToolUse && block.ID == toolUseID {
				return block.Name
			}
		}
	}
	return ""
}

func toGeminiTools(tools []mcp.Tool) []*genai.Tool {
	declarations := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		declarations = append(declarations, &genai.FunctionDeclaration{
			Name:                 t.Name,
			Description:          t.Description,
			ParametersJsonSchema: t.InputSchema,
		})
	}
	if len(declarations) == 0 {
		return nil
	}
	return []*genai.Tool{{FunctionDeclarations: declarations}}
}

func enabledGeminiTools(tools []*genai.Tool, enabled bool) []*genai.Tool {
	if !enabled {
		return nil
	}
	return tools
}

func geminiFunctionCalls(content *genai.Content) []*genai.FunctionCall {
	var calls []*genai.FunctionCall
	if content == nil {
		return calls
	}
	for _, part := range content.Parts {
		if part != nil && part.FunctionCall != nil {
			calls = append(calls, part.FunctionCall)
		}
	}
	return calls
}

func rawJSONString(raw json.RawMessage) string {
	raw = normalizedRawJSON(raw)
	return string(raw)
}

func rawJSONMap(raw json.RawMessage) map[string]any {
	value := rawJSONValue(raw)
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func normalizedRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" || !json.Valid(raw) {
		return json.RawMessage("{}")
	}
	return raw
}
