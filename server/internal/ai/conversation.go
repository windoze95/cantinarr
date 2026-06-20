package ai

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

const (
	conversationTTL   = 4 * time.Hour
	maxConversations  = 200
	maxStoredMessages = 60

	agentRoleUser      = "user"
	agentRoleAssistant = "assistant"

	blockTypeText       = "text"
	blockTypeToolUse    = "tool_use"
	blockTypeToolResult = "tool_result"
)

// transcript is the provider-neutral history used by the agent loop. It keeps
// tool calls/results server-side so follow-up turns remain grounded no matter
// which AI provider is selected.
type transcript []transcriptMessage

type transcriptMessage struct {
	Role    string
	Content []transcriptBlock
}

type transcriptBlock struct {
	Type      string
	Text      string
	ID        string
	Name      string
	Input     json.RawMessage
	ToolUseID string
	Content   string
	IsError   bool
}

// conversationStore keeps full provider-neutral agent transcripts server-side.
type conversationStore struct {
	mu            sync.Mutex
	conversations map[string]*conversation
}

type conversation struct {
	userID    int64
	messages  transcript
	updatedAt time.Time
}

func newConversationStore() *conversationStore {
	return &conversationStore{conversations: make(map[string]*conversation)}
}

// Get returns the stored history for id if it exists and belongs to userID.
// The returned slice is a copy: callers append to it while the loop runs, and
// sharing the backing array with the stored entry would race with concurrent
// turns on the same conversation.
func (s *conversationStore) Get(id string, userID int64) (transcript, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok || conv.userID != userID || time.Since(conv.updatedAt) > conversationTTL {
		return nil, false
	}
	return cloneTranscript(conv.messages), true
}

// Put stores the full transcript for id, trimming old turns and evicting
// stale conversations. The transcript is copied so the store never aliases a
// caller's slice.
func (s *conversationStore) Put(id string, userID int64, messages transcript) {
	trimmed := trimHistory(messages, maxStoredMessages)
	s.mu.Lock()
	defer s.mu.Unlock()

	s.evictLocked()
	s.conversations[id] = &conversation{
		userID:    userID,
		messages:  cloneTranscript(trimmed),
		updatedAt: time.Now(),
	}
}

// Delete removes a conversation, used to invalidate state after a failed turn
// so retries fall back to the client transcript instead of replaying a
// possibly poisoned history.
func (s *conversationStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conversations, id)
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

// trimHistory bounds the transcript while keeping it valid for the API: the
// first retained message must be a plain user message (not tool results),
// so assistant tool_use blocks are never orphaned from their results.
func trimHistory(messages transcript, maxLen int) transcript {
	if len(messages) <= maxLen {
		return messages
	}
	for i := len(messages) - maxLen; i < len(messages); i++ {
		if messages[i].Role != agentRoleUser {
			continue
		}
		if containsToolResult(messages[i]) {
			continue
		}
		return messages[i:]
	}
	// No safe boundary found in the window; keep everything rather than
	// risk an invalid transcript.
	return messages
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
	out := make(transcript, 0, len(messages))
	for i, m := range messages {
		if m.Role == agentRoleAssistant {
			m = stripOrphanToolUse(m, followingToolResults(messages, i))
		}
		if len(m.Content) == 0 {
			continue
		}
		out = append(out, m)
	}
	return out
}

// followingToolResults collects tool_use IDs answered by the next message.
func followingToolResults(messages transcript, i int) map[string]bool {
	answered := make(map[string]bool)
	if i+1 < len(messages) {
		for _, block := range messages[i+1].Content {
			if block.Type == blockTypeToolResult {
				answered[block.ToolUseID] = true
			}
		}
	}
	return answered
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
