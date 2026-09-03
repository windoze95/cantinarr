package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

func TestCarouselWatchDecisionTable(t *testing.T) {
	trendingItems := []mcp.MediaResultItem{{ID: 101, Title: "Dune Part Three", MediaType: "movie"}}

	t.Run("source with items and text answer owes a nudge exactly once", func(t *testing.T) {
		w := &carouselWatch{}
		w.observe("get_trending", trendingItems)
		if !w.shouldNudge("Trending right now: Dune Part Three.") {
			t.Fatal("shouldNudge = false, want true")
		}
		if got := w.markNudged(); got != displayMediaNudge {
			t.Fatalf("markNudged returned %q", got)
		}
		if w.shouldNudge("still no carousel") {
			t.Fatal("second shouldNudge = true, want false (nudge is one-time)")
		}
	})

	t.Run("a browse result owes a carousel like a search does", func(t *testing.T) {
		w := &carouselWatch{}
		w.observe("browse_titles", trendingItems)
		if !w.shouldNudge("Try Dune Part Three.") {
			t.Fatal("shouldNudge = false after browse_titles returned items")
		}
	})

	t.Run("display_media call settles the debt", func(t *testing.T) {
		w := &carouselWatch{}
		w.observe("get_trending", trendingItems)
		w.observe("display_media", trendingItems)
		if w.shouldNudge("Trending right now: Dune Part Three.") {
			t.Fatal("shouldNudge = true after display_media ran")
		}
	})

	t.Run("empty source results never owe a carousel", func(t *testing.T) {
		w := &carouselWatch{}
		w.observe("get_trending", []mcp.MediaResultItem{})
		if w.shouldNudge("Nothing is trending.") {
			t.Fatal("shouldNudge = true for an empty source result")
		}
	})

	t.Run("non-source tools never owe a carousel", func(t *testing.T) {
		w := &carouselWatch{}
		w.observe("get_library", trendingItems)
		w.observe("get_queue", trendingItems)
		if w.shouldNudge("You have 638 movies.") {
			t.Fatal("shouldNudge = true without a carousel-source tool")
		}
	})

	t.Run("blank final text never triggers the nudge", func(t *testing.T) {
		w := &carouselWatch{}
		w.observe("search_movies", trendingItems)
		if w.shouldNudge("  \n") {
			t.Fatal("shouldNudge = true for blank text")
		}
	})
}

