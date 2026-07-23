package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

const (
	bookSelectionIdentityLimit = 256
	bookSelectionTextLimit     = 512
)

// normalizeBookSelection validates and copies the stable external identity
// supplied by the requester. Endpoint-local Chaptarr integers are not part of
// this contract, so a persisted selection remains meaningful after a restart
// or an instance repoint.
func normalizeBookSelection(input *BookSelection, bookFormat string) (*BookSelection, error) {
	if input == nil {
		return nil, nil
	}
	selection := &BookSelection{
		LookupTerm:           strings.TrimSpace(input.LookupTerm),
		CatalogForeignBookID: strings.TrimSpace(input.CatalogForeignBookID),
		ForeignAuthorID:      strings.TrimSpace(input.ForeignAuthorID),
		AuthorName:           strings.TrimSpace(input.AuthorName),
	}
	if len(selection.CatalogForeignBookID) > bookSelectionIdentityLimit ||
		len(selection.ForeignAuthorID) > bookSelectionIdentityLimit ||
		len(selection.AuthorName) > bookSelectionTextLimit ||
		len(selection.LookupTerm) > bookSelectionTextLimit {
		return nil, ErrBookSelectionInvalid
	}
	var err error
	if input.Ebook != nil {
		if !bookFormatIncludes(bookFormat, BookFormatEbook) {
			return nil, ErrBookSelectionInvalid
		}
		selection.Ebook, err = normalizeBookPublicationSelection(input.Ebook)
		if err != nil {
			return nil, err
		}
	}
	if input.Audiobook != nil {
		if !bookFormatIncludes(bookFormat, BookFormatAudiobook) {
			return nil, ErrBookSelectionInvalid
		}
		selection.Audiobook, err = normalizeBookPublicationSelection(input.Audiobook)
		if err != nil {
			return nil, err
		}
	}
	if selection.LookupTerm == "" && selection.CatalogForeignBookID == "" && selection.ForeignAuthorID == "" && selection.AuthorName == "" && selection.Ebook == nil && selection.Audiobook == nil {
		return nil, ErrBookSelectionInvalid
	}
	return selection, nil
}

func normalizeBookPublicationSelection(input *BookPublicationSelection) (*BookPublicationSelection, error) {
	if input == nil {
		return nil, nil
	}
	selection := &BookPublicationSelection{
		ForeignEditionID: strings.TrimSpace(input.ForeignEditionID),
		ISBN13:           strings.TrimSpace(input.ISBN13),
		ASIN:             strings.TrimSpace(input.ASIN),
		EditionTitle:     strings.TrimSpace(input.EditionTitle),
		Publisher:        strings.TrimSpace(input.Publisher),
		Language:         strings.TrimSpace(input.Language),
		Year:             input.Year,
		PageCount:        input.PageCount,
	}
	for _, value := range []string{selection.ForeignEditionID, selection.ISBN13, selection.ASIN, selection.Language} {
		if len(value) > bookSelectionIdentityLimit {
			return nil, ErrBookSelectionInvalid
		}
	}
	for _, value := range []string{selection.EditionTitle, selection.Publisher} {
		if len(value) > bookSelectionTextLimit {
			return nil, ErrBookSelectionInvalid
		}
	}
	if selection.Year < 0 || selection.Year > 9999 || selection.PageCount < 0 || selection.PageCount > 10_000_000 {
		return nil, ErrBookSelectionInvalid
	}
	if !bookPublicationSelectionHasEvidence(selection) {
		return nil, ErrBookSelectionInvalid
	}
	return selection, nil
}

func bookPublicationSelectionHasEvidence(selection *BookPublicationSelection) bool {
	return selection != nil && (selection.ForeignEditionID != "" || selection.ISBN13 != "" || selection.ASIN != "" ||
		selection.EditionTitle != "" || selection.Publisher != "" || selection.Language != "" ||
		selection.Year > 0 || selection.PageCount > 0)
}

