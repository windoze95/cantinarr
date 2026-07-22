// Package chaptarr is a typed HTTP client for a Chaptarr server, a Readarr fork
// that manages books as both ebooks and audiobooks. It speaks the Servarr
// /api/v1 API and is a structural mirror of the Sonarr client, translating the
// series>season>episode model to Readarr's author>book>edition>bookFile model.
package chaptarr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	arrcommon "github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

// ErrCustomFormatsNotFound reports a 404 from the custom format endpoint. It
// is deliberately not called "unsupported": a fork or build without the
// endpoint and an instance URL missing the service's URL base are
// indistinguishable from here, so callers must present both causes rather
// than diagnose one.
var ErrCustomFormatsNotFound = errors.New("chaptarr: the custom format endpoint returned 404")

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Image is a cover/poster reference returned on authors, books, and editions.
type Image struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
	RemoteURL string `json:"remoteUrl"`
}

// genreList unmarshals a genres value that may be either a JSON array of strings
// (stock Servarr) or a single, possibly comma-separated, string. This Chaptarr
// fork returns genres as a string (e.g. "" or "Science Fiction, Fantasy"), which
// would otherwise fail to decode into a []string and abort the whole response —
// e.g. a successful book add reported as "cannot unmarshal string into Go struct
// field Book.genres of type []string".
type genreList []string

func (g *genreList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*g = nil
		return nil
	}
	if trimmed[0] == '[' {
		var arr []string
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return err
		}
		*g = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return err
	}
	out := make([]string, 0)
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	*g = out
	return nil
}

// AuthorStatistics is the per-author book rollup Chaptarr returns on an author.
type AuthorStatistics struct {
	BookCount          int     `json:"bookCount"`
	BookFileCount      int     `json:"bookFileCount"`
	AvailableBookCount int     `json:"availableBookCount"`
	TotalBookCount     int     `json:"totalBookCount"`
	SizeOnDisk         int64   `json:"sizeOnDisk"`
	PercentOfBooks     float64 `json:"percentOfBooks"`
}

type Author struct {
	ID                         int              `json:"id"`
	AuthorName                 string           `json:"authorName"`
	ForeignAuthorID            string           `json:"foreignAuthorId"`
	TitleSlug                  string           `json:"titleSlug"`
	Overview                   string           `json:"overview"`
	Status                     string           `json:"status"`
	Monitored                  bool             `json:"monitored"`
	Path                       string           `json:"path,omitempty"`
	QualityProfileID           int              `json:"qualityProfileId"`
	MetadataProfileID          int              `json:"metadataProfileId"`
	EbookQualityProfileID      int              `json:"ebookQualityProfileId"`
	AudiobookQualityProfileID  int              `json:"audiobookQualityProfileId"`
	EbookMetadataProfileID     int              `json:"ebookMetadataProfileId"`
	AudiobookMetadataProfileID int              `json:"audiobookMetadataProfileId"`
	EbookRootFolderPath        string           `json:"ebookRootFolderPath"`
	AudiobookRootFolderPath    string           `json:"audiobookRootFolderPath"`
	EbookMonitorFuture         bool             `json:"ebookMonitorFuture"`
	AudiobookMonitorFuture     bool             `json:"audiobookMonitorFuture"`
	Statistics                 AuthorStatistics `json:"statistics"`
	Images                     []Image          `json:"images"`
	Genres                     genreList        `json:"genres"`
}

// BookStatistics is the per-book file rollup Chaptarr returns on a book.
type BookStatistics struct {
	BookFileCount  int     `json:"bookFileCount"`
	BookCount      int     `json:"bookCount"`
	SizeOnDisk     int64   `json:"sizeOnDisk"`
	PercentOfBooks float64 `json:"percentOfBooks"`
}

// Edition is one published edition of a book (a specific format/ISBN). Chaptarr
// models ebooks and audiobooks as distinct editions of the same book.
type Edition struct {
	ID               int     `json:"id"`
	BookID           int     `json:"bookId"`
	ForeignEditionID string  `json:"foreignEditionId"`
	TitleSlug        string  `json:"titleSlug"`
	Title            string  `json:"title"`
	Format           string  `json:"format"`
	ASIN             string  `json:"asin"`
	ISBN13           string  `json:"isbn13"`
	Overview         string  `json:"overview"`
	Publisher        string  `json:"publisher"`
	PageCount        int     `json:"pageCount"`
	Monitored        bool    `json:"monitored"`
	ManualAdd        bool    `json:"manualAdd"`
	IsEbook          *bool   `json:"isEbook,omitempty"`
	Images           []Image `json:"images"`
}

type Book struct {
	ID            int        `json:"id"`
	Title         string     `json:"title"`
	AuthorID      int        `json:"authorId"`
	ForeignBookID string     `json:"foreignBookId"`
	TitleSlug     string     `json:"titleSlug"`
	Overview      string     `json:"overview"`
	ReleaseDate   *time.Time `json:"releaseDate,omitempty"`
	Monitored     bool       `json:"monitored"`
	// MediaType is the book-level format Chaptarr returns on library books
	// ("ebook"/"audiobook"); this fork tracks a title's ebook and audiobook as
	// separate records sharing a foreignBookId, distinguished by this field.
	MediaType    string         `json:"mediaType"`
	AnyEditionOk bool           `json:"anyEditionOk"`
	PageCount    int            `json:"pageCount"`
	Author       *AuthorContext `json:"author,omitempty"`
	Statistics   BookStatistics `json:"statistics"`
	Editions     []Edition      `json:"editions"`
	Images       []Image        `json:"images"`
	Genres       genreList      `json:"genres"`
}

