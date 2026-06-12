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
func (s *conversationStore) Get(id string, userID int64) ([]anthropic.MessageParam, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok || conv.userID != userID || time.Since(conv.updatedAt) > conversationTTL {
		return nil, false
	}
	return conv.messages, true
}

// Put stores the full transcript for id, trimming old turns and evicting
// stale conversations.
func (s *conversationStore) Put(id string, userID int64, messages []anthropic.MessageParam) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.evictLocked()
	s.conversations[id] = &conversation{
		userID:    userID,
		messages:  trimHistory(messages, maxStoredMessages),
		updatedAt: time.Now(),
	}
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

func newConversationID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}