func bookSelectionForFormat(selection *BookSelection, format string) *BookSelection {
	if selection == nil {
		return nil
	}
	result := &BookSelection{
		LookupTerm:           selection.LookupTerm,
		CatalogForeignBookID: selection.CatalogForeignBookID,
		ForeignAuthorID:      selection.ForeignAuthorID,
		AuthorName:           selection.AuthorName,
	}
	if format == BookFormatBoth || format == BookFormatEbook {
		result.Ebook = selection.Ebook
	}
	if format == BookFormatBoth || format == BookFormatAudiobook {
		result.Audiobook = selection.Audiobook
	}
	return result
}

func encodeBookSelection(selection *BookSelection, format string) (string, error) {
	if selection == nil {
		return "", nil
	}
	sliced := bookSelectionForFormat(selection, format)
	data, err := json.Marshal(sliced)
	if err != nil {
		return "", fmt.Errorf("encode selected book publication: %w", err)
	}
	return string(data), nil
}

func decodeBookSelection(raw, format string) (*BookSelection, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var selection BookSelection
	if err := json.Unmarshal([]byte(raw), &selection); err != nil {
		return nil, fmt.Errorf("decode selected book publication: %w", err)
	}
	normalized, err := normalizeBookSelection(&selection, format)
	if err != nil {
		return nil, fmt.Errorf("decode selected book publication: %w", err)
	}
	return normalized, nil
}

func bookSelectionsEquivalent(left, right *BookSelection, format string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if strings.TrimSpace(left.CatalogForeignBookID) != strings.TrimSpace(right.CatalogForeignBookID) {
		return false
	}
	leftAuthor := strings.TrimSpace(left.ForeignAuthorID)
	rightAuthor := strings.TrimSpace(right.ForeignAuthorID)
	if leftAuthor != "" && rightAuthor != "" {
		if leftAuthor != rightAuthor {
			return false
		}
	} else if normalizeBookIdentity(left.AuthorName) != normalizeBookIdentity(right.AuthorName) {
		return false
	}
	for _, concrete := range expandBookFormat(format) {
		if bookPublicationIdentityKey(left.publication(concrete)) != bookPublicationIdentityKey(right.publication(concrete)) {
			return false
		}
	}
	return true
}

func bookPublicationIdentityKey(selection *BookPublicationSelection) string {
	if selection == nil {
		return ""
	}
	if selection.ForeignEditionID != "" {
		return "foreign:" + strings.TrimSpace(selection.ForeignEditionID)
	}
	if selection.ISBN13 != "" {
		return "isbn:" + normalizedPublicationCode(selection.ISBN13)
	}
	if selection.ASIN != "" {
		return "asin:" + strings.ToUpper(strings.TrimSpace(selection.ASIN))
	}
	return fmt.Sprintf("description:%s|%s|%s|%d|%d",
		normalizeBookIdentity(selection.EditionTitle), normalizeBookIdentity(selection.Publisher),
		normalizeBookIdentity(selection.Language), selection.Year, selection.PageCount)
}

func normalizedPublicationCode(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToUpper(r)
		}
		return -1
	}, value)
}

