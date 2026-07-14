package ai

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	conversationTTL   = 4 * time.Hour
	maxConversations  = 200
	maxStoredMessages = 60
	// The client request body is capped at 1 MiB. Keep server-owned tool
	// context under the same bound so repeated large tool results cannot turn a
	// conversation into a multi-megabyte process-memory or provider-frame DoS.
	maxStoredTranscriptBytes = 1 << 20
	maxStoredTextBytes       = 64 << 10
	maxStoredToolResultBytes = 32 << 10
	maxStoredToolInputBytes  = 16 << 10
	maxStoredIdentifierBytes = 512
	// Signed provider continuation blocks cannot be truncated or redacted
	// without invalidating their signatures. Drop the containing provider turn
	// when its opaque state exceeds the ordinary per-block text budget.
	maxStoredOpaqueBlockBytes = maxStoredTextBytes

	agentRoleUser      = "user"
	agentRoleAssistant = "assistant"

	blockTypeText       = "text"
	blockTypeToolUse    = "tool_use"
	blockTypeToolResult = "tool_result"

	blockTypeAnthropicThinking         = "anthropic_thinking"
	blockTypeAnthropicRedactedThinking = "anthropic_redacted_thinking"
	blockTypeGeminiThought             = "gemini_thought"
)

// transcript is the provider-neutral representation used by the agent loop.
// It keeps tool calls/results server-side so follow-up turns remain grounded;
// conversationBinding prevents that history from crossing provider accounts.
type transcript []transcriptMessage

type transcriptMessage struct {
	Role    string
	Content []transcriptBlock
}

type transcriptBlock struct {
	Type             string
	Text             string
	ID               string
	Name             string
	Input            json.RawMessage
	ToolUseID        string
	Content          string
	IsError          bool
	Signature        string
	Data             string
	ThoughtSignature []byte
}

// conversationStore keeps byte-bounded provider-neutral transcripts server-side.
type conversationStore struct {
	mu               sync.Mutex
	conversations    map[string]*conversation
	globalGeneration uint64
	userGenerations  map[int64]uint64
}

type conversation struct {
	userID    int64
	binding   conversationBinding
	messages  transcript
	updatedAt time.Time
}

// conversationBinding identifies the resolved provider account that owns a
// transcript. A conversation must never move between personal and shared
// billing, providers, users' personal accounts, selected models, rotated API
// credentials, or replaced OAuth connections.
//
// credentialFingerprint is one-way state kept only in process memory; the raw
// API key is never copied into the conversation store. The generations are
// sampled under the store mutex and checked again by Put, so an OAuth
// unlink/relink cannot be defeated by an older in-flight request completing
// after its transcripts were purged.
type conversationBinding struct {
	ownerUserID           int64
	source                string
	provider              string
	model                 string
	personalAccountUserID int64
	hasCredential         bool
	credentialFingerprint [sha256.Size]byte
	globalGeneration      uint64
	userGeneration        uint64
}

func baseConversationBinding(userID int64, resolved resolvedAI) conversationBinding {
	binding := conversationBinding{
		ownerUserID: userID,
		source:      resolved.Source,
		provider:    resolved.Provider,
		model:       resolved.Model,
	}
	if resolved.Source == aiSourcePersonal {
		binding.personalAccountUserID = userID
	}
	if resolved.APIKey != "" {
		binding.hasCredential = true
		binding.credentialFingerprint = sha256.Sum256([]byte(resolved.APIKey))
	}
	return binding
}

func newConversationStore() *conversationStore {
	return &conversationStore{
		conversations:   make(map[string]*conversation),
		userGenerations: make(map[int64]uint64),
	}
}

// newBinding atomically snapshots the invalidation generations for the
// resolved provider account. Callers must retain this value for the whole
// request and pass the same value to Get and Put.
func (s *conversationStore) newBinding(userID int64, resolved resolvedAI) conversationBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding := baseConversationBinding(userID, resolved)
	binding.globalGeneration = s.globalGeneration
	binding.userGeneration = s.userGenerations[userID]
	return binding
}

