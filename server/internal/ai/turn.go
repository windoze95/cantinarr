package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	openai "github.com/openai/openai-go/v3"
	"google.golang.org/genai"

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

	// Provider-state blocks are opaque continuity data returned by reasoning
	// models. They must be replayed byte-for-byte with a later tool result, but
	// are never shown to users or treated as model text by the remediation
	// runner.
	BlockAnthropicThinking         = blockTypeAnthropicThinking
	BlockAnthropicRedactedThinking = blockTypeAnthropicRedactedThinking
	BlockGeminiThought             = blockTypeGeminiThought
)

// TranscriptBlock is one provider-neutral content block in the exported
// single-turn transcript. It mirrors the package-private transcriptBlock so
// remediation (a different package) can build history and read tool calls back
// without depending on the chat path's unexported types.
type TranscriptBlock struct {
	Type             string          `json:"type"`
	Text             string          `json:"text,omitempty"`
	ID               string          `json:"id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Input            json.RawMessage `json:"input,omitempty"`
	ToolUseID        string          `json:"tool_use_id,omitempty"`
	Content          string          `json:"content,omitempty"`
	IsError          bool            `json:"is_error,omitempty"`
	Signature        string          `json:"signature,omitempty"`
	Data             string          `json:"data,omitempty"`
	ThoughtSignature []byte          `json:"thought_signature,omitempty"`
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
	// ForceNoTools uses each provider's compatible tool-free request shape so the
	// model must answer in text (used to coerce a final diagnosis when bounds are
	// nearly spent).
	ForceNoTools bool
	// DisableReasoning requests the provider's lowest supported reasoning mode.
	// Validation probes use this so hidden reasoning cannot consume their small
	// output budget before any visible text arrives. Providers/models that cannot
	// fully disable reasoning receive a larger validation-only output allowance.
	DisableReasoning bool
	// MaxTokens is the per-turn output cap. Codex app-server has no request-time
	// hard-cap field, so that adapter monitors its usage notifications and
	// interrupts once the reported output reaches this ceiling. 0 falls back to
	// a small default.
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
// each provider service so remediation can follow the admin-owned shared
// provider/model without changing its guarded tool-dispatch loop.
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
			Type:             b.Type,
			Text:             b.Text,
			ID:               b.ID,
			Name:             b.Name,
			Input:            append(json.RawMessage(nil), b.Input...),
			ToolUseID:        b.ToolUseID,
			Content:          b.Content,
			IsError:          b.IsError,
			Signature:        b.Signature,
			Data:             b.Data,
			ThoughtSignature: append([]byte(nil), b.ThoughtSignature...),
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
			Type:             b.Type,
			Text:             b.Text,
			ID:               b.ID,
			Name:             b.Name,
			Input:            append(json.RawMessage(nil), b.Input...),
			ToolUseID:        b.ToolUseID,
			Content:          b.Content,
			IsError:          b.IsError,
			Signature:        b.Signature,
			Data:             b.Data,
			ThoughtSignature: append([]byte(nil), b.ThoughtSignature...),
		})
	}
	return em
}

// --- Anthropic single-turn ---

const anthropicValidationReasoningMaxTokens = 16000

// NextTurn runs one Anthropic turn with p.System as the system prompt and p.Tools
// as the explicit tool list, streaming text but executing nothing.
func (s *Service) NextTurn(ctx context.Context, p TurnParams) (TurnResult, error) {
	maxTurnTokens := turnMaxTokens(p)
	if p.DisableReasoning && anthropicAlwaysUsesAdaptiveThinking(s.model) && maxTurnTokens < anthropicValidationReasoningMaxTokens {
		// Fable's adaptive thinking cannot be disabled. Give the readiness probe a
		// bounded allowance large enough that hidden reasoning cannot crowd out its
		// one-word visible response.
		maxTurnTokens = anthropicValidationReasoningMaxTokens
	}
	params := anthropic.MessageNewParams{
		Model:     s.model,
		MaxTokens: int64(maxTurnTokens),
		System: []anthropic.TextBlockParam{
			{Text: p.System, CacheControl: anthropic.NewCacheControlEphemeralParam()},
		},
		Messages: anthropicTurnMessages(p.History),
	}
	if !p.ForceNoTools && len(p.Tools) > 0 {
		params.Tools = toSDKTools(p.Tools)
	} else {
		params.ToolChoice = anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
	}
	if p.DisableReasoning && supportsAnthropicAdaptiveThinking(s.model) && !anthropicAlwaysUsesAdaptiveThinking(s.model) {
		disabled := anthropic.NewThinkingConfigDisabledParam()
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &disabled}
	} else if !p.DisableReasoning && supportsAnthropicAdaptiveThinking(s.model) {
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}}
	}

	// Stream one turn with no callbacks (remediation streams via WS per persisted
	// step, not token-by-token). Reuse streamOne so accumulation/cache breakpoints
	// match the chat path exactly.
	message, err := s.streamOne(ctx, params, StreamCallbacks{})
	if err != nil {
		return TurnResult{}, err
	}
	if err := validateAnthropicMessage(message); err != nil {
		return TurnResult{}, err
	}
	return TurnResult{
		Message:    anthropicMessageToTurnTranscript(*message),
		Usage:      anthropicUsage(message.Usage),
		StopReason: normalizeAnthropicStop(message.StopReason),
	}, nil
}

func anthropicAlwaysUsesAdaptiveThinking(model anthropic.Model) bool {
	return strings.Contains(string(model), "fable-5")
}

func anthropicTurnMessages(history Transcript) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(history))
	for _, message := range history {
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(message.Content))
		for _, block := range message.Content {
			switch block.Type {
			case BlockText:
				if block.Text != "" {
					blocks = append(blocks, anthropic.NewTextBlock(block.Text))
				}
			case BlockAnthropicThinking:
				blocks = append(blocks, anthropic.NewThinkingBlock(block.Signature, block.Text))
			case BlockAnthropicRedactedThinking:
				blocks = append(blocks, anthropic.NewRedactedThinkingBlock(block.Data))
			case BlockToolUse:
				blocks = append(blocks, anthropic.NewToolUseBlock(block.ID, rawJSONValue(block.Input), block.Name))
			case BlockToolResult:
				blocks = append(blocks, anthropic.NewToolResultBlock(block.ToolUseID, block.Content, block.IsError))
			}
		}
		if len(blocks) == 0 {
			continue
		}
		if message.Role == RoleAssistant {
			if len(out) == 0 {
				continue
			}
			out = append(out, anthropic.NewAssistantMessage(blocks...))
		} else {
			out = append(out, anthropic.NewUserMessage(blocks...))
		}
	}
	return out
}

func anthropicMessageToTurnTranscript(message anthropic.Message) TranscriptMessage {
	out := TranscriptMessage{Role: string(message.Role)}
	if out.Role == "" {
		out.Role = RoleAssistant
	}
	for _, block := range message.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			if v.Text != "" {
				out.Content = append(out.Content, TranscriptBlock{Type: BlockText, Text: v.Text})
			}
		case anthropic.ThinkingBlock:
			out.Content = append(out.Content, TranscriptBlock{
				Type: BlockAnthropicThinking, Text: v.Thinking, Signature: v.Signature,
			})
		case anthropic.RedactedThinkingBlock:
			out.Content = append(out.Content, TranscriptBlock{
				Type: BlockAnthropicRedactedThinking, Data: v.Data,
			})
		case anthropic.ToolUseBlock:
			input := append(json.RawMessage(nil), v.Input...)
			if len(input) == 0 || string(input) == "null" {
				input = json.RawMessage("{}")
			}
			out.Content = append(out.Content, TranscriptBlock{
				Type: BlockToolUse, ID: v.ID, Name: v.Name, Input: input,
			})
		}
	}
	return out
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

const openAIValidationReasoningMaxTokens = 16000

var errOpenAIValidationReasoningBudget = errors.New("openai validation response exhausted its output budget before returning text")

type openAIReasoningCapability uint8

const (
	openAIReasoningUnknown openAIReasoningCapability = iota
	openAIReasoningUnsupported
	openAIReasoningNone
	openAIReasoningMinimal
	openAIReasoningLow
)

type openAIReasoningAttempt struct {
	effort    openai.ReasoningEffort
	maxTokens int64
}

// NextTurn runs one OpenAI turn with a custom system prompt + explicit tools,
// executing nothing. It streams one completion of its own (it does NOT reuse the
// chat loop) so the chat path stays untouched while this seam also reads usage.
func (s *openAIService) NextTurn(ctx context.Context, p TurnParams) (TurnResult, error) {
	attempts := openAIReasoningAttempts(s.model, p)
	for i := 0; i < len(attempts); {
		result, err := s.openAINextTurnAttempt(ctx, p, attempts[i])
		if err == nil {
			return result, nil
		}
		next := nextOpenAIReasoningAttempt(attempts, i, p, err)
		if next <= i || next >= len(attempts) {
			return TurnResult{}, err
		}
		i = next
	}
	return TurnResult{}, fmt.Errorf("openai validation: no compatible reasoning strategy")
}

func (s *openAIService) openAINextTurnAttempt(ctx context.Context, p TurnParams, attempt openAIReasoningAttempt) (TurnResult, error) {
	params := openAINextTurnParamsForAttempt(s.model, p, attempt)

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
	if strings.TrimSpace(msg.Refusal) != "" {
		return TurnResult{}, fmt.Errorf("openai chat stream: model refused the response")
	}
	if strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
		if p.DisableReasoning && choice.FinishReason == "length" {
			return TurnResult{}, errOpenAIValidationReasoningBudget
		}
		return TurnResult{}, fmt.Errorf("openai chat stream: response contained no text or tool calls")
	}
	if choice.FinishReason == "tool_calls" && len(msg.ToolCalls) == 0 {
		return TurnResult{}, fmt.Errorf("openai chat stream: model requested tool use but sent no complete tool calls")
	}
	return TurnResult{
		Message:    exportMessage(openAIMessageToTranscript(msg)),
		Usage:      openAIUsage(acc.Usage),
		StopReason: normalizeOpenAIStop(string(choice.FinishReason)),
	}, nil
}

// openAINextTurnParams builds the exact request used by the remediation
// single-turn stream. Streaming chat completions omit their final usage-only
// chunk unless include_usage is explicitly requested; request it so run audits
// retain useful token diagnostics.
func openAINextTurnParams(model openai.ChatModel, p TurnParams) openai.ChatCompletionNewParams {
	attempts := openAIReasoningAttempts(model, p)
	return openAINextTurnParamsForAttempt(model, p, attempts[0])
}

func openAINextTurnParamsForAttempt(model openai.ChatModel, p TurnParams, attempt openAIReasoningAttempt) openai.ChatCompletionNewParams {
	messages := []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(p.System)}
	messages = append(messages, toOpenAIMessages(p.History.toPrivate())...)

	params := openai.ChatCompletionNewParams{
		Model:               model,
		Messages:            messages,
		MaxCompletionTokens: openai.Int(attempt.maxTokens),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	if !p.ForceNoTools && len(p.Tools) > 0 {
		params.Tools = toOpenAITools(p.Tools)
	}
	if attempt.effort != "" {
		params.ReasoningEffort = attempt.effort
	}

	return params
}

// openAIReasoningAttempts is deliberately capability based rather than a
// single prefix check. OpenAI accepts custom/fine-tuned model IDs, and proxies
// often expose reasoning models under deployment aliases. Known models start
// with their lowest supported effort. Unknown IDs probe `none`, fall back to
// `low` only when the API rejects that value, then omit the field for a
// non-reasoning model. Reasoning-enabled attempts use the same output allowance
// as interactive chat so a tiny readiness probe cannot be consumed entirely by
// hidden reasoning tokens.
func openAIReasoningAttempts(model openai.ChatModel, p TurnParams) []openAIReasoningAttempt {
	requested := int64(turnMaxTokens(p))
	if !p.DisableReasoning {
		return []openAIReasoningAttempt{{maxTokens: requested}}
	}
	reasoningBudget := requested
	if reasoningBudget < openAIValidationReasoningMaxTokens {
		reasoningBudget = openAIValidationReasoningMaxTokens
	}

	var attempts []openAIReasoningAttempt
	appendAttempt := func(effort openai.ReasoningEffort, maxTokens int64) {
		candidate := openAIReasoningAttempt{effort: effort, maxTokens: maxTokens}
		if len(attempts) == 0 || attempts[len(attempts)-1] != candidate {
			attempts = append(attempts, candidate)
		}
	}

	switch openAIModelReasoningCapability(model) {
	case openAIReasoningUnsupported:
		appendAttempt("", requested)
		return attempts
	case openAIReasoningNone:
		appendAttempt(openai.ReasoningEffortNone, requested)
		appendAttempt(openai.ReasoningEffortLow, reasoningBudget)
	case openAIReasoningMinimal:
		appendAttempt(openai.ReasoningEffortMinimal, reasoningBudget)
		appendAttempt(openai.ReasoningEffortLow, reasoningBudget)
	case openAIReasoningLow:
		appendAttempt(openai.ReasoningEffortLow, reasoningBudget)
	default:
		appendAttempt(openai.ReasoningEffortNone, requested)
		appendAttempt(openai.ReasoningEffortLow, reasoningBudget)
	}
	// A field-level rejection means this is a non-reasoning model (or a proxy
	// that does not expose reasoning controls). Try a generous field-free probe,
	// then the caller's original cap if the model rejects that maximum.
	appendAttempt("", reasoningBudget)
	appendAttempt("", requested)
	return attempts
}

func openAIModelReasoningCapability(model openai.ChatModel) openAIReasoningCapability {
	name := openAIBaseModelName(model)
	switch {
	case strings.HasPrefix(name, "gpt-3.5"), strings.HasPrefix(name, "gpt-4"):
		return openAIReasoningUnsupported
	case strings.HasPrefix(name, "gpt-5"):
		if strings.Contains(name, "-pro") {
			return openAIReasoningLow
		}
		minor, ok := openAIGPT5Minor(name)
		if ok && minor >= 1 {
			return openAIReasoningNone
		}
		return openAIReasoningMinimal
	case len(name) >= 2 && name[0] == 'o' && name[1] >= '0' && name[1] <= '9':
		return openAIReasoningLow
	default:
		return openAIReasoningUnknown
	}
}

func openAIBaseModelName(model openai.ChatModel) string {
	name := strings.ToLower(strings.TrimSpace(string(model)))
	name = strings.TrimPrefix(name, "openai/")
	if strings.HasPrefix(name, "ft:") {
		name = strings.TrimPrefix(name, "ft:")
		if end := strings.IndexByte(name, ':'); end >= 0 {
			name = name[:end]
		}
	}
	return name
}

func openAIGPT5Minor(name string) (int, bool) {
	rest := strings.TrimPrefix(name, "gpt-5.")
	if rest == name {
		return 0, false
	}
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	minor, err := strconv.Atoi(rest[:end])
	return minor, err == nil
}

type openAIReasoningRejection uint8

const (
	openAIReasoningNotRejected openAIReasoningRejection = iota
	openAIReasoningValueRejected
	openAIReasoningFieldRejected
)

func nextOpenAIReasoningAttempt(attempts []openAIReasoningAttempt, current int, p TurnParams, err error) int {
	attempt := attempts[current]
	if errors.Is(err, errOpenAIValidationReasoningBudget) {
		for i := current + 1; i < len(attempts); i++ {
			if attempts[i].maxTokens > attempt.maxTokens {
				return i
			}
		}
		return -1
	}
	if attempt.effort != "" {
		switch classifyOpenAIReasoningRejection(err) {
		case openAIReasoningValueRejected:
			return current + 1
		case openAIReasoningFieldRejected:
			for i := current + 1; i < len(attempts); i++ {
				if attempts[i].effort == "" {
					return i
				}
			}
		}
		return -1
	}
	if attempt.maxTokens > int64(turnMaxTokens(p)) && openAIOutputLimitRejected(err) {
		for i := current + 1; i < len(attempts); i++ {
			if attempts[i].effort == "" && attempts[i].maxTokens == int64(turnMaxTokens(p)) {
				return i
			}
		}
	}
	return -1
}

func classifyOpenAIReasoningRejection(err error) openAIReasoningRejection {
	apiErr, ok := openAIInvalidRequest(err)
	if !ok {
		return openAIReasoningNotRejected
	}
	marker := strings.ToLower(strings.Join([]string{apiErr.Param, apiErr.Code, apiErr.Message}, " "))
	if !strings.Contains(marker, "reasoning_effort") && !strings.Contains(marker, "reasoning effort") {
		return openAIReasoningNotRejected
	}
	if strings.Contains(marker, "unsupported_value") || strings.Contains(marker, "invalid_value") ||
		strings.Contains(marker, "unsupported value") || strings.Contains(marker, "supported values") {
		return openAIReasoningValueRejected
	}
	return openAIReasoningFieldRejected
}

func openAIOutputLimitRejected(err error) bool {
	apiErr, ok := openAIInvalidRequest(err)
	if !ok {
		return false
	}
	marker := strings.ToLower(strings.Join([]string{apiErr.Param, apiErr.Code, apiErr.Message}, " "))
	return strings.Contains(marker, "max_completion_tokens") || strings.Contains(marker, "maximum completion tokens")
}

func openAIInvalidRequest(err error) (*openai.Error, bool) {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) || (apiErr.StatusCode != http.StatusBadRequest && apiErr.StatusCode != http.StatusUnprocessableEntity) {
		return nil, false
	}
	return apiErr, true
}

func openAIUsage(u openai.CompletionUsage) Usage {
	// OpenAI reports prompt_tokens inclusive of cached tokens; carry the cached
	// portion separately to preserve the provider's useful cache diagnostics.
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

const geminiSemanticMaxAttempts = 2

var errGeminiIncompleteStream = errors.New("gemini: stream ended without a terminal finish reason")

// NextTurn runs one Gemini turn with a custom system instruction + explicit tools,
// executing nothing. It aggregates one streamed response of its own (separate
// from the chat loop) and reads usage metadata off the final chunk.
func (s *geminiService) NextTurn(ctx context.Context, p TurnParams) (TurnResult, error) {
	if s.clientErr != nil {
		return TurnResult{}, fmt.Errorf("gemini client: %w", s.clientErr)
	}
	for attempt := 0; attempt < geminiSemanticMaxAttempts; attempt++ {
		result, err := s.geminiNextTurnOnce(ctx, p)
		if !errors.Is(err, errGeminiIncompleteStream) {
			return result, err
		}
		if attempt == geminiSemanticMaxAttempts-1 {
			// The v1 Google SDK can silently convert a canceled response-body
			// scanner into a clean EOF after yielding usable content. After one
			// fresh retry, accept that complete parsed content as an implicit stop.
			return result, nil
		}
		if err := waitForGeminiSemanticRetry(ctx, attempt); err != nil {
			return TurnResult{}, fmt.Errorf("gemini semantic retry: %w", err)
		}
	}
	return TurnResult{}, errGeminiIncompleteStream
}

// geminiNextTurnOnce owns a fresh aggregation buffer. Unlike interactive chat,
// NextTurn has emitted no callbacks and executed no tools, so retrying one
// semantically incomplete stream cannot duplicate user-visible output or side
// effects. forEachGeminiStream independently handles retryable transport errors
// only before output; this layer handles the narrower case where usable output
// arrived but Google's terminal finish chunk did not.
func (s *geminiService) geminiNextTurnOnce(ctx context.Context, p TurnParams) (TurnResult, error) {
	contents := geminiTurnContents(p.History)
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
	var promptFeedback *genai.GenerateContentResponsePromptFeedback
	sawCandidate := false
	err := s.forEachGeminiStream(ctx, contents, config, func(chunk *genai.GenerateContentResponse) {
		if chunk == nil {
			return
		}
		if chunk.PromptFeedback != nil {
			promptFeedback = chunk.PromptFeedback
		}
		if chunk.UsageMetadata != nil {
			usage = geminiUsage(chunk.UsageMetadata)
		}
		if len(chunk.Candidates) == 0 || chunk.Candidates[0] == nil {
			return
		}
		sawCandidate = true
		candidate := chunk.Candidates[0]
		if candidate.FinishReason != "" {
			finishReason = candidate.FinishReason
		}
		if candidate.Content == nil {
			return
		}
		if candidate.Content.Role != "" {
			content.Role = candidate.Content.Role
		}
		for _, part := range candidate.Content.Parts {
			if part != nil {
				content.Parts = append(content.Parts, part)
			}
		}
	})
	if err != nil {
		return TurnResult{}, fmt.Errorf("gemini stream generate: %w", err)
	}
	if reason := geminiPromptBlockReason(promptFeedback); reason != "" {
		return TurnResult{}, fmt.Errorf("gemini: prompt blocked (%s)", reason)
	}
	if !sawCandidate {
		return TurnResult{}, fmt.Errorf("gemini: response had no candidates")
	}
	if !geminiContentHasUsableOutput(content) {
		return TurnResult{}, fmt.Errorf("gemini: response contained no text or tool calls")
	}

	stop := StopReasonEndTurn
	if finishReason == genai.FinishReasonMaxTokens {
		stop = StopReasonMaxOut
	}
	if len(geminiFunctionCalls(content)) > 0 {
		stop = StopReasonToolUse
	}
	result := TurnResult{
		Message:    geminiContentToTurnTranscript(content),
		Usage:      usage,
		StopReason: stop,
	}
	if finishReason == "" || finishReason == genai.FinishReasonUnspecified {
		return result, errGeminiIncompleteStream
	}
	if err := validateGeminiFinishReason(finishReason); err != nil {
		return TurnResult{}, err
	}
	return result, nil
}

func waitForGeminiSemanticRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(geminiRetryBaseDelay << attempt)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func geminiUsage(u *genai.GenerateContentResponseUsageMetadata) Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{
		InputTokens:     int64(u.PromptTokenCount),
		OutputTokens:    int64(u.CandidatesTokenCount) + int64(u.ThoughtsTokenCount),
		CacheReadTokens: int64(u.CachedContentTokenCount),
	}
}

func geminiTurnContents(history Transcript) []*genai.Content {
	out := make([]*genai.Content, 0, len(history))
	for _, message := range history {
		role := genai.Role(genai.RoleUser)
		if message.Role == RoleAssistant {
			if len(out) == 0 {
				continue
			}
			role = genai.RoleModel
		}
		content := genai.NewContentFromParts(nil, role)
		for _, block := range message.Content {
			switch block.Type {
			case BlockText:
				if block.Text != "" {
					content.Parts = append(content.Parts, &genai.Part{
						Text: block.Text, ThoughtSignature: append([]byte(nil), block.ThoughtSignature...),
					})
				}
			case BlockGeminiThought:
				content.Parts = append(content.Parts, &genai.Part{
					Text: block.Text, Thought: true, ThoughtSignature: append([]byte(nil), block.ThoughtSignature...),
				})
			case BlockToolUse:
				content.Parts = append(content.Parts, &genai.Part{
					FunctionCall:     &genai.FunctionCall{Name: block.Name, Args: rawJSONMap(block.Input), ID: block.ID},
					ThoughtSignature: append([]byte(nil), block.ThoughtSignature...),
				})
			case BlockToolResult:
				name := block.Name
				if name == "" {
					name = toolNameForTurnResult(history, block.ToolUseID)
				}
				content.Parts = append(content.Parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{
					Name: name, ID: block.ToolUseID, Response: geminiTurnToolResponse(block),
				}})
			}
		}
		if len(content.Parts) > 0 {
			out = append(out, content)
		}
	}
	return out
}

func geminiContentToTurnTranscript(content *genai.Content) TranscriptMessage {
	out := TranscriptMessage{Role: RoleAssistant}
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
			out.Content = append(out.Content, TranscriptBlock{
				Type: BlockToolUse, ID: part.FunctionCall.ID, Name: part.FunctionCall.Name,
				Input: input, ThoughtSignature: signature,
			})
		case part.Thought:
			out.Content = append(out.Content, TranscriptBlock{
				Type: BlockGeminiThought, Text: part.Text, ThoughtSignature: signature,
			})
		case part.Text != "" || len(signature) > 0:
			out.Content = append(out.Content, TranscriptBlock{
				Type: BlockText, Text: part.Text, ThoughtSignature: signature,
			})
		}
	}
	return out
}

func geminiTurnToolResponse(block TranscriptBlock) map[string]any {
	if block.IsError {
		return map[string]any{"error": strings.TrimPrefix(block.Content, "Error: ")}
	}
	return map[string]any{"response": block.Content}
}

func toolNameForTurnResult(history Transcript, toolUseID string) string {
	for i := len(history) - 1; i >= 0; i-- {
		for _, block := range history[i].Content {
			if block.Type == BlockToolUse && block.ID == toolUseID {
				return block.Name
			}
		}
	}
	return ""
}