// fakeTMDBForNudge serves the two TMDB endpoints the nudge loop test needs:
// a one-movie trending page and the matching movie-details verification read.
func fakeTMDBForNudge(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/trending/movie/"):
			_, _ = io.WriteString(w, `{"results":[{"id":101,"title":"Dune Part Three","overview":"Sand.","release_date":"2026-01-01","vote_average":8.8,"media_type":"movie"}]}`)
		case r.URL.Path == "/movie/101":
			_, _ = io.WriteString(w, `{"id":101,"title":"Dune Part Three","release_date":"2026-01-01"}`)
		default:
			t.Errorf("unexpected TMDB request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeNudgeTextSSE(w http.ResponseWriter, id, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	payload, _ := json.Marshal(text)
	_, _ = io.WriteString(w, `data: {"id":"`+id+`","object":"chat.completion.chunk","created":1,"model":"local-test","choices":[{"index":0,"delta":{"role":"assistant","content":`+string(payload)+`},"finish_reason":"stop"}]}`+"\n\n")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func writeNudgeToolCallSSE(w http.ResponseWriter, id, name, arguments string) {
	w.Header().Set("Content-Type", "text/event-stream")
	payload, _ := json.Marshal(arguments)
	_, _ = io.WriteString(w, `data: {"id":"`+id+`","object":"chat.completion.chunk","created":1,"model":"local-test","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"`+id+`_call","type":"function","function":{"name":"`+name+`","arguments":`+string(payload)+`}}]},"finish_reason":"tool_calls"}]}`+"\n\n")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// The live failure this pins down (2026-08-21, issue #484 testing): a local
// model answered a trending question with prose titles and never called
// display_media, so the app rendered no carousel. The loop must remind the
// model once — silently — and the reminded display_media call must still
// reach OnToolResult, while post-nudge prose stays off the wire.
func TestOpenAIInteractiveLoopNudgesOwedCarousel(t *testing.T) {
	tmdbServer := fakeTMDBForNudge(t)

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	registry := credentials.NewRegistry(database, nil,
		credentials.WithDefaultTMDBToken("test-token"),
		credentials.WithTMDBBaseURL(tmdbServer.URL),
	)
	toolServer := mcp.NewToolServer(registry, nil, nil, nil)
	toolServer.SetCallAuthorizer(func(_ context.Context, callCtx mcp.CallContext) (string, error) { return callCtx.Role, nil })

	var requests atomic.Int32
	openAIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch requests.Add(1) {
		case 1:
			writeNudgeToolCallSSE(w, "leg1", "get_trending", `{"media_type":"movie"}`)
		case 2:
			if !strings.Contains(string(body), "Dune Part Three") {
				t.Errorf("second request is missing the trending tool result: %s", body)
			}
			writeNudgeTextSSE(w, "leg2", "Trending right now: Dune Part Three.")
		case 3:
			if !strings.Contains(string(body), "you never called display_media") {
				t.Errorf("third request did not carry the nudge: %s", body)
			}
			writeNudgeToolCallSSE(w, "leg3", "display_media",
				`{"items":[{"tmdb_id":101,"media_type":"movie","title":"Dune Part Three","year":"2026"}]}`)
		case 4:
			writeNudgeTextSSE(w, "leg4", "Posted the carousel.")
		default:
			t.Errorf("unexpected extra model request #%d", requests.Load())
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(openAIServer.Close)
	t.Setenv("OPENAI_BASE_URL", openAIServer.URL+"/v1")

	var texts []string
	var mediaResults []any
	callbacks := StreamCallbacks{
		OnText: func(text string) { texts = append(texts, text) },
		OnToolResult: func(name string, data any) {
			if name == "display_media" {
				mediaResults = append(mediaResults, data)
			}
		},
	}

	history := transcript{textTranscriptMessage(agentRoleUser, "whats trending")}
	finalHistory, err := NewOpenAIService("secret", "local-test", "", "", toolServer).SendMessage(
		context.Background(), history, ChatContext{UserID: 7, Role: auth.RoleUser}, callbacks,
	)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if got := requests.Load(); got != 4 {
		t.Fatalf("model requests = %d, want 4 (tool, text, nudged tool, close)", got)
	}
	if len(mediaResults) != 1 {
		t.Fatalf("display_media OnToolResult fired %d times, want 1", len(mediaResults))
	}
	// ExecuteTool's redaction pass JSON round-trips structured data, so the
	// client-facing frame arrives as []any of maps.
	items, ok := mediaResults[0].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("media results = %#v, want one redacted-JSON item", mediaResults[0])
	}
	if fields, ok := items[0].(map[string]any); !ok || fields["title"] != "Dune Part Three" {
		t.Fatalf("media result item = %#v, want the verified Dune item", items[0])
	}

	streamed := strings.Join(texts, "")
	if !strings.Contains(streamed, "Trending right now: Dune Part Three.") {
		t.Fatalf("streamed text lost the real answer: %q", streamed)
	}
	if strings.Contains(streamed, "Posted the carousel.") {
		t.Fatalf("post-nudge prose leaked to the client: %q", streamed)
	}

	var nudges int
	for _, message := range finalHistory {
		for _, block := range message.Content {
			if block.Type == blockTypeText && strings.Contains(block.Text, "you never called display_media") {
				nudges++
			}
		}
	}
	if nudges != 1 {
		t.Fatalf("transcript carries %d nudges, want exactly 1", nudges)
	}
}

// A turn that consumed trending results and answered with text but was built
// from an empty source result must end without extra legs — the nudge never
// fires when no carousel is owed.
func TestOpenAIInteractiveLoopDoesNotNudgeEmptySources(t *testing.T) {
	tmdbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	t.Cleanup(tmdbServer.Close)

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	registry := credentials.NewRegistry(database, nil,
		credentials.WithDefaultTMDBToken("test-token"),
		credentials.WithTMDBBaseURL(tmdbServer.URL),
	)
	toolServer := mcp.NewToolServer(registry, nil, nil, nil)
	toolServer.SetCallAuthorizer(func(_ context.Context, callCtx mcp.CallContext) (string, error) { return callCtx.Role, nil })

	var requests atomic.Int32
	openAIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1:
			writeNudgeToolCallSSE(w, "leg1", "get_trending", `{"media_type":"movie"}`)
		case 2:
			writeNudgeTextSSE(w, "leg2", "Nothing is trending right now.")
		default:
			t.Errorf("unexpected extra model request #%d", requests.Load())
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(openAIServer.Close)
	t.Setenv("OPENAI_BASE_URL", openAIServer.URL+"/v1")

	history := transcript{textTranscriptMessage(agentRoleUser, "whats trending")}
	_, err = NewOpenAIService("secret", "local-test", "", "", toolServer).SendMessage(
		context.Background(), history, ChatContext{UserID: 7, Role: auth.RoleUser}, StreamCallbacks{},
	)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("model requests = %d, want 2 (no nudge for empty results)", got)
	}
}
