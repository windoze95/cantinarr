package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mcp"
)

const httpProviderMaxOutputTokens = 16000

var errSSEDone = errors.New("sse stream complete")

type openAIService struct {
	apiKey     string
	model      string
	httpClient *http.Client
	toolServer *mcp.ToolServer
}

func NewOpenAIService(apiKey, model string, toolServer *mcp.ToolServer) *openAIService {
	if model == "" {
		model = "gpt-5.5"
	}
	return &openAIService{
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
		toolServer: toolServer,
	}
}

func (s *openAIService) SendMessage(ctx context.Context, history transcript, chatCtx ChatContext, cb StreamCallbacks) (transcript, error) {
	messages := []openAIMessage{{
		Role:    "system",
		Content: systemPrompt + "\n\n" + dynamicContext(chatCtx),
	}}
	messages = append(messages, toOpenAIMessages(history)...)
	tools := toOpenAITools(s.toolServer.GetToolsForRole(chatCtx.Role))
	finalHistory := cloneTranscript(history)

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		req := openAIChatRequest{
			Model:               s.model,
			Messages:            messages,
			MaxCompletionTokens: httpProviderMaxOutputTokens,
			Tools:               tools,
			Stream:              true,
		}
		if iteration == maxToolIterations-1 {
			req.ToolChoice = "none"
		}

		message, finishReason, err := s.chatStream(ctx, req, cb)
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

		messages = append(messages, message)
		finalHistory = append(finalHistory, openAIMessageToTranscript(message))
		var toolResultBlocks []transcriptBlock
		for _, toolCall := range message.ToolCalls {
			result, transcriptBlock := s.runOpenAITool(ctx, toolCall, chatCtx, cb)
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

func (s *openAIService) chatStream(ctx context.Context, payload openAIChatRequest, cb StreamCallbacks) (openAIMessage, string, error) {
	payload.Stream = true
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return openAIMessage{}, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", &body)
	if err != nil {
		return openAIMessage{}, "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return openAIMessage{}, "", fmt.Errorf("openai chat stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return openAIMessage{}, "", fmt.Errorf("openai chat stream: %s", providerErrorMessage(data))
	}

	message := openAIMessage{Role: "assistant"}
	toolCalls := make(map[int]*openAIToolCall)
	var toolOrder []int
	finishReason := ""

	err = readSSE(resp.Body, func(data string) error {
		if strings.TrimSpace(data) == "[DONE]" {
			return errSSEDone
		}
		var parsed openAIChatStreamResponse
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			return fmt.Errorf("decode openai stream: %w", err)
		}
		if parsed.Error != nil {
			return fmt.Errorf("openai chat stream: %s", parsed.Error.Message)
		}
		for _, choice := range parsed.Choices {
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			delta := choice.Delta
			if delta.Role != "" {
				message.Role = delta.Role
			}
			if delta.Content != "" {
				message.Content += delta.Content
				if cb.OnText != nil {
					cb.OnText(delta.Content)
				}
			}
			for _, toolDelta := range delta.ToolCalls {
				call := toolCalls[toolDelta.Index]
				if call == nil {
					call = &openAIToolCall{Type: "function"}
					toolCalls[toolDelta.Index] = call
					toolOrder = append(toolOrder, toolDelta.Index)
				}
				if toolDelta.ID != "" {
					call.ID = toolDelta.ID
				}
				if toolDelta.Type != "" {
					call.Type = toolDelta.Type
				}
				if toolDelta.Function.Name != "" {
					call.Function.Name += toolDelta.Function.Name
				}
				if toolDelta.Function.Arguments != "" {
					call.Function.Arguments += toolDelta.Function.Arguments
				}
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSSEDone) {
		return openAIMessage{}, "", err
	}

	sort.Ints(toolOrder)
	for _, index := range toolOrder {
		call := toolCalls[index]
		if call == nil || call.ID == "" || call.Function.Name == "" {
			continue
		}
		if call.Type == "" {
			call.Type = "function"
		}
		message.ToolCalls = append(message.ToolCalls, *call)
	}
	return message, finishReason, nil
}

func (s *openAIService) runOpenAITool(ctx context.Context, toolCall openAIToolCall, chatCtx ChatContext, cb StreamCallbacks) (openAIMessage, transcriptBlock) {
	name := toolCall.Function.Name
	if cb.OnToolStart != nil {
		cb.OnToolStart(name, toolLabel(name))
	}

	input := json.RawMessage(toolCall.Function.Arguments)
	if len(input) == 0 || string(input) == "null" {
		input = json.RawMessage("{}")
	}

	result, err := s.toolServer.ExecuteTool(ctx, name, input, mcp.CallContext{UserID: chatCtx.UserID, Role: chatCtx.Role})
	if err != nil {
		if cb.OnToolEnd != nil {
			cb.OnToolEnd(name, false)
		}
		content := "Error: " + err.Error()
		return openAIMessage{Role: "tool", ToolCallID: toolCall.ID, Content: content}, transcriptBlock{
			Type:      blockTypeToolResult,
			ToolUseID: toolCall.ID,
			Name:      name,
			Content:   content,
			IsError:   true,
		}
	}
	if result.StructuredData != nil && mcp.ToolsWithUI[name] && cb.OnToolResult != nil {
		cb.OnToolResult(name, result.StructuredData)
	}
	if cb.OnToolEnd != nil {
		cb.OnToolEnd(name, true)
	}
	return openAIMessage{Role: "tool", ToolCallID: toolCall.ID, Content: result.Text}, transcriptBlock{
		Type:      blockTypeToolResult,
		ToolUseID: toolCall.ID,
		Name:      name,
		Content:   result.Text,
	}
}

type openAIChatRequest struct {
	Model               string          `json:"model"`
	Messages            []openAIMessage `json:"messages"`
	Tools               []openAITool    `json:"tools,omitempty"`
	ToolChoice          any             `json:"tool_choice,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Stream              bool            `json:"stream,omitempty"`
}

type openAIChatStreamResponse struct {
	Choices []struct {
		Delta        openAIMessageDelta `json:"delta"`
		FinishReason string             `json:"finish_reason"`
	} `json:"choices"`
	Error *providerError `json:"error,omitempty"`
}

type openAIMessageDelta struct {
	Role      string                `json:"role,omitempty"`
	Content   string                `json:"content,omitempty"`
	ToolCalls []openAIToolCallDelta `json:"tool_calls,omitempty"`
}

type openAIToolCallDelta struct {
	Index    int                     `json:"index"`
	ID       string                  `json:"id,omitempty"`
	Type     string                  `json:"type,omitempty"`
	Function openAIFunctionCallDelta `json:"function,omitempty"`
}

type openAIFunctionCallDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIFunctionTool `json:"function"`
}

type openAIFunctionTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters"`
}

func toOpenAIMessages(messages transcript) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
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
				out = append(out, message)
			}
		default:
			var userText strings.Builder
			for _, block := range m.Content {
				switch block.Type {
				case blockTypeText:
					userText.WriteString(block.Text)
				case blockTypeToolResult:
					out = append(out, openAIMessage{
						Role:       "tool",
						ToolCallID: block.ToolUseID,
						Content:    block.Content,
					})
				}
			}
			if userText.Len() > 0 {
				out = append(out, openAIMessage{Role: "user", Content: userText.String()})
			}
		}
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

func toOpenAITools(tools []mcp.Tool) []openAITool {
	out := make([]openAITool, 0, len(tools))
	for _, t := range tools {
		out = append(out, openAITool{
			Type: "function",
			Function: openAIFunctionTool{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return out
}

type geminiService struct {
	apiKey     string
	model      string
	httpClient *http.Client
	toolServer *mcp.ToolServer
}

func NewGeminiService(apiKey, model string, toolServer *mcp.ToolServer) *geminiService {
	if model == "" {
		model = "gemini-3.5-flash"
	}
	return &geminiService{
		apiKey:     apiKey,
		model:      strings.TrimPrefix(model, "models/"),
		httpClient: &http.Client{Timeout: 5 * time.Minute},
		toolServer: toolServer,
	}
}

func (s *geminiService) SendMessage(ctx context.Context, history transcript, chatCtx ChatContext, cb StreamCallbacks) (transcript, error) {
	contents := toGeminiContents(history)
	tools := toGeminiTools(s.toolServer.GetToolsForRole(chatCtx.Role))
	system := geminiContent{Parts: []geminiPart{{Text: systemPrompt + "\n\n" + dynamicContext(chatCtx)}}}
	finalHistory := cloneTranscript(history)

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		useTools := iteration != maxToolIterations-1
		resp, err := s.streamGenerate(ctx, geminiGenerateRequest{
			SystemInstruction: &system,
			Contents:          contents,
			Tools:             enabledGeminiTools(tools, useTools),
			GenerationConfig:  &geminiGenerationConfig{MaxOutputTokens: httpProviderMaxOutputTokens},
		}, cb)
		if err != nil {
			return finalHistory, err
		}
		if len(resp.Candidates) == 0 {
			return finalHistory, fmt.Errorf("gemini: response had no candidates")
		}

		content := resp.Candidates[0].Content
		if content.Role == "" {
			content.Role = "model"
		}
		functionCalls := geminiFunctionCalls(content)
		if len(functionCalls) == 0 {
			if resp.Candidates[0].FinishReason == "MAX_TOKENS" && cb.OnText != nil {
				cb.OnText("\n\n_(Reply truncated at the length limit - ask me to continue.)_")
			}
			if len(content.Parts) > 0 {
				finalHistory = append(finalHistory, geminiContentToTranscript(content))
			}
			return finalHistory, nil
		}

		contents = append(contents, content)
		finalHistory = append(finalHistory, geminiContentToTranscript(content))
		resultParts := make([]geminiPart, 0, len(functionCalls))
		toolResultBlocks := make([]transcriptBlock, 0, len(functionCalls))
		for _, call := range functionCalls {
			result, transcriptBlock := s.runGeminiTool(ctx, call, chatCtx, cb)
			resultParts = append(resultParts, result)
			toolResultBlocks = append(toolResultBlocks, transcriptBlock)
		}
		contents = append(contents, geminiContent{Role: "user", Parts: resultParts})
		finalHistory = append(finalHistory, transcriptMessage{Role: agentRoleUser, Content: toolResultBlocks})
	}

	return finalHistory, fmt.Errorf("agent loop exceeded %d iterations", maxToolIterations)
}

func (s *geminiService) streamGenerate(ctx context.Context, payload geminiGenerateRequest, cb StreamCallbacks) (geminiGenerateResponse, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return geminiGenerateResponse{}, err
	}
	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse",
		url.PathEscape(s.model),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return geminiGenerateResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("x-goog-api-key", s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return geminiGenerateResponse{}, fmt.Errorf("gemini stream generate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return geminiGenerateResponse{}, fmt.Errorf("gemini stream generate: %s", providerErrorMessage(data))
	}

	var aggregated geminiGenerateResponse
	content := geminiContent{Role: "model"}
	finishReason := ""

	err = readSSE(resp.Body, func(data string) error {
		if strings.TrimSpace(data) == "[DONE]" {
			return errSSEDone
		}
		var chunk geminiGenerateResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode gemini stream: %w", err)
		}
		if chunk.Error != nil {
			return fmt.Errorf("gemini stream generate: %s", chunk.Error.Message)
		}
		if len(chunk.Candidates) == 0 {
			return nil
		}
		candidate := chunk.Candidates[0]
		if candidate.FinishReason != "" {
			finishReason = candidate.FinishReason
		}
		if candidate.Content.Role != "" {
			content.Role = candidate.Content.Role
		}
		for _, part := range candidate.Content.Parts {
			content.Parts = append(content.Parts, part)
			if part.Text != "" && !part.Thought && cb.OnText != nil {
				cb.OnText(part.Text)
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSSEDone) {
		return geminiGenerateResponse{}, err
	}

	aggregated.Candidates = append(aggregated.Candidates, struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	}{Content: content, FinishReason: finishReason})
	return aggregated, nil
}

func (s *geminiService) runGeminiTool(ctx context.Context, call geminiFunctionCall, chatCtx ChatContext, cb StreamCallbacks) (geminiPart, transcriptBlock) {
	if cb.OnToolStart != nil {
		cb.OnToolStart(call.Name, toolLabel(call.Name))
	}

	input, err := json.Marshal(call.Args)
	if err != nil || len(input) == 0 || string(input) == "null" {
		input = []byte("{}")
	}

	result, err := s.toolServer.ExecuteTool(ctx, call.Name, json.RawMessage(input), mcp.CallContext{UserID: chatCtx.UserID, Role: chatCtx.Role})
	if err != nil {
		if cb.OnToolEnd != nil {
			cb.OnToolEnd(call.Name, false)
		}
		content := "Error: " + err.Error()
		return geminiPart{FunctionResponse: &geminiFunctionResponse{
				Name:     call.Name,
				ID:       call.ID,
				Response: map[string]any{"error": err.Error()},
			}}, transcriptBlock{
				Type:      blockTypeToolResult,
				ToolUseID: call.ID,
				Name:      call.Name,
				Content:   content,
				IsError:   true,
			}
	}
	if result.StructuredData != nil && mcp.ToolsWithUI[call.Name] && cb.OnToolResult != nil {
		cb.OnToolResult(call.Name, result.StructuredData)
	}
	if cb.OnToolEnd != nil {
		cb.OnToolEnd(call.Name, true)
	}
	return geminiPart{FunctionResponse: &geminiFunctionResponse{
			Name:     call.Name,
			ID:       call.ID,
			Response: map[string]any{"response": result.Text},
		}}, transcriptBlock{
			Type:      blockTypeToolResult,
			ToolUseID: call.ID,
			Name:      call.Name,
			Content:   result.Text,
		}
}

type geminiGenerateRequest struct {
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Contents          []geminiContent         `json:"contents"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type geminiGenerateResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	Error *providerError `json:"error,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
	ID   string         `json:"id,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
	ID       string         `json:"id,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type geminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters"`
}

func toGeminiContents(messages transcript) []geminiContent {
	out := make([]geminiContent, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case agentRoleAssistant:
			if len(out) == 0 {
				continue
			}
			content := geminiContent{Role: "model"}
			for _, block := range m.Content {
				switch block.Type {
				case blockTypeText:
					if block.Text != "" {
						content.Parts = append(content.Parts, geminiPart{Text: block.Text})
					}
				case blockTypeToolUse:
					content.Parts = append(content.Parts, geminiPart{FunctionCall: &geminiFunctionCall{
						Name: block.Name,
						Args: rawJSONMap(block.Input),
						ID:   block.ID,
					}})
				}
			}
			if len(content.Parts) > 0 {
				out = append(out, content)
			}
		default:
			content := geminiContent{Role: "user"}
			for _, block := range m.Content {
				switch block.Type {
				case blockTypeText:
					if block.Text != "" {
						content.Parts = append(content.Parts, geminiPart{Text: block.Text})
					}
				case blockTypeToolResult:
					name := block.Name
					if name == "" {
						name = toolNameForResult(messages, block.ToolUseID)
					}
					content.Parts = append(content.Parts, geminiPart{FunctionResponse: &geminiFunctionResponse{
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

func geminiContentToTranscript(content geminiContent) transcriptMessage {
	out := transcriptMessage{Role: agentRoleAssistant}
	for _, part := range content.Parts {
		if part.Text != "" && !part.Thought {
			out.Content = append(out.Content, transcriptBlock{Type: blockTypeText, Text: part.Text})
		}
		if part.FunctionCall != nil {
			input, err := json.Marshal(part.FunctionCall.Args)
			if err != nil || len(input) == 0 || string(input) == "null" {
				input = []byte("{}")
			}
			out.Content = append(out.Content, transcriptBlock{
				Type:  blockTypeToolUse,
				ID:    part.FunctionCall.ID,
				Name:  part.FunctionCall.Name,
				Input: json.RawMessage(input),
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

func toGeminiTools(tools []mcp.Tool) []geminiTool {
	declarations := make([]geminiFunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		declarations = append(declarations, geminiFunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		})
	}
	if len(declarations) == 0 {
		return nil
	}
	return []geminiTool{{FunctionDeclarations: declarations}}
}

func enabledGeminiTools(tools []geminiTool, enabled bool) []geminiTool {
	if !enabled {
		return nil
	}
	return tools
}

func geminiFunctionCalls(content geminiContent) []geminiFunctionCall {
	var calls []geminiFunctionCall
	for _, part := range content.Parts {
		if part.FunctionCall != nil {
			calls = append(calls, *part.FunctionCall)
		}
	}
	return calls
}

func geminiText(content geminiContent) string {
	var sb strings.Builder
	for _, part := range content.Parts {
		if !part.Thought {
			sb.WriteString(part.Text)
		}
	}
	return sb.String()
}

func readSSE(r io.Reader, handle func(data string) error) error {
	reader := bufio.NewReader(r)
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		payload := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		if strings.TrimSpace(payload) == "" {
			return nil
		}
		return handle(payload)
	}

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if flushErr := flush(); flushErr != nil {
					return flushErr
				}
			case strings.HasPrefix(line, "data:"):
				value := strings.TrimPrefix(line, "data:")
				value = strings.TrimPrefix(value, " ")
				data.WriteString(value)
				data.WriteByte('\n')
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return flush()
			}
			return err
		}
	}
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

type providerError struct {
	Message string `json:"message"`
	Status  string `json:"status,omitempty"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

func providerErrorMessage(data []byte) string {
	var wrapped struct {
		Error *providerError `json:"error"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Error != nil && wrapped.Error.Message != "" {
		return wrapped.Error.Message
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "request failed"
	}
	if len(text) > 1000 {
		return text[:1000] + "..."
	}
	return text
}
