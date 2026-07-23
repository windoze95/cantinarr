package chaptarr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAuthorUpdateRoundTripsCompleteResource(t *testing.T) {
	const authorJSON = `{
		"id":2101,
		"authorName":"Mara Vale",
		"foreignAuthorId":"hc:author-2001",
		"monitored":false,
		"ebookMonitorFuture":false,
		"audiobookMonitorFuture":false,
		"futurePolicy":{"mode":"keep-me"}
	}`

	var putBody map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/author/2101" {
			t.Errorf("path = %q", r.URL.Path)
		}
		switch r.Method {
		case http.MethodGet:
			_, _ = io.WriteString(w, authorJSON)
		case http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
				t.Errorf("decode PUT body: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("method = %q", r.Method)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "key")
	author, err := client.GetAuthorContext(context.Background(), 2101)
	if err != nil {
		t.Fatalf("GetAuthorContext: %v", err)
	}
	author.Monitored = true
	author.AudiobookMonitorFuture = true
	if _, err := client.UpdateAuthorContext(context.Background(), *author); err != nil {
		t.Fatalf("UpdateAuthorContext: %v", err)
	}

	if string(putBody["futurePolicy"]) != `{"mode":"keep-me"}` {
		t.Fatalf("futurePolicy was not preserved: %s", putBody["futurePolicy"])
	}
	assertRawBool(t, putBody, "monitored", true)
	assertRawBool(t, putBody, "audiobookMonitorFuture", true)
	assertRawBool(t, putBody, "ebookMonitorFuture", false)
}

func TestEditionReadAndBookUpdateRoundTripCompleteResources(t *testing.T) {
	const bookJSON = `{
		"id":4101,
		"authorId":2101,
		"title":"The Clockwork Orchard",
		"foreignBookId":"hc:work-1001",
		"foreignEditionId":"hc:edition-audio",
		"mediaType":"audiobook",
		"monitored":false,
		"ebookMonitored":false,
		"audiobookMonitored":false,
		"anyEditionOk":true,
		"hasFiles":false,
		"grabbed":false,
		"genres":"Fantasy, Mystery",
		"images":[{"coverType":"cover","url":"/cover.jpg","futureImage":true}],
		"ratings":{"popularity":87.5,"votes":240},
		"editions":[],
		"futureBookField":{"mode":"keep-me"}
	}`
	const editionsJSON = `[
		{
			"id":8101,
			"bookId":4101,
			"title":"The Clockwork Orchard",
			"foreignEditionId":"hc:edition-audio",
			"format":"audiobook",
			"isEbook":false,
			"language":"eng",
			"monitored":false,
			"manualAdd":false,
			"links":[{"url":"https://example.test/audio","name":"Source"}],
			"futureEditionField":{"mode":"keep-me"}
		},
		{
			"id":8102,
			"bookId":4101,
			"title":"The Clockwork Orchard",
			"foreignEditionId":"hc:edition-physical",
			"format":"physical",
			"isEbook":false,
			"language":"eng",
			"monitored":false,
			"manualAdd":false,
			"links":[]
		}
	]`

	var putBody map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/4101":
			_, _ = io.WriteString(w, bookJSON)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/edition":
			if got := r.URL.Query().Get("bookId"); got != "4101" {
				t.Errorf("edition bookId = %q, want 4101", got)
			}
			_, _ = io.WriteString(w, editionsJSON)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/book/4101":
			if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
				t.Errorf("decode PUT body: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("request = %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "key")
	book, err := client.GetBookContext(context.Background(), 4101)
	if err != nil {
		t.Fatalf("GetBookContext: %v", err)
	}
	if book.ForeignEditionID != "hc:edition-audio" || book.MediaType != FormatAudiobook || book.Ratings.Votes != 240 {
		t.Fatalf("typed book fields = %#v", book)
	}
	editions, err := client.GetEditionsContext(context.Background(), 4101)
	if err != nil {
		t.Fatalf("GetEditionsContext: %v", err)
	}
	if len(editions) != 2 || editions[0].Format != FormatAudiobook || editions[0].Language != "eng" || editions[1].Format != "physical" {
		t.Fatalf("typed editions = %#v", editions)
	}

	book.AnyEditionOk = false
	book.AudiobookMonitored = true
	book.Editions = editions
	book.Editions[0].Monitored = true
	book.Editions[0].ManualAdd = true
	if _, err := client.UpdateBookContext(context.Background(), *book); err != nil {
		t.Fatalf("UpdateBookContext: %v", err)
	}

	if string(putBody["futureBookField"]) != `{"mode":"keep-me"}` {
		t.Fatalf("futureBookField was not preserved: %s", putBody["futureBookField"])
	}
	if string(putBody["genres"]) != `"Fantasy, Mystery"` {
		t.Fatalf("genres shape changed during round trip: %s", putBody["genres"])
	}
	assertRawBool(t, putBody, "anyEditionOk", false)
	assertRawBool(t, putBody, "audiobookMonitored", true)

	var sentEditions []map[string]json.RawMessage
	if err := json.Unmarshal(putBody["editions"], &sentEditions); err != nil {
		t.Fatalf("decode sent editions: %v", err)
	}
	if len(sentEditions) != 2 {
		t.Fatalf("sent edition count = %d", len(sentEditions))
	}
	assertRawBool(t, sentEditions[0], "monitored", true)
	assertRawBool(t, sentEditions[0], "manualAdd", true)
	assertRawBool(t, sentEditions[1], "monitored", false)
	assertRawBool(t, sentEditions[1], "manualAdd", false)
	if !strings.Contains(string(sentEditions[0]["links"]), "https://example.test/audio") {
		t.Fatalf("edition links were not preserved: %s", sentEditions[0]["links"])
	}
	if string(sentEditions[0]["futureEditionField"]) != `{"mode":"keep-me"}` {
		t.Fatalf("future edition field was not preserved: %s", sentEditions[0]["futureEditionField"])
	}
}

func TestCommandsDecodeAndBookSearchAcknowledgement(t *testing.T) {
	var postBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = io.WriteString(w, `[
				{"id":7001,"commandName":"RefreshAuthor","status":"started","body":{"authorId":2101}},
				{"id":7100,"name":"BookSearch","status":"queued","body":{"bookIds":[4101]}}
			]`)
		case http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&postBody); err != nil {
				t.Errorf("decode command POST: %v", err)
			}
			_, _ = io.WriteString(w, `{"id":7101,"name":"BookSearch","status":"queued","futureField":true}`)
		default:
			t.Errorf("method = %q", r.Method)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "key")
	commands, err := client.GetCommandsContext(context.Background())
	if err != nil {
		t.Fatalf("GetCommandsContext: %v", err)
	}
	if len(commands) != 2 || commands[0].EffectiveName() != "RefreshAuthor" || commands[0].Body.AuthorID != 2101 {
		t.Fatalf("commands = %#v", commands)
	}
	if len(commands[1].Body.BookIDs) != 1 || commands[1].Body.BookIDs[0] != 4101 {
		t.Fatalf("BookSearch body = %#v", commands[1].Body)
	}

	ack, err := client.QueueBookSearchContext(context.Background(), []int{4101})
	if err != nil {
		t.Fatalf("QueueBookSearchContext: %v", err)
	}
	if !ack.Acknowledges() || !ack.Acknowledges("booksearch") || ack.Acknowledges("RefreshAuthor") {
		t.Fatalf("acknowledgement = %#v", ack)
	}
	if postBody["name"] != "BookSearch" {
		t.Fatalf("command name = %#v", postBody["name"])
	}
	ids, ok := postBody["bookIds"].([]any)
	if !ok || len(ids) != 1 || ids[0] != float64(4101) {
		t.Fatalf("command bookIds = %#v", postBody["bookIds"])
	}
}

