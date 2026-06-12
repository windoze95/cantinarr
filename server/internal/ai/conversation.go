package ai

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

const (
	conversationTTL   = 4 * time.Hour
	maxConversations  = 200
	maxStoredMessages = 60
)

// conversationStore keeps full agent transcripts (including tool_use and
// tool_result blocks) server-side so follow-up turns retain grounding that
// the client's plain-text transcript cannot carry.
type conversationStore struct {
	mu            sync.Mutex
	conversations map[string]*conversation
}

type conversation struct {
	userID    int64
	messages  []anthropic.MessageParam
	updatedAt time.Time
}

func newConversationStore() *conversationStore {
	return &conversationStore{conversations: make(map[string]*conversation)}
}

// Get returns the stored history for id if it exists and belongs to userID.
// The returned slice is a copy: callers append to it while the loop runs, and
// sharing the backing array with the stored entry would race with concurrent
// turns on the same conversation.
func (s *conversationStore) Get(id string, userID int64) ([]anthropic.MessageParam, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok || conv.userID != userID || time.Since(conv.updatedAt) > conversationTTL {
		return nil, false
	}
	return append([]anthropic.MessageParam(nil), conv.messages...), true
}

// Put stores the full transcript for id, trimming old turns and evicting
// stale conversations. The transcript is copied so the store never aliases a
// caller's slice.
func (s *conversationStore) Put(id string, userID int64, messages []anthropic.MessageParam) {
	trimmed := trimHistory(messages, maxStoredMessages)
	s.mu.Lock()
	defer s.mu.Unlock()

	s.evictLocked()
	s.conversations[id] = &conversation{
		userID:    userID,
		messages:  append([]anthropic.MessageParam(nil), trimmed...),
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
func trimHistory(messages []anthropic.MessageParam, maxLen int) []anthropic.MessageParam {
	if len(messages) <= maxLen {
		return messages
	}
	for i := len(messages) - maxLen; i < len(messages); i++ {
		if messages[i].Role != anthropic.MessageParamRoleUser {
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

func containsToolResult(m anthropic.MessageParam) bool {
	for _, block := range m.Content {
		if block.OfToolResult != nil {
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
func sanitizeTranscript(messages []anthropic.MessageParam) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(messages))
	for i, m := range messages {
		if m.Role == anthropic.MessageParamRoleAssistant {
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
func followingToolResults(messages []anthropic.MessageParam, i int) map[string]bool {
	answered := make(map[string]bool)
	if i+1 < len(messages) {
		for _, block := range messages[i+1].Content {
			if tr := block.OfToolResult; tr != nil {
				answered[tr.ToolUseID] = true
			}
		}
	}
	return answered
}

// stripOrphanToolUse removes tool_use blocks that have no matching tool_result.
func stripOrphanToolUse(m anthropic.MessageParam, answered map[string]bool) anthropic.MessageParam {
	hasOrphan := false
	for _, block := range m.Content {
		if tu := block.OfToolUse; tu != nil && !answered[tu.ID] {
			hasOrphan = true
			break
		}
	}
	if !hasOrphan {
		return m
	}
	kept := make([]anthropic.ContentBlockParamUnion, 0, len(m.Content))
	for _, block := range m.Content {
		if tu := block.OfToolUse; tu != nil && !answered[tu.ID] {
			continue
		}
		kept = append(kept, block)
	}
	m.Content = kept
	return m
}

func newConversationID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}
