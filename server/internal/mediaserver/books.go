package mediaserver

import (
	"context"
	"errors"
)

var ErrBookAccess = errors.New("book library is not available to this user")

// BookQuery contains identifiers from authorized, available Chaptarr audiobook
// records. Titles are never identity evidence. Available is separate from the
// identifiers: a downloaded book with incomplete metadata still gets a generic
// sign-in shortcut, but a pending download does not get a listening action.
type BookQuery struct {
	Available bool
	ASINs     []string
	ISBNs     []string
}

// BookItem is a verified playable copy. Different item IDs remain separate,
// even when their identifiers match. No upstream URLs or paths cross this seam.
type BookItem struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	LibraryName string   `json:"library_name"`
	Narrators   []string `json:"narrators"`
}

// BookFinder verifies identity and the linked account's live permissions. A
// search that cannot establish a match returns ErrItemUnverified, not absence.
type BookFinder interface {
	FindBooks(context.Context, string, BookQuery) ([]BookItem, error)
}