func TestCommandAcknowledgementRejectsUnconfirmedResponses(t *testing.T) {
	tests := []Command{
		{ID: 0, Name: "BookSearch", Status: "queued"},
		{ID: 1, Name: "BookSearch", Status: "failed"},
		{ID: 1, Name: "BookSearch", Status: "unexpected"},
		{ID: 1, Name: "RefreshAuthor", Status: "queued"},
	}
	for _, command := range tests {
		if command.Acknowledges("BookSearch") {
			t.Errorf("Command %#v unexpectedly acknowledged BookSearch", command)
		}
	}
}

func TestDecodeCommandsRejectsMissingListShape(t *testing.T) {
	for _, raw := range []string{"", "null", `{}`, `{"page":1}`} {
		if _, err := decodeCommands(json.RawMessage(raw)); err == nil {
			t.Errorf("decodeCommands(%q) error = nil", raw)
		}
	}
}

func TestRequestContractContextCancellationAndSanitizedError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, `private path /library/secret and signed-token`, http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "key")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.LookupBookContext(canceled, "title"); err == nil {
		t.Fatal("LookupBookContext succeeded with canceled context")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("canceled lookup reached server %d times", got)
	}

	_, err := client.GetEditionsContext(context.Background(), 42)
	if err == nil {
		t.Fatal("GetEditionsContext accepted upstream error")
	}
	if message := err.Error(); strings.Contains(message, "/library/secret") || strings.Contains(message, "signed-token") || strings.Contains(message, server.URL) {
		t.Fatalf("error leaked upstream details: %q", message)
	}
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("error = %v, want wrapped HTTPStatusError 502", err)
	}
	if statusErr.Path != "/api/v1/edition" || statusErr.Method != http.MethodGet {
		t.Fatalf("status error = %#v, want sanitized method/path", statusErr)
	}
}

func assertRawBool(t *testing.T, fields map[string]json.RawMessage, key string, want bool) {
	t.Helper()
	var got bool
	if err := json.Unmarshal(fields[key], &got); err != nil {
		t.Fatalf("decode %s: %v (%s)", key, err, fields[key])
	}
	if got != want {
		t.Fatalf("%s = %t, want %t", key, got, want)
	}
}
