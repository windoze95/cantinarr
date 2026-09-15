package request

import (
	"context"
	"sort"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// ListeningLibraryRevision authorizes the same explicit instance as book
// detail, then binds the lookup to its current connection. Metadata-only
// catalog browsing is not library access, including for kids accounts.
func (s *Service) ListeningLibraryRevision(userID int64, instanceID string) (instance.ArrSettingsFingerprint, error) {
	var zero instance.ArrSettingsFingerprint
	if instanceID == "" {
		return zero, mediaserver.ErrBookAccess
	}
	client, _, err := s.resolveChaptarr(userID, instanceID)
	if err != nil || client == nil {
		return zero, mediaserver.ErrBookAccess
	}
	_, revision, err := s.registry.GetFreshChaptarrClient(instanceID)
	if err != nil {
		return zero, mediaserver.ErrBookAccess
	}
	return revision, nil
}

// ResolveListeningBook uses only native library identity. The detail page has
// already resolved cross-catalog identities through book-status; this route
// never guesses from a title or from client-supplied ISBNs.
func (s *Service) ResolveListeningBook(ctx context.Context, userID int64, instanceID, foreignID string) (mediaserver.BookQuery, error) {
	q := mediaserver.BookQuery{}
	if _, err := s.ListeningLibraryRevision(userID, instanceID); err != nil {
		return q, err
	}
	client, _, err := s.registry.GetFreshChaptarrClient(instanceID)
	if err != nil {
		return q, err
	}
	var records []chaptarr.Book
	if projection, ok := s.cachedBookProjection("book-live:" + instanceID); ok {
		// The 15-second projection is only an index. Confirm each candidate
		// record's identity, format and files directly before collecting IDs.
		ids := []int{}
		for id, rec := range projection.Records {
			if rec.ForeignID == foreignID && rec.Format == BookFormatAudiobook {
				ids = append(ids, id)
			}
		}
		sort.Ints(ids)
		for _, id := range ids {
			book, err := client.GetBookContext(ctx, id)
			if err != nil {
				return q, err
			}
			if book != nil {
				records = append(records, *book)
			}
		}
	} else {
		records, err = client.GetAllBooksContext(ctx)
		if err != nil {
			return q, err
		}
	}
	for _, book := range records {
		if book.ID <= 0 || book.ForeignBookID != foreignID || chaptarr.RecordFormat(book) != BookFormatAudiobook || book.Statistics.BookFileCount <= 0 {
			continue
		}
		q.Available = true
		editions := book.Editions
		if len(editions) == 0 {
			editions, err = client.GetBookEditionsContext(ctx, book.ID)
			if err != nil {
				return q, err
			}
		}
		for _, edition := range editions {
			// Explicit ebook editions must not produce an audiobook link.
			if (edition.IsEbook != nil && *edition.IsEbook) || chaptarr.FormatOf(edition.Format) == BookFormatEbook {
				continue
			}
			if edition.ASIN != "" {
				q.ASINs = append(q.ASINs, edition.ASIN)
			}
			for _, isbn := range []string{edition.ISBN10, edition.ISBN13} {
				if value := chaptarr.NormalizeISBN(isbn); value != "" {
					q.ISBNs = append(q.ISBNs, value)
				}
			}
		}
	}
	return q, nil
}