// bookPublicationMatchesEdition uses the strongest stable selector available.
// Descriptive facts are the fallback only when the provider supplied no
// edition, ISBN, or ASIN identity. That keeps harmless metadata corrections
// from redirecting an explicit external edition ID while still failing closed
// for genuinely ambiguous descriptive-only choices.
func bookPublicationMatchesEdition(selection *BookPublicationSelection, edition chaptarr.Edition, book chaptarr.Book) bool {
	if selection == nil {
		return true
	}
	if selection.ForeignEditionID != "" {
		return strings.TrimSpace(edition.ForeignEditionID) == selection.ForeignEditionID
	}
	if selection.ISBN13 != "" {
		return normalizedPublicationCode(edition.ISBN13) == normalizedPublicationCode(selection.ISBN13)
	}
	if selection.ASIN != "" {
		return strings.EqualFold(strings.TrimSpace(edition.ASIN), selection.ASIN)
	}
	if selection.EditionTitle != "" && normalizeBookIdentity(edition.Title) != normalizeBookIdentity(selection.EditionTitle) {
		return false
	}
	if selection.Publisher != "" && normalizeBookIdentity(edition.Publisher) != normalizeBookIdentity(selection.Publisher) {
		return false
	}
	if selection.Language != "" && normalizeBookIdentity(edition.Language) != normalizeBookIdentity(selection.Language) {
		return false
	}
	if selection.Year > 0 {
		if book.ReleaseDate == nil || book.ReleaseDate.Year() != selection.Year {
			return false
		}
	}
	if selection.PageCount > 0 {
		pageCount := edition.PageCount
		if pageCount == 0 {
			pageCount = book.PageCount
		}
		if pageCount != selection.PageCount {
			return false
		}
	}
	return true
}

func bookPublicationHasStableIdentity(selection *BookPublicationSelection) bool {
	return selection != nil && (selection.ForeignEditionID != "" || selection.ISBN13 != "" || selection.ASIN != "")
}

func filterChaptarrEditionsForPublication(editions []chaptarr.Edition, book chaptarr.Book, selection *BookPublicationSelection) ([]chaptarr.Edition, error) {
	if selection == nil {
		return editions, nil
	}
	matches := make([]chaptarr.Edition, 0, 1)
	for _, edition := range editions {
		if bookPublicationMatchesEdition(selection, edition, book) {
			matches = append(matches, edition)
		}
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("%w: selected publication is ambiguous", ErrBookMatchNotFound)
	}
	return matches, nil
}

// authoritativeParentEditionID returns one otherwise format-less local edition
// only when Chaptarr's authoritative parent book declares the requested medium
// and points back to that exact edition. Lookup isEbook hints are deliberately
// ignored, as is any explicit non-target (for example physical) child format.
func authoritativeParentEditionID(book chaptarr.Book, editions []chaptarr.Edition, mediaType string) int {
	if strictChaptarrFormat(book.MediaType) != mediaType {
		return 0
	}
	foreignEditionID := strings.TrimSpace(book.ForeignEditionID)
	if foreignEditionID == "" {
		return 0
	}
	matchedRelationships := 0
	formatlessEditionID := 0
	for _, edition := range editions {
		if strings.TrimSpace(edition.ForeignEditionID) != foreignEditionID {
			continue
		}
		matchedRelationships++
		if edition.ID > 0 && strings.TrimSpace(edition.Format) == "" {
			formatlessEditionID = edition.ID
		}
	}
	if matchedRelationships != 1 {
		return 0
	}
	return formatlessEditionID
}

func chaptarrEditionMatchesTargetFormat(edition chaptarr.Edition, mediaType string, authoritativeEditionID int) bool {
	if format := chaptarrEditionFormat(edition); format != "" {
		return format == mediaType
	}
	return authoritativeEditionID > 0 && edition.ID == authoritativeEditionID && strings.TrimSpace(edition.Format) == ""
}