type BookFile struct {
	ID            int             `json:"id"`
	AuthorID      int             `json:"authorId"`
	BookID        int             `json:"bookId"`
	EditionID     int             `json:"editionId"`
	Path          string          `json:"path"`
	Size          int64           `json:"size"`
	DateAdded     *time.Time      `json:"dateAdded,omitempty"`
	Quality       json.RawMessage `json:"quality"`
	MediaInfo     json.RawMessage `json:"mediaInfo"`
	QualityWeight int             `json:"qualityWeight"`
}

type QualityProfile struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	ProfileType string `json:"profileType"`
}

type MetadataProfile struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	ProfileType string `json:"profileType"`
}

type RootFolder struct {
	ID                          int    `json:"id"`
	Name                        string `json:"name"`
	Path                        string `json:"path"`
	FreeSpace                   int64  `json:"freeSpace"`
	Accessible                  bool   `json:"accessible"`
	Ebook                       bool   `json:"ebook"`
	Audiobook                   bool   `json:"audiobook"`
	IsEffectiveDefaultEbook     bool   `json:"isEffectiveDefaultEbook"`
	IsEffectiveDefaultAudiobook bool   `json:"isEffectiveDefaultAudiobook"`
}

// UnmarshalJSON accepts both Chaptarr metadata-profile representations. The
// current API uses numeric format discriminators (2 for ebook, 1 for
// audiobook), while transitional responses have serialized the same value as
// a string. Keeping a normalized string preserves either representation for
// callers without making every profile consumer repeat the compatibility
// parsing.
func (p *MetadataProfile) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID          int             `json:"id"`
		Name        string          `json:"name"`
		ProfileType json.RawMessage `json:"profileType"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	profileType := ""
	raw := bytes.TrimSpace(wire.ProfileType)
	if len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if raw[0] == '"' {
			if err := json.Unmarshal(raw, &profileType); err != nil {
				return fmt.Errorf("decode metadata profile type: %w", err)
			}
		} else {
			var number json.Number
			if err := json.Unmarshal(raw, &number); err != nil {
				return fmt.Errorf("decode metadata profile type: %w", err)
			}
			profileType = number.String()
		}
	}

	*p = MetadataProfile{ID: wire.ID, Name: wire.Name, ProfileType: profileType}
	return nil
}

// UnmarshalJSON keeps root-folder reads compatible with Chaptarr releases that
// omit accessible and releases that expose ebook/audiobook as nested settings
// objects. Only an explicit boolean true is a format discriminator; a nested
// object appears on both root types and must not select either one.
func (r *RootFolder) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID                          int             `json:"id"`
		Name                        string          `json:"name"`
		Path                        string          `json:"path"`
		FreeSpace                   int64           `json:"freeSpace"`
		Accessible                  json.RawMessage `json:"accessible"`
		Ebook                       json.RawMessage `json:"ebook"`
		Audiobook                   json.RawMessage `json:"audiobook"`
		IsEffectiveDefaultEbook     bool            `json:"isEffectiveDefaultEbook"`
		IsEffectiveDefaultAudiobook bool            `json:"isEffectiveDefaultAudiobook"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	accessible := true
	rawAccessible := bytes.TrimSpace(wire.Accessible)
	if len(rawAccessible) > 0 && !bytes.Equal(rawAccessible, []byte("null")) {
		if err := json.Unmarshal(rawAccessible, &accessible); err != nil {
			return fmt.Errorf("decode root folder accessibility: %w", err)
		}
	}

	*r = RootFolder{
		ID:                          wire.ID,
		Name:                        wire.Name,
		Path:                        wire.Path,
		FreeSpace:                   wire.FreeSpace,
		Accessible:                  accessible,
		Ebook:                       explicitTrue(wire.Ebook),
		Audiobook:                   explicitTrue(wire.Audiobook),
		IsEffectiveDefaultEbook:     wire.IsEffectiveDefaultEbook,
		IsEffectiveDefaultAudiobook: wire.IsEffectiveDefaultAudiobook,
	}
	return nil
}

func explicitTrue(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("true"))
}

// IsAccessible treats an omitted accessibility field as usable for older
// Chaptarr responses, while preserving an explicit false from the server.
func (r RootFolder) IsAccessible() bool {
	return r.Accessible
}

// LookupResult is one entry from author/lookup or book/lookup. It carries the
// fields needed to render a lookup row and seed an add: identifiers, a cover,
// and (for book lookups) the nested author the book belongs to.
//
// Editions is kept as raw JSON, not a typed []Edition, so it can be round-tripped
// verbatim into the book-add payload. Chaptarr's Editions table has NOT NULL
// constraints on columns the typed struct would drop (notably links and images),
// so a lossy re-encode fails the add with a SQLite constraint error.
type LookupResult struct {
	Title           string            `json:"title"`
	TitleSlug       string            `json:"titleSlug,omitempty"`
	AuthorName      string            `json:"authorName"`
	ForeignAuthorID string            `json:"foreignAuthorId"`
	ForeignBookID   string            `json:"foreignBookId"`
	Overview        string            `json:"overview"`
	Year            int               `json:"year"`
	Images          []Image           `json:"images"`
	Author          *Author           `json:"author,omitempty"`
	RemoteCover     string            `json:"remoteCover,omitempty"`
	Editions        []json.RawMessage `json:"editions,omitempty"`
}

