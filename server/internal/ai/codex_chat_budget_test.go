package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// TestCodexChatBudgetHelperProcess fakes an app-server whose model keeps
// requesting tools until Cantinarr's budget refusal arrives, then answers from
// gathered data; with --fake-flood-forever it never stops calling tools.
// Termination is behavior-driven on purpose: this package cannot import the
// codexapp refusal text, and if that text is ever reworded this fake fails
// loudly through the hard backstop instead of hanging.
func TestCodexChatBudgetHelperProcess(t *testing.T) {
	if !slices.Contains(os.Args, "--codex-chat-budget-fake") {
		return
	}
	encoder := json.NewEncoder(os.Stdout)
	send := func(value any) {
		if err := encoder.Encode(value); err != nil {
			t.Fatalf("encode fake response: %v", err)
		}
	}
	toolCall := func(n int) map[string]any {
		return map[string]any{"id": fmt.Sprintf("flood-%d", n), "method": "item/tool/call", "params": map[string]any{
			"callId": fmt.Sprintf("call-%d", n), "threadId": "thread-1", "turnId": "turn-1", "tool": "search_movies", "arguments": map[string]any{"query": fmt.Sprintf("query %d", n)},
		}}
	}
	forever := slices.Contains(os.Args, "--fake-flood-forever")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result struct {
				Success      bool `json:"success"`
				ContentItems []struct {
					Text string `json:"text"`
				} `json:"contentItems"`
			} `json:"result"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(message.ID) == 0 {
			continue
		}
		id := json.RawMessage(append([]byte(nil), message.ID...))
		switch message.Method {
		case "initialize":
			send(map[string]any{"id": id, "result": map[string]any{"codexHome": os.Getenv("CODEX_HOME")}})
		case "thread/start":
			send(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "thread-1"}}})
		case "thread/inject_items":
			send(map[string]any{"id": id, "result": map[string]any{}})
		case "turn/start":
			send(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress", "items": []any{}}}})
			send(toolCall(1))
		case "turn/interrupt":
			send(map[string]any{"id": id, "result": map[string]any{}})
			send(map[string]any{"method": "turn/completed", "params": map[string]any{
				"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "interrupted", "items": []any{}},
			}})
		case "":
			key := strings.Trim(string(message.ID), `"`)
			if !strings.HasPrefix(key, "flood-") {
				continue
			}
			n, err := strconv.Atoi(strings.TrimPrefix(key, "flood-"))
			if err != nil {
				t.Fatalf("unexpected reply id %q", key)
			}
			text := ""
			if len(message.Result.ContentItems) > 0 {
				text = message.Result.ContentItems[0].Text
			}
			if !forever && strings.Contains(text, "Tool call limit reached") {
				send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{
					"threadId": "thread-1", "turnId": "turn-1", "itemId": "message-1", "delta": "answer from gathered data",
				}})
				send(map[string]any{"method": "turn/completed", "params": map[string]any{
					"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed", "items": []any{}},
				}})
				continue
			}
			send(toolCall(n + 1))
		default:
			send(map[string]any{"id": id, "result": map[string]any{}})
		}
	}
}

func newCodexChatBudgetHandler(t *testing.T, extraArgs ...string) (*Handler, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	result, err := database.Exec(`INSERT INTO users (username, password_hash, role) VALUES ('budget-user', '', 'user')`)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry := credentials.NewRegistry(database, cipher)
	toolServer := mcp.NewToolServer(registry, nil, nil, nil)
	toolServer.SetCallAuthorizer(func(context.Context, mcp.CallContext) (string, error) {
		return auth.RoleUser, nil
	})
	runtimeDir := t.TempDir()
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manager := codexapp.NewManager(database, cipher, toolServer, codexapp.Options{
		Binary:                   os.Args[0],
		RuntimeDir:               runtimeDir,
		Args:                     append([]string{"-test.run=TestCodexChatBudgetHelperProcess", "--", "--codex-chat-budget-fake"}, extraArgs...),
		AllowDiskRuntimeForTests: true,
	})
	if !manager.Available() {
		t.Fatalf("test Codex manager unavailable: %v", manager.AvailabilityError())
	}
	h := NewHandler(registry, toolServer, manager)
	grantResolverSharedAccess(t, database, userID, true)
	configureResolverProfile(t, registry, database, cipher, userID, aiSourceShared, credentials.AIProviderCodex, true)
	return h, userID
}

