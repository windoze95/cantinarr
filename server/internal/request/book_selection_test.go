package request

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

func TestBookSelectionCanonicalNormalizationAndValidation(t *testing.T) {
	selection, err := normalizeBookSelection(&BookSelection{
		LookupTerm:           "  haunting Adelin  ",
		CatalogForeignBookID: "  hc:catalog-haunting  ",
		ForeignAuthorID:      "  hc:author-42  ",
		AuthorName:           "  Mara Vale  ",
		Ebook: &BookPublicationSelection{
			ISBN13:       " 978-1-4028-9462-6 ",
			EditionTitle: " First edition ",
			Publisher:    " Lantern Press ",
			Language:     " English ",
			Year:         2024,
			PageCount:    384,
		},
		Audiobook: &BookPublicationSelection{
			ForeignEditionID: " hc:edition-audio-7 ",
			ASIN:             " B0EXACT42 ",
		},
	}, BookFormatBoth)
	if err != nil {
		t.Fatalf("normalizeBookSelection: %v", err)
	}
	if selection.ForeignAuthorID != "hc:author-42" || selection.AuthorName != "Mara Vale" {
		t.Fatalf("normalized author = %#v", selection)
	}
	if selection.LookupTerm != "haunting Adelin" {
		t.Fatalf("normalized lookup term = %q", selection.LookupTerm)
	}
	if selection.CatalogForeignBookID != "hc:catalog-haunting" {
		t.Fatalf("normalized catalog foreign id = %q", selection.CatalogForeignBookID)
	}
	if selection.Ebook == nil || selection.Ebook.ISBN13 != "978-1-4028-9462-6" ||
		selection.Ebook.EditionTitle != "First edition" || selection.Ebook.Publisher != "Lantern Press" ||
		selection.Ebook.Language != "English" {
		t.Fatalf("normalized ebook = %#v", selection.Ebook)
	}
	if selection.Audiobook == nil || selection.Audiobook.ForeignEditionID != "hc:edition-audio-7" || selection.Audiobook.ASIN != "B0EXACT42" {
		t.Fatalf("normalized audiobook = %#v", selection.Audiobook)
	}

	encoded, err := encodeBookSelection(selection, BookFormatAudiobook)
	if err != nil {
		t.Fatalf("encodeBookSelection: %v", err)
	}
	const want = `{"lookup_term":"haunting Adelin","catalog_foreign_book_id":"hc:catalog-haunting","foreign_author_id":"hc:author-42","author_name":"Mara Vale","audiobook":{"foreign_edition_id":"hc:edition-audio-7","asin":"B0EXACT42"}}`
	if encoded != want {
		t.Fatalf("canonical audiobook JSON = %s, want %s", encoded, want)
	}
	decoded, err := decodeBookSelection(encoded, BookFormatAudiobook)
	if err != nil {
		t.Fatalf("decodeBookSelection: %v", err)
	}
	reencoded, err := encodeBookSelection(decoded, BookFormatAudiobook)
	if err != nil || reencoded != encoded {
		t.Fatalf("canonical round trip = %q, %v; want %q", reencoded, err, encoded)
	}

	tooLongIdentity := strings.Repeat("x", bookSelectionIdentityLimit+1)
	tooLongText := strings.Repeat("x", bookSelectionTextLimit+1)
	for _, tc := range []struct {
		name      string
		selection *BookSelection
		format    string
	}{
		{name: "empty selection", selection: &BookSelection{}, format: BookFormatEbook},
		{name: "empty publication", selection: &BookSelection{Ebook: &BookPublicationSelection{}}, format: BookFormatEbook},
		{name: "publication for unrequested format", selection: &BookSelection{Ebook: &BookPublicationSelection{ASIN: "B0WRONG"}}, format: BookFormatAudiobook},
		{name: "negative year", selection: &BookSelection{Ebook: &BookPublicationSelection{Year: -1}}, format: BookFormatEbook},
		{name: "impossible page count", selection: &BookSelection{Audiobook: &BookPublicationSelection{PageCount: 10_000_001}}, format: BookFormatAudiobook},
		{name: "oversized catalog identity", selection: &BookSelection{CatalogForeignBookID: tooLongIdentity}, format: BookFormatEbook},
		{name: "oversized author identity", selection: &BookSelection{ForeignAuthorID: tooLongIdentity}, format: BookFormatEbook},
		{name: "oversized lookup term", selection: &BookSelection{LookupTerm: tooLongText}, format: BookFormatEbook},
		{name: "oversized publication text", selection: &BookSelection{Ebook: &BookPublicationSelection{EditionTitle: tooLongText}}, format: BookFormatEbook},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeBookSelection(tc.selection, tc.format); !errors.Is(err, ErrBookSelectionInvalid) {
				t.Fatalf("normalizeBookSelection error = %v, want ErrBookSelectionInvalid", err)
			}
		})
	}
}