// AddAuthorRequest mirrors Sonarr's AddSeriesRequest shape for adding an author
// to the Chaptarr library.
type AddAuthorRequest struct {
	ID                         int    `json:"id,omitempty"`
	AuthorName                 string `json:"authorName"`
	ForeignAuthorID            string `json:"foreignAuthorId"`
	QualityProfileID           int    `json:"qualityProfileId"`
	MetadataProfileID          int    `json:"metadataProfileId"`
	RootFolderPath             string `json:"rootFolderPath"`
	EbookQualityProfileID      int    `json:"ebookQualityProfileId,omitempty"`
	AudiobookQualityProfileID  int    `json:"audiobookQualityProfileId,omitempty"`
	EbookMetadataProfileID     int    `json:"ebookMetadataProfileId,omitempty"`
	AudiobookMetadataProfileID int    `json:"audiobookMetadataProfileId,omitempty"`
	EbookRootFolderPath        string `json:"ebookRootFolderPath,omitempty"`
	AudiobookRootFolderPath    string `json:"audiobookRootFolderPath,omitempty"`
	EbookMonitorFuture         bool   `json:"ebookMonitorFuture"`
	AudiobookMonitorFuture     bool   `json:"audiobookMonitorFuture"`
	Monitored                  bool   `json:"monitored"`
	AddOptions                 struct {
		// Monitor is Chaptarr's monitor scope applied at add time: one of
		// all/future/missing/existing/none. Empty means Chaptarr's default.
		Monitor               string `json:"monitor,omitempty"`
		SearchForMissingBooks bool   `json:"searchForMissingBooks"`
	} `json:"addOptions"`
}

// AddBookRequest adds a single book. Readarr nests the author inside the
// book-add payload, so an author ref is required for authors not yet tracked.
//
// This Chaptarr fork tracks ebook vs audiobook at the book level via
// MediaType + EbookMonitored/AudiobookMonitored (not per edition — lookup
// editions carry no format), so requested-format intent is set through those
// fields. Editions is raw JSON round-tripped from the lookup result so the
// add satisfies Chaptarr's NOT NULL edition columns (links, images).
type AddBookRequest struct {
	ForeignBookID              string           `json:"foreignBookId"`
	AuthorID                   int              `json:"authorId,omitempty"`
	Title                      string           `json:"title"`
	TitleSlug                  string           `json:"titleSlug,omitempty"`
	Monitored                  bool             `json:"monitored"`
	AnyEditionOk               bool             `json:"anyEditionOk"`
	MediaType                  string           `json:"mediaType,omitempty"`
	EbookMonitored             *bool            `json:"ebookMonitored,omitempty"`
	AudiobookMonitored         *bool            `json:"audiobookMonitored,omitempty"`
	RootFolderPath             string           `json:"rootFolderPath,omitempty"`
	EbookQualityProfileID      int              `json:"ebookQualityProfileId,omitempty"`
	AudiobookQualityProfileID  int              `json:"audiobookQualityProfileId,omitempty"`
	EbookMetadataProfileID     int              `json:"ebookMetadataProfileId,omitempty"`
	AudiobookMetadataProfileID int              `json:"audiobookMetadataProfileId,omitempty"`
	EbookRootFolderPath        string           `json:"ebookRootFolderPath,omitempty"`
	AudiobookRootFolderPath    string           `json:"audiobookRootFolderPath,omitempty"`
	Author                     AddAuthorRequest `json:"author"`
	AddOptions                 struct {
		SearchForNewBook bool `json:"searchForNewBook"`
	} `json:"addOptions"`
	Editions []json.RawMessage `json:"editions,omitempty"`
}

// AuthorContext is the lean author object embedded in queue/history/book records.
type AuthorContext struct {
	ID              int    `json:"id"`
	AuthorName      string `json:"authorName"`
	ForeignAuthorID string `json:"foreignAuthorId"`
}

// BookContext is the lean book object embedded in queue/history records.
type BookContext struct {
	ID          int        `json:"id"`
	Title       string     `json:"title"`
	ReleaseDate *time.Time `json:"releaseDate,omitempty"`
}

// StatusMessage is one grouped warning/error Chaptarr attaches to a queue item.
type StatusMessage struct {
	Title    string   `json:"title"`
	Messages []string `json:"messages"`
}

type QueueItem struct {
	ID                    int             `json:"id"`
	AuthorID              int             `json:"authorId"`
	BookID                int             `json:"bookId"`
	Title                 string          `json:"title"`
	Status                string          `json:"status"`
	TrackedDownloadStatus string          `json:"trackedDownloadStatus"`
	TrackedDownloadState  string          `json:"trackedDownloadState"`
	Timeleft              string          `json:"timeleft"`
	Size                  float64         `json:"size"`
	Sizeleft              float64         `json:"sizeleft"`
	DownloadClient        string          `json:"downloadClient"`
	DownloadID            string          `json:"downloadId"`
	Indexer               string          `json:"indexer"`
	Protocol              string          `json:"protocol"`
	ErrorMessage          string          `json:"errorMessage"`
	StatusMessages        []StatusMessage `json:"statusMessages"`
	Author                *AuthorContext  `json:"author,omitempty"`
	Book                  *BookContext    `json:"book,omitempty"`
}

// DetailedQueueItem is the queue record with author and book context. Chaptarr
// returns the same shape as QueueItem; the alias keeps callers symmetric with
// the Sonarr/Radarr clients, which distinguish a leaner queue view.
type DetailedQueueItem = QueueItem

