package request

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

const chaptarrQualityProfiles0720 = `[
	{"id":11,"name":"Digital Standard","profileType":"ebook"},
	{"id":12,"name":"Spoken Standard","profileType":"audiobook"}
]`

const chaptarrMetadataProfiles0720 = `[
	{"id":20,"name":"None","profileType":0},
	{"id":21,"name":"Digital Metadata","profileType":2},
	{"id":22,"name":"Spoken Metadata","profileType":1}
]`

// Chaptarr 0.9.720 may omit accessible and expose ebook/audiobook as nested
// settings objects on every root. Only the effective-default flags and the
// root's name/path distinguish the formats in this fixture.
const chaptarrRootFolders0720 = `[
	{
		"id":31,
		"name":"Ebooks",
		"path":"/library/ebooks",
		"ebook":{"writeAudioBookShelfMetadataJson":false,"tags":[]},
		"audiobook":{"writeAudioBookShelfMetadataJson":false,"tags":[]},
		"isEffectiveDefaultEbook":false,
		"isEffectiveDefaultAudiobook":false
	},
	{
		"id":32,
		"name":"Audiobooks",
		"path":"/library/audiobooks",
		"ebook":{"writeAudioBookShelfMetadataJson":false,"tags":[]},
		"audiobook":{"writeAudioBookShelfMetadataJson":false,"tags":[]},
		"isEffectiveDefaultEbook":false,
		"isEffectiveDefaultAudiobook":false
	}
]`

func TestChaptarr0720ConfigSelectorsUseFormatDiscriminators(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(chaptarrQualityProfiles0720))
		case "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(chaptarrMetadataProfiles0720))
		case "/api/v1/rootfolder":
			_, _ = w.Write([]byte(chaptarrRootFolders0720))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	client := chaptarr.NewClient(upstream.URL, "key")
	quality, err := client.GetQualityProfiles()
	if err != nil {
		t.Fatalf("GetQualityProfiles: %v", err)
	}
	metadata, err := client.GetMetadataProfiles()
	if err != nil {
		t.Fatalf("GetMetadataProfiles: %v", err)
	}
	roots, err := client.GetRootFolders()
	if err != nil {
		t.Fatalf("GetRootFolders: %v", err)
	}

	for _, tc := range []struct {
		format       string
		wantQuality  int
		wantMetadata int
		wantRoot     string
	}{
		{format: BookFormatEbook, wantQuality: 11, wantMetadata: 21, wantRoot: "/library/ebooks"},
		{format: BookFormatAudiobook, wantQuality: 12, wantMetadata: 22, wantRoot: "/library/audiobooks"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			if id, ok := selectBookQualityProfile(quality, tc.format); !ok || id != tc.wantQuality {
				t.Fatalf("quality profile = %d ok=%v, want %d", id, ok, tc.wantQuality)
			}
			if id, ok := selectBookMetadataProfile(metadata, tc.format); !ok || id != tc.wantMetadata {
				t.Fatalf("metadata profile = %d ok=%v, want %d", id, ok, tc.wantMetadata)
			}
			if root, ok := selectBookRoot(roots, tc.format); !ok || root.Path != tc.wantRoot {
				t.Fatalf("root = %+v ok=%v, want %q", root, ok, tc.wantRoot)
			}
		})
	}
}