func TestBookSelectionsWithDifferentCatalogWorksAreDistinct(t *testing.T) {
	left := &BookSelection{CatalogForeignBookID: "hc:catalog-one", ForeignAuthorID: "hc:author"}
	right := &BookSelection{CatalogForeignBookID: "hc:catalog-two", ForeignAuthorID: "hc:author"}
	if bookSelectionsEquivalent(left, right, BookFormatAudiobook) {
		t.Fatal("different selected catalog works were treated as one request")
	}
}

func TestBookSelectionAuthorIdentityTreatsAbsentSelectorAsUnconstrained(t *testing.T) {
	locatorOnly, err := normalizeBookSelection(&BookSelection{
		LookupTerm: "haunting Adelin",
	}, BookFormatAudiobook)
	if err != nil {
		t.Fatalf("normalize locator-only selection: %v", err)
	}
	if !bookSelectionMatchesAuthorIdentity(locatorOnly, "hc:author-42", "H.D. Carlton") {
		t.Fatal("locator-only selection unexpectedly constrained the existing author")
	}

	if bookSelectionMatchesAuthorIdentity(
		&BookSelection{ForeignAuthorID: "hc:other-author"},
		"hc:author-42",
		"H.D. Carlton",
	) {
		t.Fatal("mismatched supplied author provider id was accepted")
	}
	if bookSelectionMatchesAuthorIdentity(
		&BookSelection{AuthorName: "Another Author"},
		"hc:author-42",
		"H.D. Carlton",
	) {
		t.Fatal("mismatched supplied author name was accepted")
	}
	if !bookSelectionMatchesAuthorIdentity(
		&BookSelection{AuthorName: "  h.d.   carlton "},
		"hc:author-42",
		"H.D. Carlton",
	) {
		t.Fatal("normalized supplied author name did not match")
	}
}