// Get returns the stored history for id if it exists and belongs to both the
// requesting user and the currently resolved provider account.
// The returned slice is a copy: callers append to it while the loop runs, and
// sharing the backing array with the stored entry would race with concurrent
// turns on the same conversation.
func (s *conversationStore) Get(id string, userID int64, binding conversationBinding) (transcript, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !s.bindingCurrentLocked(userID, binding) || !ok || conv.userID != userID || conv.binding != binding || time.Since(conv.updatedAt) > conversationTTL {
		return nil, false
	}
	return cloneTranscript(conv.messages), true
}

// Put stores a safe, byte-bounded transcript for id, trimming old turns and
// evicting stale conversations. The transcript is copied so the store never
// aliases a caller's slice. It returns false when an account invalidation
// happened after the request took its binding snapshot.
func (s *conversationStore) Put(id string, userID int64, binding conversationBinding, messages transcript) bool {
	trimmed := trimHistory(sanitizeTranscript(messages), maxStoredMessages)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bindingCurrentLocked(userID, binding) {
		return false
	}

	s.evictLocked()
	s.conversations[id] = &conversation{
		userID:    userID,
		binding:   binding,
		messages:  cloneTranscript(trimmed),
		updatedAt: time.Now(),
	}
	return true
}

func (s *conversationStore) bindingCurrentLocked(userID int64, binding conversationBinding) bool {
	if binding.ownerUserID != userID ||
		(binding.source == aiSourcePersonal && binding.personalAccountUserID != userID) {
		return false
	}
	return binding.globalGeneration == s.globalGeneration &&
		binding.userGeneration == s.userGenerations[userID]
}

// Delete removes a conversation, used to invalidate state after a failed turn
// so retries fall back to the client transcript instead of replaying a
// possibly poisoned history.
func (s *conversationStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conversations, id)
}

// DeleteForUser invalidates every transcript owned by one Cantinarr user.
// OAuth unlink/relink uses this because a replacement authorization can point
// at a different provider account even though the personal AccountRef is the
// same user-scoped identifier.
func (s *conversationStore) DeleteForUser(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userGenerations[userID]++
	for id, conv := range s.conversations {
		if conv.userID == userID {
			delete(s.conversations, id)
		}
	}
}

// DeleteAll invalidates all in-memory transcripts. A shared OAuth account
// replacement changes the external account for every granted user, so no
// prior shared transcript may survive that boundary.
func (s *conversationStore) DeleteAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.globalGeneration++
	s.conversations = make(map[string]*conversation)
}

func (s *conversationStore) evictLocked() {
	now := time.Now()
	for id, conv := range s.conversations {
		if now.Sub(conv.updatedAt) > conversationTTL {
			delete(s.conversations, id)
		}
	}
	// Hard cap: drop the oldest conversations if still over the limit.
	for len(s.conversations) >= maxConversations {
		oldestID := ""
		var oldest time.Time
		for id, conv := range s.conversations {
			if oldestID == "" || conv.updatedAt.Before(oldest) {
				oldestID, oldest = id, conv.updatedAt
			}
		}
		delete(s.conversations, oldestID)
	}
}

// trimHistory bounds the transcript by message count and bytes while keeping it
// valid for the API: the first retained message must be a plain user message
// (not tool results), so assistant tool_use blocks are never orphaned from
// their results.
func trimHistory(messages transcript, maxLen int) transcript {
	if len(messages) <= maxLen && transcriptSize(messages) <= maxStoredTranscriptBytes {
		return messages
	}
	start := len(messages) - maxLen
	if start < 0 {
		start = 0
	}
	for i := start; i < len(messages); i++ {
		if messages[i].Role != agentRoleUser {
			continue
		}
		if containsToolResult(messages[i]) {
			continue
		}
		if transcriptSize(messages[i:]) <= maxStoredTranscriptBytes {
			return messages[i:]
		}
	}
	// A provider may return an unusually large or fragmented turn with no safe
	// suffix under the budget. Retain only the newest plain user message; this
	// loses some context but remains valid and bounded instead of keeping an
	// attacker-controlled oversized transcript.
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == agentRoleUser && !containsToolResult(messages[i]) {
			return messages[i : i+1]
		}
	}
	return nil
}

