package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mcp"
)

const (
	anthropicURL   = "https://api.anthropic.com/v1/messages"
	anthropicModel = "claude-sonnet-4-20250514"
	maxTokens      = 4096
	systemPrompt   = `You are Cantinarr's AI assistant — a friendly, knowledgeable media companion. You help users discover movies and TV shows, check what's available on their media server, and make requests.

Guidelines:
- Be concise and conversational
- Use the search tools to find content before recommending
- Always check request status before suggesting a request
- When recommending content, include title, year, and a brief description
- If a user asks to request something, use the request_media tool
- Format lists neatly with bullets or numbers
- IMPORTANT: After selecting specific items to recommend, you MUST call the display_media tool with those items' TMDB IDs and media types. This controls which items appear in the visual carousel the user sees. Order items by relevance. Search results alone do NOT populate the carousel — only display_media does.`
)

// Message represents a chat message sent to/from the Anthropic API.
type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []ContentBlock
}

// ContentBlock is a typed block within a message's content array.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

// Service manages interactions with the Anthropic API.
type Service struct {
	apiKey     string
	toolServer *mcp.ToolServer
	httpClient *http.Client
}

// NewService creates a new AI service.
func NewService(apiKey string, toolServer *mcp.ToolServer) *Service {
	return &Service{
		apiKey:     apiKey,
		toolServer: toolServer,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// anthropicRequest is the request body for the Anthropic Messages API.
type anthropicRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system"`
	Messages  []Message     `json:"messages"`
	Tools     []mcp.Tool    `json:"tools,omitempty"`
	Stream    bool          `json:"stream"`
}

// anthropicResponse is a non-streaming response from the Anthropic API.
type anthropicResponse struct {
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
}

// streamEvent represents a parsed SSE event from the Anthropic streaming API.
type streamEvent struct {
	Type  string          `json:"type"`
	Index int             `json:"index"`
	Delta json.RawMessage `json:"delta,omitempty"`
	ContentBlock *ContentBlock `json:"content_block,omitempty"`
}

// SendMessage handles the full conversation loop with tool execution.
// It streams text back via onText and structured tool results via onToolResult.
func (s *Service) SendMessage(ctx context.Context, messages []Message, userID int64, onText func(string), onToolResult func(string, any)) error {
	tools := s.toolServer.GetTools()

	for {
		resp, err := s.callAnthropicStreaming(ctx, messages, tools, onText)
		if err != nil {
			return err
		}

		// If no tool use, we're done
		if resp.StopReason != "tool_use" {
			return nil
		}

		// Append assistant response to conversation
		messages = append(messages, Message{
			Role:    "assistant",
			Content: resp.Content,
		})

		// Execute each tool call and collect results
		var toolResults []ContentBlock
		for _, block := range resp.Content {
			if block.Type != "tool_use" {
				continue
			}
			toolResult, err := s.toolServer.ExecuteTool(ctx, block.Name, block.Input, userID)
			var resultText string
			if err != nil {
				resultText = fmt.Sprintf("Error: %s", err.Error())
			} else {
				resultText = toolResult.Text
				// Send structured data to the frontend for rich UI rendering
				if toolResult.StructuredData != nil && mcp.ToolsWithUI[block.Name] {
					onToolResult(block.Name, toolResult.StructuredData)
				}
			}
			toolResults = append(toolResults, ContentBlock{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   resultText,
			})
		}

		// Append tool results as user message
		messages = append(messages, Message{
			Role:    "user",
			Content: toolResults,
		})
	}
}

// callAnthropicStreaming sends a streaming request to the Anthropic API.
// It calls onText for each text delta and returns the full response with content blocks.
func (s *Service) callAnthropicStreaming(ctx context.Context, messages []Message, tools []mcp.Tool, onText func(string)) (*anthropicResponse, error) {
	reqBody := anthropicRequest{
		Model:     anthropicModel,
		MaxTokens: maxTokens,
		System:    systemPrompt,
		Messages:  messages,
		Tools:     tools,
		Stream:    true,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic API error (status %d): %s", resp.StatusCode, string(body))
	}

	return s.parseSSEStream(resp.Body, onText)
}

// parseSSEStream reads the Anthropic SSE stream, emitting text deltas and
// collecting the full response including any tool_use blocks.
func (s *Service) parseSSEStream(reader io.Reader, onText func(string)) (*anthropicResponse, error) {
	scanner := bufio.NewScanner(reader)
	// Increase buffer for potentially large responses
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var contentBlocks []ContentBlock
	var currentBlock *ContentBlock
	stopReason := "end_turn"

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event streamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			log.Printf("ai: failed to parse SSE event: %v", err)
			continue
		}

		switch event.Type {
		case "content_block_start":
			if event.ContentBlock != nil {
				block := *event.ContentBlock
				// Clear the initial empty input from tool_use blocks;
				// the real input arrives via input_json_delta events.
				if block.Type == "tool_use" {
					block.Input = nil
				}
				currentBlock = &block
			}

		case "content_block_delta":
			if currentBlock == nil {
				continue
			}
			var delta struct {
				Type  string          `json:"type"`
				Text  string          `json:"text,omitempty"`
				PartialJSON string   `json:"partial_json,omitempty"`
			}
			if err := json.Unmarshal(event.Delta, &delta); err != nil {
				continue
			}
			switch delta.Type {
			case "text_delta":
				currentBlock.Text += delta.Text
				onText(delta.Text)
			case "input_json_delta":
				// Accumulate raw JSON for tool input
				if currentBlock.Input == nil {
					currentBlock.Input = json.RawMessage(delta.PartialJSON)
				} else {
					currentBlock.Input = append(currentBlock.Input, []byte(delta.PartialJSON)...)
				}
			}

		case "content_block_stop":
			if currentBlock != nil {
				contentBlocks = append(contentBlocks, *currentBlock)
				currentBlock = nil
			}

		case "message_delta":
			var msgDelta struct {
				StopReason string `json:"stop_reason"`
			}
			if err := json.Unmarshal(event.Delta, &msgDelta); err == nil && msgDelta.StopReason != "" {
				stopReason = msgDelta.StopReason
			}

		case "message_stop":
			// End of message

		case "error":
			var errData struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal([]byte(data), &errData); err == nil {
				return nil, fmt.Errorf("anthropic stream error: %s", errData.Message)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SSE stream: %w", err)
	}

	return &anthropicResponse{
		Content:    contentBlocks,
		StopReason: stopReason,
	}, nil
}
