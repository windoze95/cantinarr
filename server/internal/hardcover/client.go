// Package hardcover talks to Hardcover's GraphQL API (https://hardcover.app)
// with an instance's API or OAuth access token. Hardcover is an internet host:
// every call rides httpx.External().
//
// Hardcover's `books_trending` answers only an ordered list of book ids, so a
// trending row is always two calls: the ids, then one `books` query to
// hydrate them. Its rate limit is small (the headers report a budget of ten
// per window), so callers cache aggressively and never fetch per viewer.
package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
)

// APIURL is Hardcover's GraphQL endpoint.
const APIURL = "https://api.hardcover.app/v1/graphql"

// ErrUnauthorized means Hardcover answered and refused the token.
var ErrUnauthorized = errors.New("Hardcover rejected the credential")
var ErrInsufficientScope = errors.New("Hardcover requires public catalog permission (read:catalog:data)")

// Client runs GraphQL operations against one Hardcover endpoint. It holds no
// token; the caller passes the instance's token per call so one client serves
// every instance.
type Client struct {
	apiURL string
	http   *http.Client
}

// NewClient returns a client for Hardcover's public endpoint.
func NewClient() *Client { return NewClientForURL(APIURL) }

// NewClientForURL points the client at another endpoint (tests).
func NewClientForURL(apiURL string) *Client {
	return &Client{
		apiURL: apiURL,
		http: &http.Client{Transport: httpx.External(), Timeout: 20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

// Book is one hydrated Hardcover book in the shape a discovery row needs.
type Book struct {
	ID             int64    `json:"hardcover_id"`
	ForeignID      string   `json:"foreign_id"` // hc:<id>, the Chaptarr lookup term and identity
	Title          string   `json:"title"`
	Authors        []string `json:"authors"`
	Year           int      `json:"year,omitempty"`
	Rating         float64  `json:"rating,omitempty"`
	RatingsCount   int      `json:"ratings_count,omitempty"`
	ReadersCount   int      `json:"readers_count,omitempty"`
	Description    string   `json:"description,omitempty"`
	ImageURL       string   `json:"image_url,omitempty"`
	SeriesName     string   `json:"series,omitempty"`
	SeriesPosition float64  `json:"series_position,omitempty"`
	// ISBN13s are the default print/ebook/audio editions' ISBN-13s, for
	// matching against a library digest's isbn: identity keys.
	ISBN13s []string `json:"isbn13s,omitempty"`
}

// IdentityKeys are the typed keys a library digest row would share with this
// book: the hc-book key and any ISBN-13s.
func (b Book) IdentityKeys() []string {
	keys := []string{fmt.Sprintf("hc-book:%d", b.ID)}
	for _, isbn := range b.ISBN13s {
		keys = append(keys, "isbn:"+isbn)
	}
	return keys
}

// Trending returns the top `limit` trending books, in Hardcover's order,
// hydrated. Hardcover ids that the hydration query did not return are
// dropped rather than rendered empty.
func (c *Client) Trending(ctx context.Context, token string, limit int) ([]Book, error) {
	if limit <= 0 {
		limit = 20
	}
	var trending struct {
		BooksTrending struct {
			IDs   []int64 `json:"ids"`
			Error string  `json:"error"`
		} `json:"books_trending"`
	}
	if err := c.query(ctx, token, `query CantinarrTrending($limit: Int!) { books_trending(limit: $limit, offset: 0) { ids error } }`,
		map[string]any{"limit": limit}, &trending); err != nil {
		return nil, err
	}
	if trending.BooksTrending.Error != "" {
		// Hardcover said it could not compute the list: blindness, not an
		// empty answer.
		return nil, errors.New("hardcover: could not compute the trending list")
	}
	ids := trending.BooksTrending.IDs
	if len(ids) == 0 {
		return []Book{}, nil
	}
	books, err := c.books(ctx, token, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]Book, len(books))
	for _, b := range books {
		byID[b.ID] = b
	}
	// `books` does not preserve the trending order; the id list does.
	ordered := make([]Book, 0, len(ids))
	for _, id := range ids {
		if b, ok := byID[id]; ok {
			ordered = append(ordered, b)
		}
	}
	return ordered, nil
}

// booksQuery is the exact hydration shape validated against the live API.
const booksQuery = `query CantinarrBooks($ids: [Int!]!) {
  books(where: {id: {_in: $ids}}) {
    id title release_year rating ratings_count users_count description
    image { url }
    contributions { author { name } }
    book_series { position series { name } }
    default_physical_edition { isbn_13 }
    default_ebook_edition { isbn_13 }
    default_audio_edition { isbn_13 }
  }
}`

func (c *Client) books(ctx context.Context, token string, ids []int64) ([]Book, error) {
	type edition struct {
		ISBN13 string `json:"isbn_13"`
	}
	var reply struct {
		Books []struct {
			ID           int64   `json:"id"`
			Title        string  `json:"title"`
			ReleaseYear  int     `json:"release_year"`
			Rating       float64 `json:"rating"`
			RatingsCount int     `json:"ratings_count"`
			UsersCount   int     `json:"users_count"`
			Description  string  `json:"description"`
			Image        *struct {
				URL string `json:"url"`
			} `json:"image"`
			Contributions []struct {
				Author struct {
					Name string `json:"name"`
				} `json:"author"`
			} `json:"contributions"`
			BookSeries []struct {
				Position float64 `json:"position"`
				Series   struct {
					Name string `json:"name"`
				} `json:"series"`
			} `json:"book_series"`
			Physical *edition `json:"default_physical_edition"`
			Ebook    *edition `json:"default_ebook_edition"`
			Audio    *edition `json:"default_audio_edition"`
		} `json:"books"`
	}
	if err := c.query(ctx, token, booksQuery, map[string]any{"ids": ids}, &reply); err != nil {
		return nil, err
	}
	out := make([]Book, 0, len(reply.Books))
	for _, raw := range reply.Books {
		if raw.ID <= 0 || strings.TrimSpace(raw.Title) == "" {
			continue
		}
		b := Book{
			ID:           raw.ID,
			ForeignID:    fmt.Sprintf("hc:%d", raw.ID),
			Title:        strings.TrimSpace(raw.Title),
			Authors:      []string{},
			Year:         raw.ReleaseYear,
			Rating:       raw.Rating,
			RatingsCount: raw.RatingsCount,
			ReadersCount: raw.UsersCount,
			Description:  strings.TrimSpace(raw.Description),
		}
		for _, contribution := range raw.Contributions {
			if name := strings.TrimSpace(contribution.Author.Name); name != "" {
				b.Authors = append(b.Authors, name)
			}
		}
		if raw.Image != nil && strings.HasPrefix(raw.Image.URL, "https://") {
			b.ImageURL = raw.Image.URL
		}
		if len(raw.BookSeries) > 0 {
			b.SeriesName = strings.TrimSpace(raw.BookSeries[0].Series.Name)
			b.SeriesPosition = raw.BookSeries[0].Position
		}
		seen := map[string]bool{}
		for _, ed := range []*edition{raw.Physical, raw.Ebook, raw.Audio} {
			if ed == nil {
				continue
			}
			isbn := strings.ReplaceAll(strings.TrimSpace(ed.ISBN13), "-", "")
			if len(isbn) == 13 && !seen[isbn] {
				seen[isbn] = true
				b.ISBN13s = append(b.ISBN13s, isbn)
			}
		}
		out = append(out, b)
	}
	return out, nil
}

// query posts one GraphQL operation and decodes `data` into out. A 401/403,
// or a 200 carrying GraphQL errors (how Hardcover reports an expired token),
// is ErrUnauthorized; anything else unexpected is a plain error that never
// contains the token.
func (c *Client) query(ctx context.Context, token, operation string, variables map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": operation, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hardcover: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return ErrInsufficientScope
	case resp.StatusCode == http.StatusTooManyRequests:
		return errors.New("hardcover: rate limited")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("hardcover: unexpected status %d", resp.StatusCode)
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("hardcover: invalid response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		for _, problem := range envelope.Errors {
			msg := strings.ToLower(problem.Message)
			code := strings.ToLower(problem.Extensions.Code)
			if strings.Contains(msg, "jwt") || strings.Contains(msg, "unauthorized") || strings.Contains(msg, "authentication") || code == "invalid-jwt" || code == "unauthenticated" {
				return ErrUnauthorized
			}
			if strings.Contains(msg, "scope") || strings.Contains(msg, "permission") || code == "access-denied" || code == "forbidden" || code == "insufficient_scope" {
				return ErrInsufficientScope
			}
		}
		// Provider error text can include submitted values; never echo it.
		return errors.New("hardcover: catalog query failed")
	}
	if len(envelope.Data) == 0 {
		return errors.New("hardcover: empty response")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("hardcover: invalid data: %w", err)
	}
	return nil
}

// VerifyCatalog exercises both operations used by the feed, including the
// hydration fields, without asking for profile (me) or library permissions.
// Empty arrays are valid; a missing/null result is an unreadable response.
func (c *Client) VerifyCatalog(ctx context.Context, token string) error {
	var reply struct {
		Books    json.RawMessage `json:"books"`
		Trending *struct {
			IDs   []int64 `json:"ids"`
			Error string  `json:"error"`
		} `json:"books_trending"`
	}
	const query = `query CantinarrVerify {
  books_trending(limit: 1, offset: 0) { ids error }
  books(limit: 1) {
    id title release_year rating ratings_count users_count description
    image { url } contributions { author { name } }
    book_series { position series { name } }
    default_physical_edition { isbn_13 } default_ebook_edition { isbn_13 } default_audio_edition { isbn_13 }
  }
 }`
	if err := c.query(ctx, token, query, nil, &reply); err != nil {
		return err
	}
	var books []json.RawMessage
	if json.Unmarshal(reply.Books, &books) != nil || books == nil || reply.Trending == nil || reply.Trending.IDs == nil || reply.Trending.Error != "" {
		return errors.New("hardcover: incomplete catalog verification")
	}
	return nil
}
