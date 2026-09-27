package request

import (
	"context"

	"github.com/windoze95/cantinarr-server/internal/arr"
)

// Bind the delivered child to its current native parent. Never choose an
// artist/author by a name, or infer success from a tag existing globally.
func (s *Service) parentRequesterTag(ctx context.Context, r *resolvedRequest, requestID int64, format string) (apply func(string, string, func() error) (string, error), check func() error, err error) {
	var canonical string
	var recordID int
	if err = s.db.QueryRow(`SELECT canonical_foreign_id,book_record_id FROM request_dispatch WHERE request_id=? AND format=? AND state='complete'`, requestID, format).Scan(&canonical, &recordID); err != nil {
		return nil, nil, errRequesterTagStopped
	}
	identity := canonical
	if identity == "" {
		identity = r.foreignID
	}
	if identity == "" {
		return nil, nil, arr.ErrTagIdentity
	}
	checkBinding := func() error {
		var matches int
		if s.db.QueryRow(`SELECT COUNT(*) FROM request_dispatch WHERE request_id=? AND format=? AND state='complete' AND canonical_foreign_id=? AND book_record_id=?`, requestID, format, canonical, recordID).Scan(&matches) != nil || matches != 1 {
			return errRequesterTagStopped
		}
		return nil
	}
	if r.mediaType == "music" {
		native, fingerprint, e := s.registry.GetFreshLidarrClient(r.instanceID)
		if e != nil {
			return nil, nil, errRequesterTagStopped
		}
		native = native.WithContext(ctx)
		albumID := recordID
		if albumID == 0 {
			albums, e := native.GetAllAlbums()
			if e != nil {
				return nil, nil, e
			}
			for _, album := range albums {
				if album.ForeignAlbumID == identity {
					if albumID != 0 {
						return nil, nil, arr.ErrTagIdentity
					}
					albumID = album.ID
				}
			}
		}
		if albumID <= 0 {
			return nil, nil, arr.ErrTagIdentity
		}
		album, e := native.GetAlbum(albumID)
		if e != nil {
			return nil, nil, e
		}
		if album == nil || album.ID != albumID || album.ForeignAlbumID != identity || album.ArtistID <= 0 {
			return nil, nil, arr.ErrTagIdentity
		}
		artist, e := native.GetArtist(album.ArtistID)
		if e != nil {
			return nil, nil, e
		}
		if artist.ID != album.ArtistID || artist.ForeignArtistID == "" {
			return nil, nil, arr.ErrTagIdentity
		}
		check = func() error {
			_, current, e := s.registry.GetFreshLidarrClient(r.instanceID)
			if e != nil || current != fingerprint {
				return errRequesterTagStopped
			}
			currentAlbum, e := native.GetAlbum(albumID)
			if e != nil {
				return e
			}
			if currentAlbum == nil || currentAlbum.ID != albumID || currentAlbum.ForeignAlbumID != identity || currentAlbum.ArtistID != artist.ID {
				return arr.ErrTagIdentity
			}
			return checkBinding()
		}
		apply = func(prefix, label string, guard func() error) (string, error) {
			return native.RequesterTags().ApplyParent(ctx, artist.ID, artist.ForeignArtistID, "", prefix, label, guard)
		}
		return apply, check, nil
	}
	native, fingerprint, e := s.registry.GetFreshChaptarrClient(r.instanceID)
	if e != nil {
		return nil, nil, errRequesterTagStopped
	}
	native = native.WithContext(ctx)
	bookID := recordID
	if bookID == 0 {
		books, e := native.GetAllBooks()
		if e != nil {
			return nil, nil, e
		}
		for _, book := range books {
			if book.ForeignBookID == identity && recordFormat(book) == format {
				if bookID != 0 {
					return nil, nil, arr.ErrTagIdentity
				}
				bookID = book.ID
			}
		}
	}
	if bookID <= 0 {
		return nil, nil, arr.ErrTagIdentity
	}
	book, e := native.GetBook(bookID)
	if e != nil {
		return nil, nil, e
	}
	if book == nil || book.ID != bookID || book.ForeignBookID != identity || recordFormat(*book) != format || book.AuthorID <= 0 {
		return nil, nil, arr.ErrTagIdentity
	}
	author, e := native.GetAuthor(book.AuthorID)
	if e != nil {
		return nil, nil, e
	}
	if author == nil || author.ID != book.AuthorID || author.ForeignAuthorID == "" {
		return nil, nil, arr.ErrTagIdentity
	}
	check = func() error {
		_, current, e := s.registry.GetFreshChaptarrClient(r.instanceID)
		if e != nil || current != fingerprint {
			return errRequesterTagStopped
		}
		currentBook, e := native.GetBook(bookID)
		if e != nil {
			return e
		}
		if currentBook == nil || currentBook.ID != bookID || currentBook.ForeignBookID != identity || recordFormat(*currentBook) != format || currentBook.AuthorID != author.ID {
			return arr.ErrTagIdentity
		}
		return checkBinding()
	}
	apply = func(prefix, label string, guard func() error) (string, error) {
		return native.RequesterTags().ApplyParent(ctx, author.ID, author.ForeignAuthorID, format, prefix, label, guard)
	}
	return apply, check, nil
}