func transcriptSize(messages transcript) int {
	total := 0
	for _, message := range messages {
		total += len(message.Role)
		for _, block := range message.Content {
			total += len(block.Type) + len(block.Text) + len(block.ID) + len(block.Name)
			total += len(block.Input) + len(block.ToolUseID) + len(block.Content)
			total += len(block.Signature) + len(block.Data) + len(block.ThoughtSignature) + 32
		}
	}
	return total
}

func containsToolResult(m transcriptMessage) bool {
	for _, block := range m.Content {
		if block.Type == blockTypeToolResult {
			return true
		}
	}
	return false
}

// sanitizeTranscript makes a transcript safe to persist and replay: assistant
// tool_use blocks whose results never arrived (max_tokens truncation mid-tool,
// stream errors) are stripped, and messages left with no content are dropped.
// Without this, one bad turn would 400 every subsequent request on the
// conversation until its TTL expired.
func sanitizeTranscript(messages transcript) transcript {
	bounded := make(transcript, len(messages))
	valid := make([]bool, len(messages))
	for i, message := range messages {
		bounded[i], valid[i] = boundTranscriptMessage(message)
	}
	out := make(transcript, 0, len(bounded))
	for i, m := range bounded {
		if !valid[i] {
			continue
		}
		if m.Role == agentRoleAssistant {
			m = stripOrphanToolUse(m, followingToolResults(bounded, valid, i))
		} else if containsToolResult(m) {
			m = stripOrphanToolResults(m, precedingToolUses(bounded, valid, i))
		}
		if len(m.Content) == 0 {
			continue
		}
		out = append(out, m)
	}
	return out
}

func boundTranscriptMessage(message transcriptMessage) (transcriptMessage, bool) {
	message.Role = boundedString(message.Role, maxStoredIdentifierBytes)
	blocks := make([]transcriptBlock, len(message.Content))
	for i, block := range message.Content {
		if isOpaqueContinuationBlock(block) {
			// Provider-signed state is an atomic capability: even innocuous-looking
			// normalization or truncation makes it unusable. Clone it exactly, or
			// reject the containing turn when it exceeds a safe bound.
			if opaqueContinuationBlockSize(block) > maxStoredOpaqueBlockBytes ||
				len(block.Type) > maxStoredIdentifierBytes ||
				len(block.ID) > maxStoredIdentifierBytes ||
				len(block.Name) > maxStoredIdentifierBytes ||
				len(block.ToolUseID) > maxStoredIdentifierBytes ||
				len(block.Input) > maxStoredToolInputBytes ||
				(block.Input != nil && !json.Valid(block.Input)) {
				return transcriptMessage{}, false
			}
			block.Input = append(json.RawMessage(nil), block.Input...)
			block.ThoughtSignature = append([]byte(nil), block.ThoughtSignature...)
			blocks[i] = block
			continue
		}
		block.Type = boundedString(block.Type, maxStoredIdentifierBytes)
		block.Text = boundedString(block.Text, maxStoredTextBytes)
		block.ID = boundedString(block.ID, maxStoredIdentifierBytes)
		block.Name = boundedString(block.Name, maxStoredIdentifierBytes)
		block.ToolUseID = boundedString(block.ToolUseID, maxStoredIdentifierBytes)
		block.Content = boundedString(block.Content, maxStoredToolResultBytes)
		if block.Input != nil {
			if len(block.Input) > maxStoredToolInputBytes || !json.Valid(block.Input) {
				block.Input = json.RawMessage(`{"_cantinarr_truncated":true}`)
			} else {
				block.Input = append(json.RawMessage(nil), block.Input...)
			}
		}
		blocks[i] = block
	}
	message.Content = blocks
	return message, true
}

func isOpaqueContinuationBlock(block transcriptBlock) bool {
	return block.Type == blockTypeAnthropicThinking ||
		block.Type == blockTypeAnthropicRedactedThinking ||
		block.Type == blockTypeGeminiThought ||
		block.Signature != "" || block.Data != "" || len(block.ThoughtSignature) > 0
}

