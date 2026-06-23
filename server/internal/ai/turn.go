package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	openai "github.com/openai/openai-go/v3"
	"google.golang.org/genai"

	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// This file adds an ADDITIVE single-turn seam used by the remediation agent. It
// runs ONE model turn with a caller-supplied system prompt + an explicit tool
// list over a provider-neutral transcript, returns the assistant turn (text +
// tool_use blocks) plus normalized token usage and a stop reason, and NEVER lets
// the provider execute a tool. The Runner inspects the returned tool_use blocks
// and dispatches them itself through its hardcoded read-tool allow-list, which is
// what makes a mutating tool architecturally unreachable.
//
// The existing chat SendMessage path is intentionally left byte-for-byte
// unchanged: these single-turn helpers stream one turn of their own and reuse
// only the existing provider-neutral converters (toSDKMessages, toOpenAIMessages,
// toGeminiContents and their inverses), never the chat loop's forced
// tool_choice:none or its 15-iteration loop.

// Stop reasons surfaced by NextTurn, normalized across providers.
const (
	StopReasonToolUse = "tool_use"
	StopReasonEndTurn = "end_turn"
	StopReasonMaxOut  = "max_tokens"
)

// Block roles in the exported transcript, mirroring the private transcript.
const (
	RoleUser      = agentRoleUser
	RoleAssistant = agentRoleAssistant

	BlockText       = blockTypeText
	BlockToolUse    = blockTypeToolUse
	BlockToolResult = blockTypeToolResult
)

// TranscriptBlock is one provider-neutral content block in the exported
// single-turn transcript. It mirrors the package-private transcriptBlock so
// remediation (a different package) can build history and read tool calls back
// without depending on the chat path's unexported types.
type TranscriptBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// TranscriptMessage is one role-tagged turn in the exported transcript.
type TranscriptMessage struct {
	Role    string            `json:"role"`
	Content []TranscriptBlock `json:"content"`
}

// Transcript is the provider-neutral, exported conversation the remediation
// Runner persists (untruncated) and rehydrates across human-gated pauses.
type Transcript []TranscriptMessage

// Usage is the normalized token accounting for one model turn. Fields a provider
// does not surface are left 0 (best-effort, per the design).
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
}

// TurnParams configures a single model turn.
type TurnParams struct {
	// System is the custom system prompt for this turn (NOT the chat const).
	System string
	// Tools is the EXPLICIT tool list offered to the model this turn. The Runner
	// passes only its read-tool allow-list + agent-only tools, so the model
	// cannot even see a mutating tool.
	Tools []mcp.Tool
	// History is the provider-neutral transcript to continue from.
	History Transcript
	// ForceNoTools omits tools / sets tool_choice:none so the model must answer
	// in text (used to coerce a final diagnosis when bounds are nearly spent).
	ForceNoTools bool
	// MaxTokens is the per-turn output cap. 0 falls back to a small default so a
	// single turn cannot blow the per-run cost ceiling.
	MaxTokens int
}

// TurnResult is the outcome of one model turn.
type TurnResult struct {
	// Message is the assistant turn (text + tool_use blocks). It never contains
	// tool_result blocks: the provider does not execute tools here.
	Message TranscriptMessage
	// Usage is the normalized token accounting for this turn.
	Usage Usage
	// StopReason is one of StopReasonToolUse / StopReasonEndTurn / StopReasonMaxOut.
	StopReason string
}

// TurnRunner runs one model turn without executing any tool. It is satisfied by
// each provider service so remediation gets whatever provider/model is
// configured.
type TurnRunner interface {
	NextTurn(ctx context.Context, p TurnParams) (TurnResult, error)
}

const defaultTurnMaxTokens = 4096

func turnMaxTokens(p TurnParams) int {
	if p.MaxTokens > 0 {
		return p.MaxTokens
	}
	return defaultTurnMaxTokens
}

// NewTurnRunner builds a single-turn runner for the given provider/model, mirroring
// handler.go's provider switch. apiKey + model come from the caller (remediation
// resolves them from credentials/settings), so no provider is hardcoded here.
func NewTurnRunner(provider, apiKey, model string, ts *mcp.ToolServer) (TurnRunner, error) {
	switch provider {
	case credentials.AIProviderAnthropic, "":
		return NewService(apiKey, model, ts), nil
	case credentials.AIProviderOpenAI:
		return NewOpenAIService(apiKey, model, ts), nil
	case credentials.AIProviderGemini:
		return NewGeminiService(apiKey, model, ts), nil
	default:
		return nil, fmt.Errorf("unsupported AI provider: %s", provider)
	}
}