func chatSSEFrames(t *testing.T, body string) []map[string]json.RawMessage {
	t.Helper()
	var frames []map[string]json.RawMessage
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var frame map[string]json.RawMessage
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			t.Fatalf("bad SSE frame %q: %v", line, err)
		}
		frames = append(frames, frame)
	}
	return frames
}

func postChat(t *testing.T, h *Handler, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/ai/chat", strings.NewReader(`{"messages":[{"role":"user","content":"find horror movies I do not have"}]}`))
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleUser, DeviceID: "device-1"}))
	recorder := httptest.NewRecorder()
	h.Chat(recorder, req)
	return recorder
}

// TestChatPersistsConversationAfterCodexSoftLanding proves the issue #492 fix
// end to end: a turn that exhausts the tool budget still streams a final
// answer, emits no error frame, and keeps the stored conversation.
func TestChatPersistsConversationAfterCodexSoftLanding(t *testing.T) {
	h, userID := newCodexChatBudgetHandler(t)
	recorder := postChat(t, h, userID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	convID := ""
	text := ""
	for _, frame := range chatSSEFrames(t, recorder.Body.String()) {
		if raw, ok := frame["error"]; ok {
			t.Fatalf("budget-exhausted turn emitted an error frame: %s", raw)
		}
		if raw, ok := frame["conversation_id"]; ok {
			if err := json.Unmarshal(raw, &convID); err != nil {
				t.Fatal(err)
			}
		}
		if raw, ok := frame["text"]; ok {
			var delta string
			if err := json.Unmarshal(raw, &delta); err != nil {
				t.Fatal(err)
			}
			text += delta
		}
	}
	if convID == "" {
		t.Fatal("no conversation_id frame")
	}
	if text != "answer from gathered data" {
		t.Fatalf("streamed text = %q", text)
	}
	binding := h.conversations.newBinding(userID, h.resolveAI(context.Background(), userID))
	stored, ok := h.conversations.Get(convID, userID, binding)
	if !ok {
		t.Fatal("budget-exhausted turn did not persist the conversation")
	}
	last := stored[len(stored)-1]
	if last.Role != agentRoleAssistant {
		t.Fatalf("last stored message role = %q, want assistant", last.Role)
	}
	// Question + capped tool pairs + final answer. Without the per-turn record
	// cap, trimHistory finds no valid suffix for a maximal turn and keeps only
	// the user question, silently dropping the answer and all tool grounding.
	if want := 2 + 2*maxStoredToolRecordsPerTurn; len(stored) != want {
		t.Fatalf("stored transcript length = %d, want %d", len(stored), want)
	}
}

// TestChatEmitsHonestErrorAfterCodexHardAbort pins the copy for the rare case
// where the model ignores every budget refusal: the SSE error frame names the
// tool limit instead of blaming the OAuth connection, and the conversation is
// dropped like any other failed turn.
func TestChatEmitsHonestErrorAfterCodexHardAbort(t *testing.T) {
	h, userID := newCodexChatBudgetHandler(t, "--fake-flood-forever")
	recorder := postChat(t, h, userID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	convID := ""
	errorText := ""
	for _, frame := range chatSSEFrames(t, recorder.Body.String()) {
		if raw, ok := frame["conversation_id"]; ok {
			if err := json.Unmarshal(raw, &convID); err != nil {
				t.Fatal(err)
			}
		}
		if raw, ok := frame["error"]; ok {
			if err := json.Unmarshal(raw, &errorText); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := "The AI needed more lookups than one question allows and had to stop. Try again, or split the question into smaller parts."
	if errorText != want {
		t.Fatalf("error frame = %q, want %q", errorText, want)
	}
	binding := h.conversations.newBinding(userID, h.resolveAI(context.Background(), userID))
	if _, ok := h.conversations.Get(convID, userID, binding); ok {
		t.Fatal("hard-aborted turn must not persist the conversation")
	}
}