type HistoryRecord struct {
	ID          int               `json:"id"`
	EventType   string            `json:"eventType"`
	SourceTitle string            `json:"sourceTitle"`
	Date        *time.Time        `json:"date,omitempty"`
	Quality     json.RawMessage   `json:"quality"`
	AuthorID    int               `json:"authorId"`
	BookID      int               `json:"bookId"`
	Author      *AuthorContext    `json:"author,omitempty"`
	Book        *BookContext      `json:"book,omitempty"`
	Data        map[string]string `json:"data,omitempty"`
}

type HistoryPage struct {
	Page         int             `json:"page"`
	PageSize     int             `json:"pageSize"`
	TotalRecords int             `json:"totalRecords"`
	Records      []HistoryRecord `json:"records"`
}

type WantedRecord struct {
	ID          int            `json:"id"`
	AuthorID    int            `json:"authorId"`
	BookID      int            `json:"bookId"`
	Title       string         `json:"title"`
	ReleaseDate *time.Time     `json:"releaseDate,omitempty"`
	Monitored   bool           `json:"monitored"`
	Author      *AuthorContext `json:"author,omitempty"`
}

type WantedPage struct {
	Page         int            `json:"page"`
	PageSize     int            `json:"pageSize"`
	TotalRecords int            `json:"totalRecords"`
	Records      []WantedRecord `json:"records"`
}

type Release struct {
	GUID       string          `json:"guid"`
	IndexerID  int             `json:"indexerId"`
	Indexer    string          `json:"indexer"`
	Title      string          `json:"title"`
	Size       int64           `json:"size"`
	Seeders    *int            `json:"seeders"`
	Leechers   *int            `json:"leechers"`
	Protocol   string          `json:"protocol"`
	AgeHours   float64         `json:"ageHours"`
	Quality    json.RawMessage `json:"quality"`
	Rejected   bool            `json:"rejected"`
	Rejections []string        `json:"rejections"`
}

type DiskSpace struct {
	Path       string `json:"path"`
	Label      string `json:"label"`
	FreeSpace  int64  `json:"freeSpace"`
	TotalSpace int64  `json:"totalSpace"`
}

// HealthCheck is one entry from Chaptarr's system health report: a config-level
// problem (download client unreachable, remote path mapping, indexers down, no
// root folder, low disk, etc.). Type is one of ok/notice/warning/error.
type HealthCheck struct {
	Source  string `json:"source"`
	Type    string `json:"type"`
	Message string `json:"message"`
	WikiURL string `json:"wikiUrl"`
}

// ManualImportRejection is a single reason Chaptarr would not auto-import a
// file, plus whether the rejection is permanent (a force import will likely
// still fail) or temporary.
type ManualImportRejection struct {
	Reason string `json:"reason"`
	Type   string `json:"type"`
}

// ManualImportCandidate is a file Chaptarr found for a download, as returned by
// GET /manualimport. Quality is kept as raw JSON so it can be round-tripped
// verbatim back into the ManualImport command (modeling and re-serializing it
// risks losing fields Chaptarr requires).
type ManualImportCandidate struct {
	ID           int                     `json:"id"`
	Path         string                  `json:"path"`
	FolderName   string                  `json:"folderName"`
	Name         string                  `json:"name"`
	Size         int64                   `json:"size"`
	AuthorID     int                     `json:"-"`
	BookID       int                     `json:"-"`
	Quality      json.RawMessage         `json:"quality"`
	ReleaseGroup string                  `json:"releaseGroup"`
	DownloadID   string                  `json:"downloadId"`
	Rejections   []ManualImportRejection `json:"rejections"`
}