func TestLookupChaptarrBookResultsPreservesMixedTransportErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		errorTerm string
	}{
		{name: "locator empty then title errors", errorTerm: "Haunting Adeline"},
		{name: "locator errors then title empty", errorTerm: "haunting Adelin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var terms []string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				term := r.URL.Query().Get("term")
				terms = append(terms, term)
				if term == tc.errorTerm {
					http.Error(w, "synthetic lookup failure", http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[]`))
			}))
			defer upstream.Close()

			results, err := lookupChaptarrBookResultsForRequest(
				context.Background(),
				chaptarr.NewClient(upstream.URL, "key"),
				"hc:haunting-adeline",
				"Haunting Adeline",
				&BookSelection{LookupTerm: "haunting Adelin"},
			)
			if err == nil {
				t.Fatalf("lookup returned results %#v without preserving the transport error", results)
			}
			var statusErr *chaptarr.HTTPStatusError
			if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusBadGateway {
				t.Fatalf("lookup error = %v, want Chaptarr 502", err)
			}
			if got, want := strings.Join(terms, "|"), "haunting Adelin|Haunting Adeline"; got != want {
				t.Fatalf("lookup terms = %q, want %q", got, want)
			}
		})
	}
}

func TestSelectedExternalAuthorDisambiguatesOtherwiseIdenticalLookupRows(t *testing.T) {
	edition := func(foreignEditionID string) json.RawMessage {
		t.Helper()
		encoded, err := json.Marshal(chaptarr.Edition{
			ForeignEditionID: foreignEditionID,
			Title:            "The Same Work",
			Format:           "Audiobook",
			ASIN:             "B0SAMEWORK",
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	results := []chaptarr.LookupResult{
		{
			Title: "The Same Work", ForeignBookID: "hc:shared-work",
			Author:   &chaptarr.Author{AuthorName: "First Author", ForeignAuthorID: "hc:author-first"},
			Editions: []json.RawMessage{edition("hc:edition-first")},
		},
		{
			Title: "The Same Work", ForeignBookID: "hc:shared-work",
			Author:   &chaptarr.Author{AuthorName: "Selected Author", ForeignAuthorID: "hc:author-selected"},
			Editions: []json.RawMessage{edition("hc:edition-selected")},
		},
	}
	selection := &BookSelection{
		ForeignAuthorID: "hc:author-selected",
		AuthorName:      "Selected Author",
		Audiobook:       &BookPublicationSelection{ForeignEditionID: "hc:edition-selected"},
	}
	got, err := selectChaptarrLookupResultWithSelection(results, "hc:shared-work", "The Same Work", selection)
	if err != nil {
		t.Fatalf("selectChaptarrLookupResultWithSelection: %v", err)
	}
	if got == nil || got.Author == nil || got.Author.ForeignAuthorID != "hc:author-selected" {
		t.Fatalf("selected lookup = %#v, want selected external author", got)
	}
}

func TestVerifiedBookRequestReplaysTheLookupTermThatFoundTheWork(t *testing.T) {
	lookupResult := func(upstream *verifiedBookUpstream, foreignBookID string) map[string]any {
		return map[string]any{
			"title":         upstream.title,
			"foreignBookId": foreignBookID,
			"author": map[string]any{
				"authorName":      upstream.authorName,
				"foreignAuthorId": upstream.foreignAuthorID,
			},
			// This is the real discovery shape that must remain requestable.
			// `isEbook` can help presentation, but it is not authoritative
			// publication identity and is deliberately absent from the selection.
			"editions": []map[string]any{{
				"foreignEditionId": "lookup-only",
				"isEbook":          false,
				"format":           nil,
			}},
		}
	}

	t.Run("successful discovery term survives full title miss", func(t *testing.T) {
		upstream := newVerifiedBookUpstream(
			"Haunting Adeline (Cat and Mouse, #1)",
			"hc:haunting-adeline",
		)
		upstream.lookupResultsByTerm = map[string][]map[string]any{
			"haunting Adelin": {lookupResult(upstream, upstream.foreignBookID)},
			upstream.title:    {},
		}
		service, userID := newVerifiedMutationService(t, upstream)

		response, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType:  "book",
			ForeignID:  upstream.foreignBookID,
			Title:      upstream.title,
			BookFormat: BookFormatAudiobook,
			BookSelection: &BookSelection{
				LookupTerm:      "haunting Adelin",
				ForeignAuthorID: upstream.foreignAuthorID,
				AuthorName:      upstream.authorName,
			},
		})
		if err != nil {
			t.Fatalf("CreateMediaRequest: %v", err)
		}
		if response.Status != StatusRequested || upstream.searchCalls != 1 {
			t.Fatalf("response=%#v searches=%d, want one requested audiobook", response, upstream.searchCalls)
		}
		if len(upstream.lookupTerms) == 0 || upstream.lookupTerms[0] != "haunting Adelin" {
			t.Fatalf("lookup terms = %#v, want discovery term first", upstream.lookupTerms)
		}
	})

	t.Run("locator cannot substitute another foreign work", func(t *testing.T) {
		upstream := newVerifiedBookUpstream(
			"Haunting Adeline (Cat and Mouse, #1)",
			"hc:haunting-adeline",
		)
		upstream.lookupResultsByTerm = map[string][]map[string]any{
			"haunting Adelin": {lookupResult(upstream, "hc:different-work")},
			upstream.title:    {},
		}
		service, userID := newVerifiedMutationService(t, upstream)

		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType:  "book",
			ForeignID:  upstream.foreignBookID,
			Title:      upstream.title,
			BookFormat: BookFormatAudiobook,
			BookSelection: &BookSelection{
				LookupTerm:      "haunting Adelin",
				ForeignAuthorID: upstream.foreignAuthorID,
				AuthorName:      upstream.authorName,
			},
		})
		if !errors.Is(err, ErrBookMatchNotFound) {
			t.Fatalf("CreateMediaRequest error = %v, want ErrBookMatchNotFound", err)
		}
		if len(upstream.seedBodies) != 0 || upstream.searchCalls != 0 {
			t.Fatalf("wrong locator result mutated Chaptarr: seed=%d search=%d", len(upstream.seedBodies), upstream.searchCalls)
		}
	})
}

func TestVerifiedBookRequestKeepsCanonicalMutationIdentityAndCatalogLookupIdentity(t *testing.T) {
	const (
		canonicalID = "hc:library-haunting"
		catalogID   = "hc:catalog-haunting"
		lookupTerm  = "haunting Adelin"
	)
	lookupResult := func(upstream *verifiedBookUpstream, foreignBookID, authorID string) map[string]any {
		return map[string]any{
			"title":         upstream.title,
			"titleSlug":     fallbackTitleSlug(upstream.title),
			"foreignBookId": foreignBookID,
			"author": map[string]any{
				"authorName":      upstream.authorName,
				"foreignAuthorId": authorID,
			},
			"editions": []map[string]any{{
				"foreignEditionId": "lookup-only",
				"format":           nil,
				"isEbook":          false,
			}},
		}
	}
	selection := func(upstream *verifiedBookUpstream) *BookSelection {
		return &BookSelection{
			LookupTerm:           lookupTerm,
			CatalogForeignBookID: catalogID,
			ForeignAuthorID:      upstream.foreignAuthorID,
			AuthorName:           upstream.authorName,
		}
	}

	t.Run("anchored missing audiobook mutates canonical sibling once", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("Haunting Adeline (Cat and Mouse, #1)", canonicalID)
		upstream.addExisting(BookFormatEbook, true)
		upstream.formatlessSeedEdition = true
		upstream.lookupResultsByTerm = map[string][]map[string]any{
			lookupTerm:     {lookupResult(upstream, catalogID, upstream.foreignAuthorID)},
			upstream.title: {},
		}
		service, userID := newVerifiedMutationService(t, upstream)

		response, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: canonicalID, Title: upstream.title, BookFormat: BookFormatAudiobook,
			BookSelection: selection(upstream),
		})
		if err != nil {
			t.Fatalf("CreateMediaRequest: %v", err)
		}
		if response.Status != StatusRequested || response.BookFormats[BookFormatAudiobook] != StatusRequested {
			t.Fatalf("response = %#v, want requested audiobook", response)
		}

		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		if len(upstream.seedBodies) != 1 || upstream.seedBodies[0]["foreignBookId"] != canonicalID || upstream.seedBodies[0]["mediaType"] != BookFormatAudiobook {
			t.Fatalf("seed bodies = %#v, want one canonical audiobook sibling", upstream.seedBodies)
		}
		seedEditions, ok := upstream.seedBodies[0]["editions"].([]any)
		if !ok || len(seedEditions) != 1 || seedEditions[0].(map[string]any)["format"] != nil {
			t.Fatalf("seed editions = %#v, want the raw format-null lookup edition", upstream.seedBodies[0]["editions"])
		}
		if len(upstream.lookupTerms) == 0 || upstream.lookupTerms[0] != lookupTerm {
			t.Fatalf("lookup terms = %#v, want selected discovery term first", upstream.lookupTerms)
		}
		if upstream.searchCalls != 1 || len(upstream.searchBookIDs) != 1 {
			t.Fatalf("searches=%d ids=%v, want one authoritative local audiobook", upstream.searchCalls, upstream.searchBookIDs)
		}
		requestedRow := upstream.rows[upstream.searchBookIDs[0]]
		if requestedRow == nil || requestedRow.book.MediaType != BookFormatAudiobook ||
			len(requestedRow.editions) != 1 || requestedRow.editions[0].Format != "" || !requestedRow.editions[0].Monitored {
			t.Fatalf("requested local audiobook = %#v, want monitored authoritative format-null edition", requestedRow)
		}
	})

	t.Run("wrong catalog work cannot borrow the canonical anchor", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("Haunting Adeline (Cat and Mouse, #1)", canonicalID)
		upstream.addExisting(BookFormatEbook, true)
		upstream.lookupResultsByTerm = map[string][]map[string]any{
			lookupTerm:     {lookupResult(upstream, "hc:another-catalog-work", upstream.foreignAuthorID)},
			upstream.title: {},
		}
		service, userID := newVerifiedMutationService(t, upstream)

		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: canonicalID, Title: upstream.title, BookFormat: BookFormatAudiobook,
			BookSelection: selection(upstream),
		})
		if !errors.Is(err, ErrBookMatchNotFound) {
			t.Fatalf("CreateMediaRequest error = %v, want ErrBookMatchNotFound", err)
		}
		if len(upstream.seedBodies) != 0 || len(upstream.bookPutIDs) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
			t.Fatalf("wrong catalog work mutated Chaptarr: seed=%d put=%v monitor=%v search=%d", len(upstream.seedBodies), upstream.bookPutIDs, upstream.monitorIDs, upstream.searchCalls)
		}
	})

	t.Run("differing ids require a live canonical anchor", func(t *testing.T) {
		upstream := newVerifiedBookUpstream("Haunting Adeline (Cat and Mouse, #1)", canonicalID)
		upstream.lookupResultsByTerm = map[string][]map[string]any{
			lookupTerm: {lookupResult(upstream, catalogID, upstream.foreignAuthorID)},
		}
		service, userID := newVerifiedMutationService(t, upstream)
		catalogOnlySelection := selection(upstream)
		catalogOnlySelection.ForeignAuthorID = ""
		catalogOnlySelection.AuthorName = ""

		_, err := service.CreateMediaRequest(userID, &CreateRequest{
			MediaType: "book", ForeignID: canonicalID, Title: upstream.title, BookFormat: BookFormatAudiobook,
			BookSelection: catalogOnlySelection,
		})
		if !errors.Is(err, ErrBookMatchNotFound) {
			t.Fatalf("CreateMediaRequest error = %v, want ErrBookMatchNotFound", err)
		}
		if len(upstream.lookupTerms) != 0 || len(upstream.seedBodies) != 0 || len(upstream.bookPutIDs) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
			t.Fatalf("unanchored selection touched mutation flow: lookup=%v seed=%d put=%v monitor=%v search=%d", upstream.lookupTerms, len(upstream.seedBodies), upstream.bookPutIDs, upstream.monitorIDs, upstream.searchCalls)
		}
	})
}

func TestExactPublicationSelectionUsesParentWorkScopeInsteadOfEditionLabel(t *testing.T) {
	book := chaptarr.Book{
		ID: 17, AuthorID: 23, ForeignBookID: "hc:work-selected",
		Title: "The Selected Work", MediaType: BookFormatAudiobook,
	}
	target := chaptarrBookTarget{
		authorID: 23, foreignBookID: "hc:work-selected",
		title: "The Selected Work", mediaType: BookFormatAudiobook,
	}

	for _, tc := range []struct {
		name      string
		selection *BookPublicationSelection
		edition   chaptarr.Edition
	}{
		{
			name:      "foreign edition id",
			selection: &BookPublicationSelection{ForeignEditionID: "hc:edition-exact"},
			edition:   chaptarr.Edition{ForeignEditionID: "hc:edition-exact"},
		},
		{
			name:      "isbn",
			selection: &BookPublicationSelection{ISBN13: "978-1-4028-9462-6"},
			edition:   chaptarr.Edition{ISBN13: "9781402894626"},
		},
		{
			name:      "asin",
			selection: &BookPublicationSelection{ASIN: "B0EXACT42"},
			edition:   chaptarr.Edition{ASIN: "b0exact42"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testTarget := target
			testTarget.publication = tc.selection
			edition := tc.edition
			edition.ID = 31
			edition.Title = "Unabridged Edition"
			edition.Format = BookFormatAudiobook

			matches, tier, multiWork, err := selectChaptarrEditionsForTarget([]chaptarr.Edition{edition}, book, testTarget)
			if err != nil || multiWork || tier != 4 || len(matches) != 1 || matches[0].ID != edition.ID {
				t.Fatalf("matches=%#v tier=%d multiWork=%v err=%v, want exact labeled publication", matches, tier, multiWork, err)
			}
		})
	}
}

func TestExactPublicationSelectionFailsClosedOutsideSelectedWork(t *testing.T) {
	target := chaptarrBookTarget{
		authorID: 23, foreignBookID: "hc:work-selected", title: "The Selected Work",
		mediaType:   BookFormatAudiobook,
		publication: &BookPublicationSelection{ForeignEditionID: "hc:edition-exact"},
	}
	matchingEdition := chaptarr.Edition{
		ID: 31, ForeignEditionID: "hc:edition-exact", Title: "Unabridged Edition", Format: BookFormatAudiobook,
	}
	selectedBook := chaptarr.Book{
		ID: 17, AuthorID: 23, ForeignBookID: "hc:work-selected",
		Title: "The Selected Work", MediaType: BookFormatAudiobook,
	}

	t.Run("mismatched edition id", func(t *testing.T) {
		edition := matchingEdition
		edition.ForeignEditionID = "hc:edition-unrelated"
		matches, _, _, err := selectChaptarrEditionsForTarget([]chaptarr.Edition{edition}, selectedBook, target)
		if err != nil || len(matches) != 0 {
			t.Fatalf("matches=%#v err=%v, want no mismatched publication", matches, err)
		}
	})

	for _, tc := range []struct {
		name string
		book chaptarr.Book
	}{
		{
			name: "unrelated work",
			book: chaptarr.Book{
				ID: 18, AuthorID: 23, ForeignBookID: "hc:work-unrelated",
				Title: "An Unrelated Work", MediaType: BookFormatAudiobook,
			},
		},
		{
			name: "unrelated author",
			book: chaptarr.Book{
				ID: 19, AuthorID: 99, ForeignBookID: "hc:work-selected",
				Title: "The Selected Work", MediaType: BookFormatAudiobook,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matches, _, _, err := selectChaptarrEditionsForTarget([]chaptarr.Edition{matchingEdition}, tc.book, target)
			if err != nil || len(matches) != 0 {
				t.Fatalf("matches=%#v err=%v, want publication scoped to selected author/work", matches, err)
			}
		})
	}
}

func TestFormatlessLocalEditionRequiresExactAuthoritativeParent(t *testing.T) {
	isEbookFalse := false
	target := chaptarrBookTarget{
		authorID: 23, foreignBookID: "hc:work-selected", title: "The Selected Work",
		mediaType: BookFormatAudiobook,
	}
	book := chaptarr.Book{
		ID: 17, AuthorID: 23, ForeignBookID: "hc:work-selected",
		ForeignEditionID: "hc:edition-exact", Title: "The Selected Work", MediaType: BookFormatAudiobook,
	}
	edition := chaptarr.Edition{
		ID: 31, BookID: 17, ForeignEditionID: "hc:edition-exact",
		Title: "The Selected Work", Format: "", IsEbook: &isEbookFalse,
	}

	matches, _, multiWork, err := selectChaptarrEditionsForTarget([]chaptarr.Edition{edition}, book, target)
	if err != nil || multiWork || len(matches) != 1 || matches[0].ID != edition.ID {
		t.Fatalf("authoritative format-less matches=%#v multiWork=%v err=%v, want exact parent edition", matches, multiWork, err)
	}

	for _, tc := range []struct {
		name     string
		book     chaptarr.Book
		editions []chaptarr.Edition
	}{
		{
			name: "isEbook false without authoritative parent medium",
			book: func() chaptarr.Book {
				candidate := book
				candidate.MediaType = ""
				return candidate
			}(),
			editions: []chaptarr.Edition{edition},
		},
		{
			name: "parent points to another edition",
			book: func() chaptarr.Book {
				candidate := book
				candidate.ForeignEditionID = "hc:edition-other"
				return candidate
			}(),
			editions: []chaptarr.Edition{edition},
		},
		{
			name: "explicit physical child is never inferred",
			book: book,
			editions: []chaptarr.Edition{func() chaptarr.Edition {
				candidate := edition
				candidate.Format = "physical"
				return candidate
			}()},
		},
		{
			name: "duplicate parent relationship is ambiguous",
			book: book,
			editions: []chaptarr.Edition{
				edition,
				func() chaptarr.Edition {
					candidate := edition
					candidate.ID = 32
					return candidate
				}(),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matches, _, _, err := selectChaptarrEditionsForTarget(tc.editions, tc.book, target)
			if err != nil || len(matches) != 0 {
				t.Fatalf("matches=%#v err=%v, want no inferred audiobook edition", matches, err)
			}
		})
	}
}

func TestVerifiedBookSelectionChoosesExactSecondPublication(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection *BookPublicationSelection
	}{
		{name: "foreign edition id", selection: &BookPublicationSelection{ForeignEditionID: "hc:edition-second", ASIN: "B0SECOND"}},
		{name: "asin", selection: &BookPublicationSelection{ASIN: "B0SECOND"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Exact Publication", "hc:exact-publication")
			bookID := upstream.addExisting(BookFormatAudiobook, false)
			upstream.mu.Lock()
			first := upstream.rows[bookID].editions[0]
			first.ForeignEditionID = "hc:edition-first"
			first.ASIN = "B0FIRST"
			first.Title = upstream.title
			second := first
			second.ID++
			second.ForeignEditionID = "hc:edition-second"
			second.ASIN = "B0SECOND"
			second.Title = "Unabridged Edition"
			upstream.rows[bookID].editions = []chaptarr.Edition{first, second}
			upstream.mu.Unlock()

			service, userID := newVerifiedMutationService(t, upstream)
			response, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title,
				BookFormat: BookFormatAudiobook,
				BookSelection: &BookSelection{
					ForeignAuthorID: upstream.foreignAuthorID,
					AuthorName:      upstream.authorName,
					Audiobook:       tc.selection,
				},
			})
			if err != nil {
				t.Fatalf("CreateMediaRequest: %v", err)
			}
			if response.Status != StatusRequested || response.BookFormats[BookFormatAudiobook] != StatusRequested {
				t.Fatalf("response = %#v, want requested audiobook", response)
			}
			var historySelectionJSON string
			if err := service.db.QueryRow(
				`SELECT COALESCE(book_selection_json, '') FROM request_log
				 WHERE user_id = ? AND foreign_id = ? AND book_format = ? AND status = ? ORDER BY id DESC LIMIT 1`,
				userID, upstream.foreignBookID, BookFormatAudiobook, StatusRequested,
			).Scan(&historySelectionJSON); err != nil {
				t.Fatalf("read selected publication history: %v", err)
			}
			historySelection, err := decodeBookSelection(historySelectionJSON, BookFormatAudiobook)
			if err != nil || historySelection == nil || historySelection.Audiobook == nil ||
				bookPublicationIdentityKey(historySelection.Audiobook) != bookPublicationIdentityKey(tc.selection) {
				t.Fatalf("history selection = %#v, %v; want %#v", historySelection, err, tc.selection)
			}

			upstream.mu.Lock()
			defer upstream.mu.Unlock()
			row := upstream.rows[bookID]
			if row == nil || len(row.editions) != 2 {
				t.Fatalf("selected row = %#v", row)
			}
			if row.editions[0].Monitored || row.editions[0].ManualAdd || !row.editions[1].Monitored || !row.editions[1].ManualAdd {
				t.Fatalf("edition selection = %#v, want only second publication monitored", row.editions)
			}
			if len(upstream.bookPutIDs) != 1 || upstream.bookPutIDs[0] != bookID ||
				len(upstream.monitorIDs) != 1 || upstream.monitorIDs[0] != bookID ||
				upstream.searchCalls != 1 || len(upstream.searchBookIDs) != 1 || upstream.searchBookIDs[0] != bookID {
				t.Fatalf("put=%v monitor=%v search=%v calls=%d, want one exact row mutation", upstream.bookPutIDs, upstream.monitorIDs, upstream.searchBookIDs, upstream.searchCalls)
			}
		})
	}
}

func TestVerifiedBookSelectionFailsClosedBeforeBookMutation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection *BookPublicationSelection
		configure func(*verifiedBookUpstream, int)
	}{
		{
			name:      "missing foreign edition",
			selection: &BookPublicationSelection{ForeignEditionID: "hc:edition-missing"},
		},
		{
			name:      "ambiguous asin",
			selection: &BookPublicationSelection{ASIN: "B0DUPLICATE"},
			configure: func(upstream *verifiedBookUpstream, bookID int) {
				first := upstream.rows[bookID].editions[0]
				first.ForeignEditionID = "hc:edition-one"
				first.ASIN = "B0DUPLICATE"
				first.Title = upstream.title
				second := first
				second.ID++
				second.ForeignEditionID = "hc:edition-two"
				upstream.rows[bookID].editions = []chaptarr.Edition{first, second}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVerifiedBookUpstream("Fail Closed Selection", "hc:fail-closed-selection")
			bookID := upstream.addExisting(BookFormatAudiobook, false)
			upstream.mu.Lock()
			if tc.configure != nil {
				tc.configure(upstream, bookID)
			}
			upstream.mu.Unlock()
			service, userID := newVerifiedMutationService(t, upstream)

			_, err := service.CreateMediaRequest(userID, &CreateRequest{
				MediaType: "book", ForeignID: upstream.foreignBookID, Title: upstream.title,
				BookFormat: BookFormatAudiobook,
				BookSelection: &BookSelection{
					ForeignAuthorID: upstream.foreignAuthorID,
					AuthorName:      upstream.authorName,
					Audiobook:       tc.selection,
				},
			})
			if !errors.Is(err, ErrBookMatchNotFound) {
				t.Fatalf("CreateMediaRequest error = %v, want ErrBookMatchNotFound", err)
			}
			upstream.mu.Lock()
			defer upstream.mu.Unlock()
			if len(upstream.seedBodies) != 0 || len(upstream.bookPutIDs) != 0 || len(upstream.monitorIDs) != 0 || upstream.searchCalls != 0 {
				t.Fatalf("stale selection mutated Chaptarr: seed=%d put=%v monitor=%v search=%d", len(upstream.seedBodies), upstream.bookPutIDs, upstream.monitorIDs, upstream.searchCalls)
			}
		})
	}
}

func TestBookSelectionSurvivesPendingApprovalAndDurableJobReload(t *testing.T) {
	upstream := newVerifiedBookUpstream("Durable Selection", "hc:durable-selection")
	service, userID := newVerifiedMutationService(t, upstream)
	_, instanceID, err := service.resolveChaptarr(userID, "")
	if err != nil {
		t.Fatal(err)
	}
	selection, err := normalizeBookSelection(&BookSelection{
		LookupTerm:           "durable select",
		CatalogForeignBookID: "hc:durable-catalog-selection",
		ForeignAuthorID:      upstream.foreignAuthorID,
		AuthorName:           upstream.authorName,
		Audiobook: &BookPublicationSelection{
			ForeignEditionID: "hc:durable-edition", ASIN: "B0DURABLE", Publisher: "Lantern Audio",
		},
	}, BookFormatAudiobook)
	if err != nil {
		t.Fatal(err)
	}
	resolved := &resolvedRequest{
		userID: userID, instanceID: instanceID, foreignID: upstream.foreignBookID,
		title: upstream.title, mediaType: "book", bookFormat: BookFormatAudiobook,
		bookSelection: selection,
	}
	if _, err := service.createPending(resolved); err != nil {
		t.Fatalf("createPending: %v", err)
	}
	var requestID int64
	var pendingJSON string
	if err := service.db.QueryRow(
		"SELECT id, COALESCE(book_selection_json, '') FROM request_log WHERE user_id = ? AND foreign_id = ? AND status = ?",
		userID, upstream.foreignBookID, StatusPending,
	).Scan(&requestID, &pendingJSON); err != nil {
		t.Fatal(err)
	}
	wantJSON, err := encodeBookSelection(selection, BookFormatAudiobook)
	if err != nil {
		t.Fatal(err)
	}
	if pendingJSON != wantJSON {
		t.Fatalf("pending selection JSON = %q, want %q", pendingJSON, wantJSON)
	}
	loadedRequest, status, err := service.loadRequest(requestID)
	if err != nil {
		t.Fatalf("loadRequest: %v", err)
	}
	if status != StatusPending {
		t.Fatalf("loaded approval status = %q, want pending", status)
	}
	loadedJSON, err := encodeBookSelection(loadedRequest.bookSelection, loadedRequest.bookFormat)
	if err != nil || loadedJSON != wantJSON {
		t.Fatalf("loaded approval selection = %q, %v; want %q", loadedJSON, err, wantJSON)
	}
	pending, err := service.ListPending()
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 || pending[0].BookSelection == nil || pending[0].BookSelection.CatalogForeignBookID != "hc:durable-catalog-selection" || pending[0].BookSelection.Audiobook == nil ||
		pending[0].BookSelection.Audiobook.ForeignEditionID != "hc:durable-edition" {
		t.Fatalf("pending approval = %#v", pending)
	}

	adminID := createTestAdmin(t, service)
	job, _, active, err := service.prepareApprovalBookJob(adminID, requestID, loadedRequest)
	if err != nil || active {
		t.Fatalf("prepareApprovalBookJob job=%#v active=%v err=%v", job, active, err)
	}
	if job.BookSelectionJSON != wantJSON {
		t.Fatalf("prepared job selection = %q, want %q", job.BookSelectionJSON, wantJSON)
	}

	restarted := NewService(service.db, service.registry, service.bridge, service.notifier)
	reloadedJob, err := restarted.loadBookRequestJob(job.ID)
	if err != nil {
		t.Fatalf("loadBookRequestJob after restart: %v", err)
	}
	reloadedJSON, err := encodeBookSelection(reloadedJob.BookSelection, reloadedJob.BookFormat)
	if err != nil || reloadedJSON != wantJSON || reloadedJob.RequestID != requestID {
		t.Fatalf("reloaded job selection=%q request=%d err=%v, want %q request=%d", reloadedJSON, reloadedJob.RequestID, err, wantJSON, requestID)
	}
}

func TestLookupPublicationCanUseStableTopLevelEditionIdentity(t *testing.T) {
	result := chaptarr.LookupResult{
		Title: "Top-Level Publication", ForeignBookID: "hc:top-level-book",
		ForeignEditionID: "hc:top-level-edition", MediaType: BookFormatAudiobook,
		Year: 2025, PageCount: 640,
	}
	matched, err := lookupResultMatchesPublication(result, BookFormatAudiobook, &BookPublicationSelection{
		ForeignEditionID: "hc:top-level-edition", Year: 2025, PageCount: 640,
	})
	if err != nil || !matched {
		t.Fatalf("lookupResultMatchesPublication = %v, %v; want exact top-level match", matched, err)
	}
	matched, err = lookupResultMatchesPublication(result, BookFormatEbook, &BookPublicationSelection{
		ForeignEditionID: "hc:top-level-edition",
	})
	if err != nil || matched {
		t.Fatalf("wrong-format top-level match = %v, %v; want false", matched, err)
	}
}

func TestPendingBookSelectionsForSameWorkRemainDistinct(t *testing.T) {
	service, userID := newBookTestService(t)
	first, err := normalizeBookSelection(&BookSelection{
		ForeignAuthorID: "hc:author-shared",
		Audiobook:       &BookPublicationSelection{ForeignEditionID: "hc:edition-one", ASIN: "B0ONE"},
	}, BookFormatAudiobook)
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalizeBookSelection(&BookSelection{
		ForeignAuthorID: "hc:author-shared",
		Audiobook:       &BookPublicationSelection{ForeignEditionID: "hc:edition-two", ASIN: "B0TWO"},
	}, BookFormatAudiobook)
	if err != nil {
		t.Fatal(err)
	}
	for _, selection := range []*BookSelection{first, second} {
		if _, err := service.createPending(&resolvedRequest{
			userID: userID, foreignID: "hc:same-work", title: "Same Work",
			mediaType: "book", bookFormat: BookFormatAudiobook, bookSelection: selection,
		}); err != nil {
			t.Fatalf("createPending: %v", err)
		}
	}

	rows, err := service.db.Query(
		`SELECT COALESCE(book_selection_json, '') FROM request_log
		 WHERE user_id = ? AND foreign_id = ? AND media_type = 'book' AND status = ? ORDER BY id`,
		userID, "hc:same-work", StatusPending,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var selectionJSON string
		if err := rows.Scan(&selectionJSON); err != nil {
			t.Fatal(err)
		}
		got = append(got, selectionJSON)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantFirst, _ := encodeBookSelection(first, BookFormatAudiobook)
	wantSecond, _ := encodeBookSelection(second, BookFormatAudiobook)
	if len(got) != 2 || got[0] != wantFirst || got[1] != wantSecond {
		t.Fatalf("pending selections = %#v, want distinct %#v", got, []string{wantFirst, wantSecond})
	}
	var waiterCount int
	if err := service.db.QueryRow(
		`SELECT COUNT(*) FROM book_request_waiters w
		 JOIN request_log r ON r.id = w.request_id
		 WHERE w.user_id = ? AND r.foreign_id = ? AND r.status = ?`,
		userID, "hc:same-work", StatusPending,
	).Scan(&waiterCount); err != nil {
		t.Fatal(err)
	}
	if waiterCount != 2 {
		t.Fatalf("pending waiter rows = %d, want one per distinct publication", waiterCount)
	}
}