func opaqueContinuationBlockSize(block transcriptBlock) int {
	return len(block.Type) + len(block.Text) + len(block.ID) + len(block.Name) +
		len(block.Input) + len(block.ToolUseID) + len(block.Content) +
		len(block.Signature) + len(block.Data) + len(block.ThoughtSignature)
}

func boundedString(value string, limit int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= limit {
		return value
	}
	const suffix = "\n[truncated]"
	cut := limit - len(suffix)
	if cut <= 0 {
		return suffix[:limit]
	}
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + suffix
}

// followingToolResults collects tool_use IDs answered by the next message.
func followingToolResults(messages transcript, valid []bool, i int) map[string]bool {
	answered := make(map[string]bool)
	if i+1 < len(messages) && valid[i+1] {
		for _, block := range messages[i+1].Content {
			if block.Type == blockTypeToolResult {
				answered[block.ToolUseID] = true
			}
		}
	}
	return answered
}

// precedingToolUses identifies tool calls that make a user tool_result turn
// valid. It also removes result turns orphaned when an oversized signed
// assistant turn had to be dropped atomically.
func precedingToolUses(messages transcript, valid []bool, i int) map[string]bool {
	called := make(map[string]bool)
	if i == 0 || !valid[i-1] || messages[i-1].Role != agentRoleAssistant {
		return called
	}
	for _, block := range messages[i-1].Content {
		if block.Type == blockTypeToolUse {
			called[block.ID] = true
		}
	}
	return called
}

// stripOrphanToolUse removes tool_use blocks that have no matching tool_result.
func stripOrphanToolUse(m transcriptMessage, answered map[string]bool) transcriptMessage {
	hasOrphan := false
	for _, block := range m.Content {
		if block.Type == blockTypeToolUse && !answered[block.ID] {
			hasOrphan = true
			break
		}
	}
	if !hasOrphan {
		return m
	}
	kept := make([]transcriptBlock, 0, len(m.Content))
	for _, block := range m.Content {
		if block.Type == blockTypeToolUse && !answered[block.ID] {
			continue
		}
		kept = append(kept, block)
	}
	m.Content = kept
	return m
}

func stripOrphanToolResults(m transcriptMessage, called map[string]bool) transcriptMessage {
	kept := make([]transcriptBlock, 0, len(m.Content))
	for _, block := range m.Content {
		if block.Type == blockTypeToolResult && !called[block.ToolUseID] {
			continue
		}
		kept = append(kept, block)
	}
	m.Content = kept
	return m
}

func transcriptFromClient(messages []Message) transcript {
	out := make(transcript, 0, len(messages))
	for _, m := range messages {
		text := messageText(m.Content)
		if text == "" {
			continue
		}
		switch m.Role {
		case agentRoleAssistant:
			// Every provider requires the first conversational message to be
			// user-authored; drop display-only welcome assistant messages.
			if len(out) == 0 {
				continue
			}
			out = append(out, textTranscriptMessage(agentRoleAssistant, text))
		default:
			out = append(out, textTranscriptMessage(agentRoleUser, text))
		}
	}
	return out
}

func textTranscriptMessage(role, text string) transcriptMessage {
	return transcriptMessage{
		Role:    role,
		Content: []transcriptBlock{{Type: blockTypeText, Text: text}},
	}
}

func cloneTranscript(messages transcript) transcript {
	out := make(transcript, len(messages))
	for i, message := range messages {
		out[i].Role = message.Role
		if len(message.Content) == 0 {
			continue
		}
		out[i].Content = make([]transcriptBlock, len(message.Content))
		copy(out[i].Content, message.Content)
		for j := range out[i].Content {
			if out[i].Content[j].Input != nil {
				out[i].Content[j].Input = append(json.RawMessage(nil), out[i].Content[j].Input...)
			}
			if out[i].Content[j].ThoughtSignature != nil {
				out[i].Content[j].ThoughtSignature = append([]byte(nil), out[i].Content[j].ThoughtSignature...)
			}
		}
	}
	return out
}

func newConversationID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}