// UnmarshalJSON decodes a manual-import candidate, lifting the nested author id
// (Chaptarr nests it under "author": {"id": ...}) into AuthorID and the book id
// (nested under "book": {"id": ...}, else top-level "bookId") into BookID.
func (m *ManualImportCandidate) UnmarshalJSON(data []byte) error {
	type alias ManualImportCandidate
	aux := struct {
		*alias
		Author *struct {
			ID int `json:"id"`
		} `json:"author"`
		Book *struct {
			ID int `json:"id"`
		} `json:"book"`
		BookID int `json:"bookId"`
	}{alias: (*alias)(m)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Author != nil {
		m.AuthorID = aux.Author.ID
	}
	if aux.Book != nil {
		m.BookID = aux.Book.ID
	} else {
		m.BookID = aux.BookID
	}
	return nil
}

// ManualImportFile is one file to import via the ManualImport command. Quality
// is passed back verbatim from the candidate. AuthorID and BookID must be set
// for Chaptarr or the file is silently skipped.
type ManualImportFile struct {
	Path         string          `json:"path"`
	FolderName   string          `json:"folderName,omitempty"`
	AuthorID     int             `json:"authorId"`
	BookID       int             `json:"bookId"`
	Quality      json.RawMessage `json:"quality,omitempty"`
	ReleaseGroup string          `json:"releaseGroup,omitempty"`
	DownloadID   string          `json:"downloadId,omitempty"`
}

// do executes a request with an optional JSON body, fails on non-2xx status,
// and decodes JSON into out when out is non-nil. Upstream error bodies are
// deliberately excluded because they can contain credentials or signed URLs.
func (c *Client) do(method, path string, body, out any) error {
	return c.doWith(c.httpClient, method, path, body, out)
}

func (c *Client) doWith(client *http.Client, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		// Transport errors embed the full request URL (and DNS failures repeat
		// the hostname). These errors surface beyond admins — e.g. in request
		// failures — so summarize them host-free like the status branch below.
		requestPath, _, _ := strings.Cut(path, "?")
		return fmt.Errorf("chaptarr %s %s: %s", method, requestPath, transporterr.Summarize(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		requestPath, _, _ := strings.Cut(path, "?")
		return fmt.Errorf("chaptarr %s %s returned status %d", method, requestPath, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *Client) doRequest(method, path string) (*http.Response, error) {
	return c.doRequestContext(context.Background(), method, path)
}

func (c *Client) doRequestContext(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Host-free, like doWith: transport errors embed the full request URL.
		requestPath, _, _ := strings.Cut(path, "?")
		return nil, fmt.Errorf("chaptarr %s %s: %s", method, requestPath, transporterr.Summarize(err))
	}
	return resp, nil
}

// LookupAuthor searches Chaptarr's metadata for authors matching term.
func (c *Client) LookupAuthor(term string) ([]LookupResult, error) {
	resp, err := c.doRequest("GET", "/api/v1/author/lookup?term="+url.QueryEscape(term))
	if err != nil {
		return nil, fmt.Errorf("chaptarr author lookup: %w", err)
	}
	defer resp.Body.Close()

	var results []LookupResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode chaptarr author lookup: %w", err)
	}
	return results, nil
}

// LookupBook searches Chaptarr's metadata for books matching term.
func (c *Client) LookupBook(term string) ([]LookupResult, error) {
	resp, err := c.doRequest("GET", "/api/v1/book/lookup?term="+url.QueryEscape(term))
	if err != nil {
		return nil, fmt.Errorf("chaptarr book lookup: %w", err)
	}
	defer resp.Body.Close()

	var results []LookupResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode chaptarr book lookup: %w", err)
	}
	return results, nil
}

// GetAllAuthors lists every author in the Chaptarr library.
func (c *Client) GetAllAuthors() ([]Author, error) {
	var authors []Author
	if err := c.do("GET", "/api/v1/author", nil, &authors); err != nil {
		return nil, fmt.Errorf("chaptarr author list: %w", err)
	}
	return authors, nil
}

// GetAuthor returns a single author by id.
func (c *Client) GetAuthor(id int) (*Author, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/v1/author/%d", id))
	if err != nil {
		return nil, fmt.Errorf("chaptarr get author: %w", err)
	}
	defer resp.Body.Close()

	var author Author
	if err := json.NewDecoder(resp.Body).Decode(&author); err != nil {
		return nil, fmt.Errorf("decode chaptarr author: %w", err)
	}
	return &author, nil
}

// GetBooks lists the books of one author.
func (c *Client) GetBooks(authorID int) ([]Book, error) {
	var books []Book
	path := fmt.Sprintf("/api/v1/book?authorId=%d", authorID)
	if err := c.do("GET", path, nil, &books); err != nil {
		return nil, fmt.Errorf("chaptarr books: %w", err)
	}
	return books, nil
}

// GetAllBooks lists every book in the Chaptarr library (all authors). Chaptarr
// returns the same book shape as GetBooks; omitting authorId widens the scope.
func (c *Client) GetAllBooks() ([]Book, error) {
	var books []Book
	if err := c.do("GET", "/api/v1/book", nil, &books); err != nil {
		return nil, fmt.Errorf("chaptarr books: %w", err)
	}
	return books, nil
}

// GetBook returns a single book by id.
func (c *Client) GetBook(id int) (*Book, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/api/v1/book/%d", id))
	if err != nil {
		return nil, fmt.Errorf("chaptarr get book: %w", err)
	}
	defer resp.Body.Close()

	var book Book
	if err := json.NewDecoder(resp.Body).Decode(&book); err != nil {
		return nil, fmt.Errorf("decode chaptarr book: %w", err)
	}
	return &book, nil
}

// GetBookFiles lists the book files on disk for one author.
func (c *Client) GetBookFiles(authorID int) ([]BookFile, error) {
	var files []BookFile
	path := fmt.Sprintf("/api/v1/bookfile?authorId=%d", authorID)
	if err := c.do("GET", path, nil, &files); err != nil {
		return nil, fmt.Errorf("chaptarr book files: %w", err)
	}
	return files, nil
}

// GetBookFile returns live metadata for one completed file in Chaptarr.
func (c *Client) GetBookFile(id int) (*BookFile, error) {
	var file BookFile
	path := fmt.Sprintf("/api/v1/bookfile/%d", id)
	if err := c.do(http.MethodGet, path, nil, &file); err != nil {
		return nil, fmt.Errorf("chaptarr book file: %w", err)
	}
	return &file, nil
}

func (c *Client) GetQualityProfiles() ([]QualityProfile, error) {
	resp, err := c.doRequest("GET", "/api/v1/qualityprofile")
	if err != nil {
		return nil, fmt.Errorf("chaptarr quality profiles: %w", err)
	}
	defer resp.Body.Close()

	var profiles []QualityProfile
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode quality profiles: %w", err)
	}
	return profiles, nil
}

// GetQualityProfilesRaw returns every quality profile exactly as Chaptarr
// sent it. Settings objects must round-trip verbatim on a future PUT
// (modeling and re-serializing them risks losing fields Chaptarr requires),
// so callers decode only the fields they need from each raw object.
func (c *Client) GetQualityProfilesRaw() ([]json.RawMessage, error) {
	return c.GetQualityProfilesRawContext(context.Background())
}

