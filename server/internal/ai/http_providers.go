package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mcp"
)

const httpProviderMaxOutputTokens = 16000

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

func (s *openAIService) SendMessage(ctx context.Context, history []Message, chatCtx ChatContext, cb StreamCallbacks) error {
	messages := []openAIMessage{{
		Role:    "system",
		Content: systemPrompt + "\n\n" + dynamicContext(chatCtx),
	}}
	messages = append(messages, toOpenAIMessages(history)...)
	tools := toOpenAITools(s.toolServer.GetToolsForRole(chatCtx.Role))

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		req := openAIChatRequest{
			Model:               s.model,
			Messages:            messages,
			MaxCompletionTokens: httpProviderMaxOutputTokens,
			Tools:               tools,
		}
		if iteration == maxToolIterations-1 {
			req.ToolChoice = "none"
		}

		message, finishReason, err := s.chat(ctx, req)
		if err != nil {
			return err
		}
		if len(message.ToolCalls) == 0 {
			if message.Content != "" && cb.OnText != nil {
				cb.OnText(message.Content)
			}
			if finishReason == "length" && cb.OnText != nil {
				cb.OnText("\n\n_(Reply truncated at the length limit - ask me to continue.)_")
			}
			return nil
		}

		messages = append(messages, message)
		for _, toolCall := range message.ToolCalls {
			messages = append(messages, s.runOpenAITool(ctx, toolCall, chatCtx, cb))
		}
	}

	return fmt.Errorf("agent loop exceeded %d iterations", maxToolIterations)
}

func (s *openAIService) chat(ctx context.Context, payload openAIChatRequest) (openAIMessage, string, error) {
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

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return openAIMessage{}, "", fmt.Errorf("openai chat: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return openAIMessage{}, "", err
	}
	if resp.StatusCode >= 300 {
		return openAIMessage{}, "", fmt.Errorf("openai chat: %s", providerErrorMessage(data))
	}

	var parsed openAIChatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return openAIMessage{}, "", fmt.Errorf("decode openai chat: %w", err)
	}
	if parsed.Error != nil {
		return openAIMessage{}, "", fmt.Errorf("openai chat: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return openAIMessage{}, "", fmt.Errorf("openai chat: response had no choices")
	}
	return parsed.Choices[0].Message, parsed.Choices[0].FinishReason, nil
}

func (s *openAIService) runOpenAITool(ctx context.Context, toolCall openAIToolCall, chatCtx ChatContext, cb StreamCallbacks) openAIMessage {
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
		return openAIMessage{Role: "tool", ToolCallID: toolCall.ID, Content: "Error: " + err.Error()}
	}
	if result.StructuredData != nil && mcp.ToolsWithUI[name] && cb.OnToolResult != nil {
		cb.OnToolResult(name, result.StructuredData)
	}
	if cb.OnToolEnd != nil {
		cb.OnToolEnd(name, true)
	}
	return openAIMessage{Role: "tool", ToolCallID: toolCall.ID, Content: result.Text}
}

type openAIChatRequest struct {
	Model               string          `json:"model"`
	Messages            []openAIMessage `json:"messages"`
	Tools               []openAITool    `json:"tools,omitempty"`
	ToolChoice          any             `json:"tool_choice,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message      openAIMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Error *providerError `json:"error,omitempty"`
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

func toOpenAIMessages(messages []Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for _, m := range messages {
		text := messageText(m.Content)
		if text == "" {
			continue
		}
		switch m.Role {
		case "assistant":
			if len(out) == 0 {
				continue
			}
			out = append(out, openAIMessage{Role: "assistant", Content: text})
		default:
			out = append(out, openAIMessage{Role: "user", Content: text})
		}
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

func (s *geminiService) SendMessage(ctx context.Context, history []Message, chatCtx ChatContext, cb StreamCallbacks) error {
	contents := toGeminiContents(history)
	tools := toGeminiTools(s.toolServer.GetToolsForRole(chatCtx.Role))
	system := geminiContent{Parts: []geminiPart{{Text: systemPrompt + "\n\n" + dynamicContext(chatCtx)}}}

	for iteration := 0; iteration < maxToolIterations; iteration++ {
		useTools := iteration != maxToolIterations-1
		resp, err := s.generate(ctx, geminiGenerateRequest{
			SystemInstruction: &system,
			Contents:          contents,
			Tools:             enabledGeminiTools(tools, useTools),
			GenerationConfig:  &geminiGenerationConfig{MaxOutputTokens: httpProviderMaxOutputTokens},
		})
		if err != nil {
			return err
		}
		if len(resp.Candidates) == 0 {
			return fmt.Errorf("gemini: response had no candidates")
		}

		content := resp.Candidates[0].Content
		if content.Role == "" {
			content.Role = "model"
		}
		functionCalls := geminiFunctionCalls(content)
		if len(functionCalls) == 0 {
			text := geminiText(content)
			if text != "" && cb.OnText != nil {
				cb.OnText(text)
			}
			if resp.Candidates[0].FinishReason == "MAX_TOKENS" && cb.OnText != nil {
				cb.OnText("\n\n_(Reply truncated at the length limit - ask me to continue.)_")
			}
			return nil
		}

		contents = append(contents, content)
		resultParts := make([]geminiPart, 0, len(functionCalls))
		for _, call := range functionCalls {
			resultParts = append(resultParts, s.runGeminiTool(ctx, call, chatCtx, cb))
		}
		contents = append(contents, geminiContent{Role: "user", Parts: resultParts})
	}

	return fmt.Errorf("agent loop exceeded %d iterations", maxToolIterations)
}

func (s *geminiService) generate(ctx context.Context, payload geminiGenerateRequest) (geminiGenerateResponse, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return geminiGenerateResponse{}, err
	}
	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent",
		url.PathEscape(s.model),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return geminiGenerateResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return geminiGenerateResponse{}, fmt.Errorf("gemini generate: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return geminiGenerateResponse{}, err
	}
	if resp.StatusCode >= 300 {
		return geminiGenerateResponse{}, fmt.Errorf("gemini generate: %s", providerErrorMessage(data))
	}

	var parsed geminiGenerateResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return geminiGenerateResponse{}, fmt.Errorf("decode gemini response: %w", err)
	}
	if parsed.Error != nil {
		return geminiGenerateResponse{}, fmt.Errorf("gemini generate: %s", parsed.Error.Message)
	}
	return parsed, nil
}

func (s *geminiService) runGeminiTool(ctx context.Context, call geminiFunctionCall, chatCtx ChatContext, cb StreamCallbacks) geminiPart {
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
		return geminiPart{FunctionResponse: &geminiFunctionResponse{
			Name:     call.Name,
			ID:       call.ID,
			Response: map[string]any{"error": err.Error()},
		}}
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
	}}
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

func toGeminiContents(messages []Message) []geminiContent {
	out := make([]geminiContent, 0, len(messages))
	for _, m := range messages {
		text := messageText(m.Content)
		if text == "" {
			continue
		}
		switch m.Role {
		case "assistant":
			if len(out) == 0 {
				continue
			}
			out = append(out, geminiContent{Role: "model", Parts: []geminiPart{{Text: text}}})
		default:
			out = append(out, geminiContent{Role: "user", Parts: []geminiPart{{Text: text}}})
		}
	}
	return out
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
		sb.WriteString(part.Text)
	}
	return sb.String()
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