func TestChaptarrTypedProfilesAndPerFormatAuthorsFailClosedOnAmbiguity(t *testing.T) {
	if id, ok := selectBookQualityProfile([]chaptarr.QualityProfile{
		{ID: 11, Name: "Ebook Standard", ProfileType: BookFormatEbook},
		{ID: 13, Name: "Ebook Default", ProfileType: BookFormatEbook},
	}, BookFormatEbook); !ok || id != 13 {
		t.Fatalf("typed ebook quality default = %d ok=%v, want 13", id, ok)
	}
	if id, ok := selectBookMetadataProfile([]chaptarr.MetadataProfile{
		{ID: 21, Name: "Ebook Standard", ProfileType: "2"},
		{ID: 23, Name: "Ebook Default", ProfileType: "2"},
	}, BookFormatEbook); !ok || id != 23 {
		t.Fatalf("typed ebook metadata default = %d ok=%v, want 23", id, ok)
	}
	if _, ok := selectBookQualityProfile([]chaptarr.QualityProfile{
		{ID: 11, Name: "Ebook Standard", ProfileType: BookFormatEbook},
		{ID: 13, Name: "Ebook High", ProfileType: BookFormatEbook},
	}, BookFormatEbook); ok {
		t.Fatal("multiple typed ebook quality profiles without a default were guessed")
	}
	if id, ok := selectBookQualityProfile([]chaptarr.QualityProfile{
		{ID: 11, Name: "Ebook Standard", ProfileType: BookFormatEbook},
		{ID: 12, Name: "Audiobook"},
	}, BookFormatAudiobook); !ok || id != 12 {
		t.Fatalf("mixed catalog audiobook quality = %d ok=%v, want untyped format match 12", id, ok)
	}
	if id, ok := selectBookMetadataProfile([]chaptarr.MetadataProfile{
		{ID: 21, Name: "Ebook Standard", ProfileType: "2"},
		{ID: 22, Name: "Audiobook"},
	}, BookFormatAudiobook); !ok || id != 22 {
		t.Fatalf("mixed catalog audiobook metadata = %d ok=%v, want untyped format match 22", id, ok)
	}

	if _, ok := bookConfigFromAuthor(&chaptarr.Author{
		ID:                         2101,
		QualityProfileID:           90,
		MetadataProfileID:          91,
		Path:                       "/legacy/books",
		EbookQualityProfileID:      11,
		AudiobookQualityProfileID:  12,
		EbookMetadataProfileID:     21,
		AudiobookMetadataProfileID: 22,
		EbookRootFolderPath:        "/library/ebooks",
		// A current per-format shape must not borrow the legacy path when one
		// current field is incomplete.
		AudiobookRootFolderPath: "",
	}); ok {
		t.Fatal("incomplete per-format author configuration used legacy fallback")
	}
}

func TestChaptarr0720RootSelectionUsesNamesAndEffectiveDefaults(t *testing.T) {
	t.Run("names with nested settings", func(t *testing.T) {
		var roots []chaptarr.RootFolder
		if err := json.Unmarshal([]byte(`[
			{
				"id":41,
				"name":"Ebooks",
				"path":"/volume/one",
				"ebook":{"writeMetadata":false},
				"audiobook":{"writeMetadata":false}
			},
			{
				"id":42,
				"name":"Audiobooks",
				"path":"/volume/two",
				"ebook":{"writeMetadata":false},
				"audiobook":{"writeMetadata":false}
			}
		]`), &roots); err != nil {
			t.Fatalf("decode named roots: %v", err)
		}
		if root, ok := selectBookRoot(roots, BookFormatEbook); !ok || root.Path != "/volume/one" {
			t.Fatalf("ebook root = %+v ok=%v, want named ebook root", root, ok)
		}
		if root, ok := selectBookRoot(roots, BookFormatAudiobook); !ok || root.Path != "/volume/two" {
			t.Fatalf("audiobook root = %+v ok=%v, want named audiobook root", root, ok)
		}
	})

	t.Run("sole legacy generic beside opposite format", func(t *testing.T) {
		roots := []chaptarr.RootFolder{
			{Path: "/library/ebooks", Accessible: true},
			{Path: "/library/shared-books", Accessible: true},
		}
		root, ok := selectBookRoot(roots, BookFormatAudiobook)
		if !ok || root.Path != "/library/shared-books" {
			t.Fatalf("legacy audiobook root = %+v ok=%v, want sole generic root", root, ok)
		}
	})

	t.Run("effective defaults", func(t *testing.T) {
		var roots []chaptarr.RootFolder
		if err := json.Unmarshal([]byte(`[
			{
				"id":51,
				"name":"Primary",
				"path":"/volume/alpha",
				"isEffectiveDefaultEbook":true
			},
			{
				"id":52,
				"name":"Secondary",
				"path":"/volume/beta",
				"isEffectiveDefaultAudiobook":true
			}
		]`), &roots); err != nil {
			t.Fatalf("decode effective-default roots: %v", err)
		}
		if root, ok := selectBookRoot(roots, BookFormatEbook); !ok || root.Path != "/volume/alpha" {
			t.Fatalf("ebook root = %+v ok=%v, want effective ebook default", root, ok)
		}
		if root, ok := selectBookRoot(roots, BookFormatAudiobook); !ok || root.Path != "/volume/beta" {
			t.Fatalf("audiobook root = %+v ok=%v, want effective audiobook default", root, ok)
		}
	})
}