func (c *Client) GetQualityProfilesRawContext(ctx context.Context) ([]json.RawMessage, error) {
	resp, err := c.doRequestContext(ctx, "GET", "/api/v1/qualityprofile")
	if err != nil {
		return nil, fmt.Errorf("chaptarr quality profiles: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("chaptarr GET /api/v1/qualityprofile returned status %d", resp.StatusCode)
	}
	var profiles []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode quality profiles: %w", err)
	}
	return profiles, nil
}

// UpdateQualityProfileRaw fully replaces one credential-free quality profile.
func (c *Client) UpdateQualityProfileRaw(id int, body json.RawMessage) (json.RawMessage, error) {
	return c.UpdateQualityProfileRawContext(context.Background(), id, body)
}

func (c *Client) UpdateQualityProfileRawContext(ctx context.Context, id int, body json.RawMessage) (json.RawMessage, error) {
	path := fmt.Sprintf("/api/v1/qualityprofile/%d", id)
	raw, _, err := arrcommon.DoSettingsWrite(ctx, c.httpClient, "chaptarr", c.baseURL, c.apiKey, http.MethodPut, path, body)
	return raw, err
}

// GetCustomFormatsRaw returns every custom format exactly as Chaptarr sent
// it, verbatim for the same round-trip reason as GetQualityProfilesRaw. A 404
// maps to ErrCustomFormatsNotFound.
func (c *Client) GetCustomFormatsRaw() ([]json.RawMessage, error) {
	return c.GetCustomFormatsRawContext(context.Background())
}

// GetCustomFormatsRawContext is the cancellation-aware mutation preflight.
func (c *Client) GetCustomFormatsRawContext(ctx context.Context) ([]json.RawMessage, error) {
	resp, err := c.doRequestContext(ctx, "GET", "/api/v1/customformat")
	if err != nil {
		return nil, fmt.Errorf("chaptarr custom formats: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrCustomFormatsNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("chaptarr GET /api/v1/customformat returned status %d", resp.StatusCode)
	}
	var formats []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&formats); err != nil {
		return nil, fmt.Errorf("decode custom formats: %w", err)
	}
	return formats, nil
}

// CreateCustomFormatRaw creates one credential-free custom-format object. Its
// dedicated write path is the only Chaptarr client path allowed to surface the
// typed, redacted validation details from an HTTP 400 response.
func (c *Client) CreateCustomFormatRaw(body json.RawMessage) (json.RawMessage, error) {
	return c.CreateCustomFormatRawContext(context.Background(), body)
}

func (c *Client) CreateCustomFormatRawContext(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	raw, _, err := arrcommon.DoSettingsWrite(ctx, c.httpClient, "chaptarr", c.baseURL, c.apiKey, http.MethodPost, "/api/v1/customformat", body)
	return raw, err
}

// UpdateCustomFormatRaw fully replaces one custom-format object.
func (c *Client) UpdateCustomFormatRaw(id int, body json.RawMessage) (json.RawMessage, error) {
	return c.UpdateCustomFormatRawContext(context.Background(), id, body)
}

func (c *Client) UpdateCustomFormatRawContext(ctx context.Context, id int, body json.RawMessage) (json.RawMessage, error) {
	path := fmt.Sprintf("/api/v1/customformat/%d", id)
	raw, _, err := arrcommon.DoSettingsWrite(ctx, c.httpClient, "chaptarr", c.baseURL, c.apiKey, http.MethodPut, path, body)
	return raw, err
}

func (c *Client) GetMetadataProfiles() ([]MetadataProfile, error) {
	resp, err := c.doRequest("GET", "/api/v1/metadataprofile")
	if err != nil {
		return nil, fmt.Errorf("chaptarr metadata profiles: %w", err)
	}
	defer resp.Body.Close()

	var profiles []MetadataProfile
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode metadata profiles: %w", err)
	}
	return profiles, nil
}

func (c *Client) GetRootFolders() ([]RootFolder, error) {
	resp, err := c.doRequest("GET", "/api/v1/rootfolder")
	if err != nil {
		return nil, fmt.Errorf("chaptarr root folders: %w", err)
	}
	defer resp.Body.Close()

	var folders []RootFolder
	if err := json.NewDecoder(resp.Body).Decode(&folders); err != nil {
		return nil, fmt.Errorf("decode root folders: %w", err)
	}
	return folders, nil
}

// AddAuthor adds an author to the Chaptarr library.
func (c *Client) AddAuthor(req AddAuthorRequest) (*Author, error) {
	var author Author
	if err := c.do("POST", "/api/v1/author", req, &author); err != nil {
		return nil, fmt.Errorf("chaptarr add author: %w", err)
	}
	return &author, nil
}

// AddBook adds a single book (and, if needed, its author) to the library.
func (c *Client) AddBook(req AddBookRequest) (*Book, error) {
	var book Book
	if err := c.do("POST", "/api/v1/book", req, &book); err != nil {
		return nil, fmt.Errorf("chaptarr add book: %w", err)
	}
	return &book, nil
}

// SetBookMonitored toggles monitoring for the given books. Chaptarr's
// book/monitor endpoint is a PUT (a POST returns 405); it also re-derives a
// book's ebook/audiobook monitor flags from its mediaType, so monitoring a book
// whose mediaType was set on add applies the requested format.
func (c *Client) SetBookMonitored(bookIDs []int, monitored bool) error {
	body := map[string]any{"bookIds": bookIDs, "monitored": monitored}
	if err := c.do("PUT", "/api/v1/book/monitor", body, nil); err != nil {
		return fmt.Errorf("chaptarr set book monitored: %w", err)
	}
	return nil
}