func selectChaptarrEditionsForTarget(editions []chaptarr.Edition, book chaptarr.Book, target chaptarrBookTarget) ([]chaptarr.Edition, int, bool, error) {
	authoritativeEditionID := authoritativeParentEditionID(book, editions, target.mediaType)
	if target.publication == nil {
		matching, tier, multiWork := selectChaptarrEditionsWithAuthoritativeParent(editions, target.title, target.mediaType, authoritativeEditionID)
		return matching, tier, multiWork, nil
	}
	// An edition title is a publication label, not necessarily the parent work
	// title (for example, "Unabridged Edition"). Scope an explicit publication
	// selector to the already resolved author/work row, then let its stable
	// edition, ISBN, or ASIN identity decide the publication match. This keeps an
	// exact selector useful without allowing the same identifier on an unrelated
	// parent row to cross the work boundary.
	if chaptarrBookIdentityTier(book, target) == 0 {
		return nil, 0, false, nil
	}
	stableIdentity := bookPublicationHasStableIdentity(target.publication)
	matches := make([]chaptarr.Edition, 0, 1)
	matchTier := 0
	for _, edition := range editions {
		if edition.ID <= 0 || !chaptarrEditionMatchesTargetFormat(edition, target.mediaType, authoritativeEditionID) {
			continue
		}
		if !bookPublicationMatchesEdition(target.publication, edition, book) {
			continue
		}
		tier := 4 // Stable publication identity outranks title-derived compatibility.
		if !stableIdentity {
			tier = 0
			if strings.TrimSpace(edition.Title) == "" {
				tier = 1
			} else if titleTier := bookTitleIdentityTier(edition.Title, target.title); titleTier > 0 {
				tier = titleTier + 1
			}
			if tier == 0 {
				continue
			}
		}
		if chaptarrTitleIsMultiWork(edition.Title) {
			return nil, tier, true, ErrBookMultiWorkUnsupported
		}
		matches = append(matches, edition)
		matchTier = tier
	}
	if len(matches) > 1 {
		return nil, 0, false, fmt.Errorf("%w: selected publication is ambiguous", ErrBookMatchNotFound)
	}
	return matches, matchTier, false, nil
}

func decodeChaptarrLookupEditions(raw []json.RawMessage) ([]chaptarr.Edition, error) {
	editions := make([]chaptarr.Edition, 0, len(raw))
	for _, encoded := range raw {
		var edition chaptarr.Edition
		if err := json.Unmarshal(encoded, &edition); err != nil {
			return nil, fmt.Errorf("decode lookup publication: %w", err)
		}
		editions = append(editions, edition)
	}
	return editions, nil
}

func lookupResultMatchesPublication(result chaptarr.LookupResult, format string, selection *BookPublicationSelection) (bool, error) {
	if selection == nil {
		return true, nil
	}
	editions, err := decodeChaptarrLookupEditions(result.Editions)
	if err != nil {
		return false, err
	}
	lookupBook := chaptarr.Book{Title: result.Title, PageCount: result.PageCount}
	// Publication year is a fallback selector only. Constructing a local
	// release date here would add a timezone concern, so compare it directly.
	if selection.ForeignEditionID == "" && selection.ISBN13 == "" && selection.ASIN == "" && selection.Year > 0 && result.Year != selection.Year {
		return false, nil
	}
	matches := 0
	for _, edition := range editions {
		if chaptarrEditionFormat(edition) != format {
			continue
		}
		candidateSelection := *selection
		candidateSelection.Year = 0
		if bookPublicationMatchesEdition(&candidateSelection, edition, lookupBook) {
			matches++
		}
	}
	// This Chaptarr fork can return an authoritative top-level medium and
	// edition identity together with one otherwise format-less child edition.
	// Accept that exact parent-child relationship just as the later local-row
	// verifier does. An isEbook hint alone is not authoritative, and an
	// explicitly conflicting or duplicate child still fails closed.
	if matches == 0 && len(editions) == 1 &&
		strictChaptarrFormat(result.MediaType) == format &&
		selection.ForeignEditionID != "" &&
		strings.TrimSpace(result.ForeignEditionID) == selection.ForeignEditionID {
		edition := editions[0]
		if strings.TrimSpace(edition.Format) == "" &&
			strings.TrimSpace(edition.ForeignEditionID) == strings.TrimSpace(result.ForeignEditionID) {
			candidateSelection := *selection
			candidateSelection.Year = 0
			if bookPublicationMatchesEdition(&candidateSelection, edition, lookupBook) {
				matches++
			}
		}
	}
	if len(editions) == 0 && strictChaptarrFormat(result.MediaType) == format {
		candidateSelection := *selection
		candidateSelection.Year = 0
		synthetic := chaptarr.Edition{
			ForeignEditionID: result.ForeignEditionID,
			Title:            result.Title,
			Format:           result.MediaType,
			PageCount:        result.PageCount,
		}
		if bookPublicationMatchesEdition(&candidateSelection, synthetic, lookupBook) {
			matches++
		}
	}
	if matches > 1 {
		return false, fmt.Errorf("%w: selected lookup publication is ambiguous", ErrBookMatchNotFound)
	}
	return matches == 1, nil
}