// --- exported <-> private transcript conversion ---

func (t Transcript) toPrivate() transcript {
	out := make(transcript, 0, len(t))
	for _, m := range t {
		out = append(out, m.toPrivate())
	}
	return out
}

func (m TranscriptMessage) toPrivate() transcriptMessage {
	role := m.Role
	if role == "" {
		role = agentRoleUser
	}
	pm := transcriptMessage{Role: role}
	for _, b := range m.Content {
		pm.Content = append(pm.Content, transcriptBlock{
			Type:      b.Type,
			Text:      b.Text,
			ID:        b.ID,
			Name:      b.Name,
			Input:     append(json.RawMessage(nil), b.Input...),
			ToolUseID: b.ToolUseID,
			Content:   b.Content,
			IsError:   b.IsError,
		})
	}
	return pm
}

func exportMessage(m transcriptMessage) TranscriptMessage {
	role := m.Role
	if role == "" {
		role = agentRoleAssistant
	}
	em := TranscriptMessage{Role: role}
	for _, b := range m.Content {
		em.Content = append(em.Content, TranscriptBlock{
			Type:      b.Type,
			Text:      b.Text,
			ID:        b.ID,
			Name:      b.Name,
			Input:     append(json.RawMessage(nil), b.Input...),
			ToolUseID: b.ToolUseID,
			Content:   b.Content,
			IsError:   b.IsError,
		})
	}
	return em
}

// --- Anthropic single-turn ---

// NextTurn runs one Anthropic turn with p.System as the system prompt and p.Tools
// as the explicit tool list, streaming text but executing nothing.
func (s *Service) NextTurn(ctx context.Context, p TurnParams) (TurnResult, error) {
	params := anthropic.MessageNewParams{
		Model:     s.model,
		MaxTokens: int64(turnMaxTokens(p)),
		System: []anthropic.TextBlockParam{
			{Text: p.System, CacheControl: anthropic.NewCacheControlEphemeralParam()},
		},
		Messages: toSDKMessages(p.History.toPrivate()),
	}
	if !p.ForceNoTools && len(p.Tools) > 0 {
		params.Tools = toSDKTools(p.Tools)
	} else {
		params.ToolChoice = anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
	}
	if supportsAnthropicAdaptiveThinking(s.model) {
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}}
	}

	// Stream one turn with no callbacks (remediation streams via WS per persisted
	// step, not token-by-token). Reuse streamOne so accumulation/cache breakpoints
	// match the chat path exactly.
	message, err := s.streamOne(ctx, params, StreamCallbacks{})
	if err != nil {
		return TurnResult{}, err
	}
	return TurnResult{
		Message:    exportMessage(anthropicMessageToTranscript(*message)),
		Usage:      anthropicUsage(message.Usage),
		StopReason: normalizeAnthropicStop(message.StopReason),
	}, nil
}

func anthropicUsage(u anthropic.Usage) Usage {
	return Usage{
		InputTokens:         u.InputTokens,
		OutputTokens:        u.OutputTokens,
		CacheReadTokens:     u.CacheReadInputTokens,
		CacheCreationTokens: u.CacheCreationInputTokens,
	}
}

func normalizeAnthropicStop(r anthropic.StopReason) string {
	switch r {
	case anthropic.StopReasonToolUse:
		return StopReasonToolUse
	case anthropic.StopReasonMaxTokens:
		return StopReasonMaxOut
	default:
		return StopReasonEndTurn
	}
}

// --- OpenAI single-turn ---