func (c *Client) GetQueue() ([]QueueItem, error) {
	resp, err := c.doRequest("GET", "/api/v1/queue?includeAuthor=true&includeBook=true")
	if err != nil {
		return nil, fmt.Errorf("chaptarr queue: %w", err)
	}
	defer resp.Body.Close()

	var queueResp struct {
		Records []QueueItem `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&queueResp); err != nil {
		return nil, fmt.Errorf("decode queue: %w", err)
	}
	return queueResp.Records, nil
}

// queueMaxRecords is a safety cap on the total queue records accumulated across
// pages of GetQueueDetailed.
const queueMaxRecords = 1000

// GetQueueDetailed returns the download queue with author and book context,
// paginating from page until all records are fetched (capped at
// queueMaxRecords).
func (c *Client) GetQueueDetailed(page, pageSize int) ([]DetailedQueueItem, error) {
	var all []DetailedQueueItem
	for ; ; page++ {
		var resp struct {
			TotalRecords int                 `json:"totalRecords"`
			Records      []DetailedQueueItem `json:"records"`
		}
		path := fmt.Sprintf("/api/v1/queue?page=%d&pageSize=%d&includeAuthor=true&includeBook=true", page, pageSize)
		if err := c.do("GET", path, nil, &resp); err != nil {
			return nil, fmt.Errorf("chaptarr queue: %w", err)
		}
		all = append(all, resp.Records...)
		if len(resp.Records) == 0 || len(all) >= resp.TotalRecords || len(all) >= queueMaxRecords {
			break
		}
	}
	if len(all) > queueMaxRecords {
		all = all[:queueMaxRecords]
	}
	return all, nil
}

// RemoveQueueItem removes an item from the download queue. removeFromClient
// also deletes the download from the client; blocklist prevents the release
// from being grabbed again; skipRedownload suppresses the automatic re-search
// that a blocklist would otherwise trigger; changeCategory hands the download
// to the client's post-import category instead of removing it.
func (c *Client) RemoveQueueItem(id int, removeFromClient, blocklist, skipRedownload, changeCategory bool) error {
	path := fmt.Sprintf("/api/v1/queue/%d?removeFromClient=%t&blocklist=%t&skipRedownload=%t&changeCategory=%t",
		id, removeFromClient, blocklist, skipRedownload, changeCategory)
	if err := c.do("DELETE", path, nil, nil); err != nil {
		return fmt.Errorf("chaptarr remove queue item: %w", err)
	}
	return nil
}

// GetHistory returns a page of history records (grabs, imports, failures),
// most recent first.
func (c *Client) GetHistory(page, pageSize int) (*HistoryPage, error) {
	var hp HistoryPage
	path := fmt.Sprintf("/api/v1/history?page=%d&pageSize=%d&sortKey=date&sortDirection=descending", page, pageSize)
	if err := c.do("GET", path, nil, &hp); err != nil {
		return nil, fmt.Errorf("chaptarr history: %w", err)
	}
	return &hp, nil
}

// GetWantedMissing returns a page of monitored books with no file.
func (c *Client) GetWantedMissing(page, pageSize int) (*WantedPage, error) {
	var wp WantedPage
	path := fmt.Sprintf("/api/v1/wanted/missing?page=%d&pageSize=%d&sortKey=releaseDate&sortDirection=descending&includeAuthor=true", page, pageSize)
	if err := c.do("GET", path, nil, &wp); err != nil {
		return nil, fmt.Errorf("chaptarr wanted missing: %w", err)
	}
	return &wp, nil
}

// GetWantedCutoff returns a page of monitored books whose file is below the
// quality cutoff.
func (c *Client) GetWantedCutoff(page, pageSize int) (*WantedPage, error) {
	var wp WantedPage
	path := fmt.Sprintf("/api/v1/wanted/cutoff?page=%d&pageSize=%d&sortKey=releaseDate&sortDirection=descending&includeAuthor=true", page, pageSize)
	if err := c.do("GET", path, nil, &wp); err != nil {
		return nil, fmt.Errorf("chaptarr wanted cutoff: %w", err)
	}
	return &wp, nil
}

// releaseSearchClient allows the much longer round-trips of interactive
// release searches, which query every configured indexer.
func releaseSearchClient() *http.Client {
	return &http.Client{
		Timeout:       120 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// SearchReleases runs an interactive release search for a book.
func (c *Client) SearchReleases(bookID int) ([]Release, error) {
	var raw json.RawMessage
	path := fmt.Sprintf("/api/v1/release?bookId=%d", bookID)
	if err := c.doWith(releaseSearchClient(), "GET", path, nil, &raw); err != nil {
		return nil, fmt.Errorf("chaptarr release search: %w", err)
	}
	releases, err := decodeReleases(raw)
	if err != nil {
		return nil, fmt.Errorf("chaptarr release search: %w", err)
	}
	return releases, nil
}

// decodeReleases parses Chaptarr's interactive-search response. This fork wraps
// results in a {"releases": [...]} envelope (alongside hiddenReleases /
// filterSummary), while stock Servarr returns a bare array — accept either.
func decodeReleases(raw json.RawMessage) ([]Release, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '{' {
		var env struct {
			Releases []Release `json:"releases"`
		}
		if err := json.Unmarshal(trimmed, &env); err != nil {
			return nil, fmt.Errorf("decode release envelope: %w", err)
		}
		return env.Releases, nil
	}
	var releases []Release
	if err := json.Unmarshal(trimmed, &releases); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	return releases, nil
}

// GrabRelease tells Chaptarr to send a previously searched release to the
// download client.
func (c *Client) GrabRelease(guid string, indexerID int) error {
	body := map[string]any{"guid": guid, "indexerId": indexerID}
	if err := c.do("POST", "/api/v1/release", body, nil); err != nil {
		return fmt.Errorf("chaptarr grab release: %w", err)
	}
	return nil
}

// triggerCommand posts a command payload to Chaptarr's command endpoint.
func (c *Client) triggerCommand(payload map[string]any) error {
	if err := c.do("POST", "/api/v1/command", payload, nil); err != nil {
		return fmt.Errorf("chaptarr command: %w", err)
	}
	return nil
}

// TriggerAuthorSearch starts an automatic search for all monitored books of an
// author.
func (c *Client) TriggerAuthorSearch(authorID int) error {
	return c.triggerCommand(map[string]any{"name": "AuthorSearch", "authorId": authorID})
}

// TriggerBookSearch starts an automatic search for specific books.
func (c *Client) TriggerBookSearch(bookIDs []int) error {
	return c.triggerCommand(map[string]any{"name": "BookSearch", "bookIds": bookIDs})
}

// TriggerMissingBookSearch starts a search for all monitored, missing books.
func (c *Client) TriggerMissingBookSearch() error {
	return c.triggerCommand(map[string]any{"name": "MissingBookSearch"})
}

// TriggerRefreshAuthor refreshes metadata and rescans files for an author.
func (c *Client) TriggerRefreshAuthor(authorID int) error {
	return c.triggerCommand(map[string]any{"name": "RefreshAuthor", "authorId": authorID})
}

// ProcessMonitoredDownloads asks Chaptarr to run its import pass over the
// download client now (the pass that normally runs on a timer).
func (c *Client) ProcessMonitoredDownloads() error {
	return c.triggerCommand(map[string]any{"name": "ProcessMonitoredDownloads"})
}

// RescanAuthor rescans the files on disk for an author.
func (c *Client) RescanAuthor(authorID int) error {
	return c.triggerCommand(map[string]any{"name": "RescanFolders", "authorId": authorID})
}

// GetDiskSpace reports disk usage for Chaptarr's mounted volumes.
func (c *Client) GetDiskSpace() ([]DiskSpace, error) {
	var disks []DiskSpace
	if err := c.do("GET", "/api/v1/diskspace", nil, &disks); err != nil {
		return nil, fmt.Errorf("chaptarr diskspace: %w", err)
	}
	return disks, nil
}

// GetHealth returns Chaptarr's current system health checks. These surface
// config-level root causes (download client down, remote path mapping wrong,
// indexers unavailable) that per-item queue diagnosis can only guess at.
func (c *Client) GetHealth() ([]HealthCheck, error) {
	var checks []HealthCheck
	if err := c.do("GET", "/api/v1/health", nil, &checks); err != nil {
		return nil, fmt.Errorf("chaptarr health: %w", err)
	}
	return checks, nil
}

// GetManualImportCandidates lists the files Chaptarr found for a download,
// including any rejection reasons, without importing existing files.
func (c *Client) GetManualImportCandidates(downloadID string) ([]ManualImportCandidate, error) {
	var candidates []ManualImportCandidate
	path := fmt.Sprintf("/api/v1/manualimport?downloadId=%s&filterExistingFiles=false", url.QueryEscape(downloadID))
	if err := c.doWith(releaseSearchClient(), "GET", path, nil, &candidates); err != nil {
		return nil, fmt.Errorf("chaptarr manual import candidates: %w", err)
	}
	return candidates, nil
}

// ExecuteManualImport tells Chaptarr to import the given files. importMode must
// be lowercase (move/copy/auto); the PascalCase form is silently ignored by the
// ManualImport command.
func (c *Client) ExecuteManualImport(files []ManualImportFile) error {
	payload := map[string]any{
		"name":       "ManualImport",
		"importMode": "auto",
		"files":      files,
	}
	return c.triggerCommand(payload)
}

// ebookTokens and audiobookTokens are uppercase substrings matched against a
// quality name to classify a book file's format.
var (
	ebookTokens = []string{
		"EPUB", "MOBI", "AZW3", "AZW", "PDF", "CBZ", "CBR", "KEPUB",
		"EBOOK", "E-BOOK", "KINDLE", "NOOK", "KOBO", "DIGITAL",
	}
	audiobookTokens = []string{
		"MP3", "M4B", "M4A", "FLAC", "AAC", "OGG", "OPUS",
		"AUDIOBOOK", "AUDIO BOOK", "AUDIBLE", "AUDIO CD", "MP3 CD", "AUDIO",
	}
)

// Format classifications returned by FormatOf and used to route a book record
// to its ebook/audiobook slot.
const (
	FormatEbook     = "ebook"
	FormatAudiobook = "audiobook"
	FormatUnknown   = "unknown"
)

// FormatOf classifies a Chaptarr quality name as "ebook", "audiobook", or
// "unknown" via a case-insensitive substring match. Ebook tokens are checked
// first so an ambiguous name leans toward the text format.
func FormatOf(qualityName string) string {
	upper := strings.ToUpper(qualityName)
	for _, tok := range ebookTokens {
		if strings.Contains(upper, tok) {
			return FormatEbook
		}
	}
	for _, tok := range audiobookTokens {
		if strings.Contains(upper, tok) {
			return FormatAudiobook
		}
	}
	return FormatUnknown
}