func bookSelectionMatchesAuthorIdentity(selection *BookSelection, foreignAuthorID, authorName string) bool {
	if selection == nil || (selection.ForeignAuthorID == "" && selection.AuthorName == "") {
		return true
	}
	if selection.ForeignAuthorID != "" && strings.TrimSpace(foreignAuthorID) == selection.ForeignAuthorID {
		return true
	}
	return selection.AuthorName != "" && normalizeBookIdentity(authorName) == normalizeBookIdentity(selection.AuthorName)
}

func (s *Service) selectChaptarrLibraryWorkForRequest(ctx context.Context, client *chaptarr.Client, books []chaptarr.Book, foreignBookID, selectedTitle string, selection *BookSelection) (string, []chaptarr.Book, error) {
	if selection == nil || (selection.ForeignAuthorID == "" && selection.AuthorName == "") {
		return selectChaptarrLibraryWorkWithSelection(books, foreignBookID, selectedTitle, selection)
	}
	hydrated := append([]chaptarr.Book(nil), books...)
	for i := range hydrated {
		book := &hydrated[i]
		if book.ForeignBookID != foreignBookID || bookTitleIdentityTier(book.Title, selectedTitle) == 0 {
			continue
		}
		authorID := book.AuthorID
		if authorID == 0 && book.Author != nil {
			authorID = book.Author.ID
		}
		if authorID <= 0 {
			continue
		}
		if book.Author != nil && book.Author.AuthorName != "" && book.Author.ForeignAuthorID != "" {
			continue
		}
		author, err := client.GetAuthorContext(ctx, authorID)
		if err != nil {
			return "", nil, fmt.Errorf("resolve selected library author: %w", err)
		}
		book.Author = &chaptarr.AuthorContext{
			ID: author.ID, AuthorName: author.AuthorName, ForeignAuthorID: author.ForeignAuthorID,
		}
	}
	return selectChaptarrLibraryWorkWithSelection(hydrated, foreignBookID, selectedTitle, selection)
}

func bookSelectionFromTarget(target chaptarrBookTarget) *BookSelection {
	if !target.explicitSelection {
		return nil
	}
	if target.selection != nil {
		return target.selection
	}
	selection := &BookSelection{ForeignAuthorID: target.foreignAuthorID, AuthorName: target.authorName}
	if target.mediaType == BookFormatAudiobook {
		selection.Audiobook = target.publication
	} else {
		selection.Ebook = target.publication
	}
	return selection
}

func chaptarrBookTargetForResolvedRequest(r *resolvedRequest, authorID int, title, mediaType string) chaptarrBookTarget {
	target := chaptarrBookTarget{
		jobID: r.bookJobID, authorID: authorID, foreignBookID: r.foreignID,
		title: title, mediaType: mediaType,
		publication:       r.bookSelection.publication(mediaType),
		explicitSelection: r.bookSelection != nil,
		selection:         bookSelectionForFormat(r.bookSelection, mediaType),
	}
	if r.bookSelection != nil {
		target.foreignAuthorID = r.bookSelection.ForeignAuthorID
		target.authorName = r.bookSelection.AuthorName
	}
	return target
}

func requireDecodedBookSelection(raw, format string) (*BookSelection, error) {
	selection, err := decodeBookSelection(raw, format)
	if err != nil {
		if errors.Is(err, ErrBookSelectionInvalid) {
			return nil, err
		}
		return nil, fmt.Errorf("invalid stored book selection: %w", err)
	}
	return selection, nil
}