// NextTurn runs one OpenAI turn with a custom system prompt + explicit tools,
// executing nothing. It streams one completion of its own (it does NOT reuse the
// chat loop) so the chat path stays untouched while this seam also reads usage.
func (s *openAIService) NextTurn(ctx context.Context, p TurnParams) (TurnResult, error) {
	messages := []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(p.System)}
	messages = append(messages, toOpenAIMessages(p.History.toPrivate())...)

	params := openai.ChatCompletionNewParams{
		Model:               s.model,
		Messages:            messages,
		MaxCompletionTokens: openai.Int(int64(turnMaxTokens(p))),
	}
	if !p.ForceNoTools && len(p.Tools) > 0 {
		params.Tools = toOpenAITools(p.Tools)
	} else {
		params.ToolChoice.OfAuto = openai.String(string(openai.ChatCompletionToolChoiceOptionAutoNone))
	}

	stream := s.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()
	acc := openai.ChatCompletionAccumulator{}
	for stream.Next() {
		if !acc.AddChunk(stream.Current()) {
			return TurnResult{}, fmt.Errorf("accumulate openai stream chunk")
		}
	}
	if err := stream.Err(); err != nil {
		return TurnResult{}, fmt.Errorf("openai chat stream: %w", err)
	}
	if len(acc.Choices) == 0 {
		return TurnResult{}, fmt.Errorf("openai chat stream: response had no choices")
	}

	choice := acc.Choices[0]
	msg := openAIMessageFromSDK(choice.Message)
	return TurnResult{
		Message:    exportMessage(openAIMessageToTranscript(msg)),
		Usage:      openAIUsage(acc.Usage),
		StopReason: normalizeOpenAIStop(string(choice.FinishReason)),
	}, nil
}

func openAIUsage(u openai.CompletionUsage) Usage {
	// OpenAI reports prompt_tokens inclusive of cached tokens; carry the cached
	// portion separately so the cost map can discount it, mirroring how the
	// Anthropic cache fields are billed.
	return Usage{
		InputTokens:     u.PromptTokens,
		OutputTokens:    u.CompletionTokens,
		CacheReadTokens: u.PromptTokensDetails.CachedTokens,
	}
}

func normalizeOpenAIStop(r string) string {
	switch r {
	case "tool_calls":
		return StopReasonToolUse
	case "length":
		return StopReasonMaxOut
	default:
		return StopReasonEndTurn
	}
}

// --- Gemini single-turn ---

// NextTurn runs one Gemini turn with a custom system instruction + explicit tools,
// executing nothing. It aggregates one streamed response of its own (separate
// from the chat loop) and reads usage metadata off the final chunk.
func (s *geminiService) NextTurn(ctx context.Context, p TurnParams) (TurnResult, error) {
	if s.clientErr != nil {
		return TurnResult{}, fmt.Errorf("gemini client: %w", s.clientErr)
	}
	contents := toGeminiContents(p.History.toPrivate())
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{genai.NewPartFromText(p.System)}},
		MaxOutputTokens:   int32(turnMaxTokens(p)),
	}
	if !p.ForceNoTools && len(p.Tools) > 0 {
		config.Tools = toGeminiTools(p.Tools)
	}

	content := genai.NewContentFromParts(nil, genai.RoleModel)
	var finishReason genai.FinishReason
	var usage Usage
	for chunk, err := range s.client.Models.GenerateContentStream(ctx, s.model, contents, config) {
		if err != nil {
			return TurnResult{}, fmt.Errorf("gemini stream generate: %w", err)
		}
		if chunk == nil {
			continue
		}
		if chunk.UsageMetadata != nil {
			usage = geminiUsage(chunk.UsageMetadata)
		}
		if len(chunk.Candidates) == 0 || chunk.Candidates[0] == nil {
			continue
		}
		candidate := chunk.Candidates[0]
		if candidate.FinishReason != "" {
			finishReason = candidate.FinishReason
		}
		if candidate.Content == nil {
			continue
		}
		if candidate.Content.Role != "" {
			content.Role = candidate.Content.Role
		}
		for _, part := range candidate.Content.Parts {
			if part != nil {
				content.Parts = append(content.Parts, part)
			}
		}
	}

	stop := StopReasonEndTurn
	if finishReason == genai.FinishReasonMaxTokens {
		stop = StopReasonMaxOut
	}
	if len(geminiFunctionCalls(content)) > 0 {
		stop = StopReasonToolUse
	}
	return TurnResult{
		Message:    exportMessage(geminiContentToTranscript(content)),
		Usage:      usage,
		StopReason: stop,
	}, nil
}

func geminiUsage(u *genai.GenerateContentResponseUsageMetadata) Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{
		InputTokens:     int64(u.PromptTokenCount),
		OutputTokens:    int64(u.CandidatesTokenCount),
		CacheReadTokens: int64(u.CachedContentTokenCount),
	}
}