func TestApproveNewAudiobookUsesChaptarr0720PerFormatConfiguration(t *testing.T) {
	fake := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	upstream := httptest.NewServer(fake)
	t.Cleanup(upstream.Close)

	svc, requesterID := newChaptarrBookTestService(t, upstream.URL)
	requireApproval(t, svc)
	adminID := createTestAdmin(t, svc)

	created, err := svc.CreateMediaRequest(requesterID, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "hc:work-1001",
		Title:      "The Clockwork Orchard",
		BookFormat: BookFormatAudiobook,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if created.Status != StatusPending {
		t.Fatalf("created status = %q, want %q", created.Status, StatusPending)
	}

	pending, err := svc.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want one request", pending, err)
	}
	approved, err := svc.ApproveRequest(adminID, pending[0].ID, nil)
	if err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}
	if approved.Status != StatusRequested {
		t.Fatalf("approved status = %q, want %q", approved.Status, StatusRequested)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.seedBodies) != 1 {
		t.Fatal("Chaptarr add body was not captured")
	}
	addBody := fake.seedBodies[0]

	assertJSONFields(t, "book", addBody, map[string]any{
		"mediaType":                  BookFormatAudiobook,
		"rootFolderPath":             "/library/audiobooks",
		"ebookQualityProfileId":      float64(11),
		"audiobookQualityProfileId":  float64(12),
		"ebookMetadataProfileId":     float64(21),
		"audiobookMetadataProfileId": float64(22),
	})

	author, ok := addBody["author"].(map[string]any)
	if !ok {
		t.Fatalf("author = %#v, want object", addBody["author"])
	}
	assertJSONFields(t, "author", author, map[string]any{
		"qualityProfileId":           float64(12),
		"metadataProfileId":          float64(22),
		"rootFolderPath":             "/library/audiobooks",
		"ebookQualityProfileId":      float64(11),
		"audiobookQualityProfileId":  float64(12),
		"ebookMetadataProfileId":     float64(21),
		"audiobookMetadataProfileId": float64(22),
		"ebookRootFolderPath":        "/library/ebooks",
		"audiobookRootFolderPath":    "/library/audiobooks",
		"ebookMonitorFuture":         false,
		"audiobookMonitorFuture":     true,
	})
}

func TestApproveAudiobookBesideExistingEbookUsesPerFormatAuthorConfiguration(t *testing.T) {
	fake := newVerifiedBookUpstream("The Clockwork Orchard", "hc:work-1001")
	fake.author.EbookMonitorFuture = true
	fake.addExisting(BookFormatEbook, true)
	upstream := httptest.NewServer(fake)
	t.Cleanup(upstream.Close)

	svc, requesterID := newChaptarrBookTestService(t, upstream.URL)
	requireApproval(t, svc)
	adminID := createTestAdmin(t, svc)

	if _, err := svc.CreateMediaRequest(requesterID, &CreateRequest{
		MediaType:  "book",
		ForeignID:  "hc:work-1001",
		Title:      "The Clockwork Orchard",
		BookFormat: BookFormatAudiobook,
	}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	pending, err := svc.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("ListPending = %+v err=%v, want one audiobook request", pending, err)
	}
	approved, err := svc.ApproveRequest(adminID, pending[0].ID, nil)
	if err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}
	if approved.Status != StatusRequested {
		t.Fatalf("approved status = %q, want %q", approved.Status, StatusRequested)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.seedBodies) != 1 {
		t.Fatal("Chaptarr sibling-format add body was not captured")
	}
	addBody := fake.seedBodies[0]

	assertJSONFields(t, "book", addBody, map[string]any{
		"authorId":                   float64(2101),
		"mediaType":                  BookFormatAudiobook,
		"rootFolderPath":             "/library/audiobooks",
		"ebookQualityProfileId":      float64(11),
		"audiobookQualityProfileId":  float64(12),
		"ebookMetadataProfileId":     float64(21),
		"audiobookMetadataProfileId": float64(22),
	})

	author, ok := addBody["author"].(map[string]any)
	if !ok {
		t.Fatalf("author = %#v, want object", addBody["author"])
	}
	assertJSONFields(t, "author", author, map[string]any{
		"id":                         float64(2101),
		"qualityProfileId":           float64(12),
		"metadataProfileId":          float64(22),
		"rootFolderPath":             "/library/audiobooks",
		"ebookQualityProfileId":      float64(11),
		"audiobookQualityProfileId":  float64(12),
		"ebookMetadataProfileId":     float64(21),
		"audiobookMetadataProfileId": float64(22),
		"ebookRootFolderPath":        "/library/ebooks",
		"audiobookRootFolderPath":    "/library/audiobooks",
		"ebookMonitorFuture":         true,
		"audiobookMonitorFuture":     true,
	})
}

func assertJSONFields(t *testing.T, label string, got map[string]any, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s.%s = %#v, want %#v", label, key, got[key], value)
		}
	}
}
