// discovermusic.go — public music discovery: the browse feeds, artist and
// album search, artist pages, genres, and the artwork relay. This is metadata
// only — no library or request state rides on any of it, exactly like the
// real MusicBrainz/ListenBrainz-backed handlers. Authorization runs before
// the catalog is read and again before the response.
//
// Prefix: mdisc…
package main

import (
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// mdiscPageSize is the fixed page size every feed answers with, so
// `next_page` means something the app can act on.
const mdiscPageSize = 12

// mdiscPeriods are the windows the Popular feed offers.
var mdiscPeriods = map[string]bool{"this_week": true, "this_month": true, "this_year": true}

// mdiscGenres is the browse vocabulary, in the app's own order. The tag is
// what the feed filters on; the demo maps it to the seeded album genres.
var mdiscGenres = []struct{ ID, Name, Tag string }{
	{"pop", "Pop", "pop"},
	{"rock", "Rock", "rock"},
	{"hip-hop", "Hip-Hop", "hip-hop"},
	{"r-and-b", "R&B", "r&b"},
	{"electronic", "Electronic", "electronic"},
	{"jazz", "Jazz", "jazz"},
	{"classical", "Classical", "classical"},
	{"metal", "Metal", "metal"},
	{"country", "Country", "country"},
	{"folk", "Folk", "folk"},
	{"blues", "Blues", "blues"},
	{"reggae", "Reggae", "reggae"},
}

func registerMusicDiscovery(r chi.Router) {
	// Static segments before {feed} / {mbid}, so chi matches them first.
	r.Get("/discover/music/search", mdiscSearchHandler)
	r.Get("/discover/music/artists", mdiscArtistsHandler)
	r.Get("/discover/music/artwork/{mbid}", mdiscArtworkHandler)
	r.Get("/discover/music/{feed}", mdiscFeedHandler)
	r.Get("/genres/music", mdiscGenresHandler)
	r.Get("/media/music/artists/{mbid}", mdiscArtistHandler)
	r.Get("/media/music/artists/{mbid}/albums", mdiscArtistAlbumsHandler)
	r.Get("/media/music/{mbid}", mdiscAlbumHandler)
}

// mdiscAuthorize resolves and authorizes the Lidarr instance a metadata call
// is scoped to. Admins may browse the catalog with no library; a requester
// must hold the instance.
func mdiscAuthorize(w http.ResponseWriter, r *http.Request) bool {
	u := userFrom(r)
	explicit := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if explicit == "" {
		if inst := effectiveInstanceFor(u, serviceLidarr); inst != nil {
			explicit = inst.ID
		} else if u != nil && u.Role == roleAdmin {
			// Admins may browse the catalog before any library exists.
			return true
		} else {
			writeErr(w, http.StatusForbidden, "music is not available to you")
			return false
		}
	}
	inst := instanceByID(explicit)
	if inst == nil || inst.ServiceType != serviceLidarr {
		writeErr(w, http.StatusBadRequest, "invalid lidarr instance")
		return false
	}
	if u == nil || u.Role != roleAdmin {
		for _, id := range visibleInstanceIDs(u, serviceLidarr) {
			if id == inst.ID {
				return true
			}
		}
		writeErr(w, http.StatusForbidden, "music is not available to you")
		return false
	}
	return true
}

// ─── Views ──────────────────────────────────────────────

// mdiscArtistView is the public artist record: identity and description, no
// library state.
func mdiscArtistView(a *DemoArtist) map[string]any {
	out := map[string]any{
		"foreign_id": a.ForeignID,
		"name":       a.Name,
		"type":       a.ArtistType,
	}
	if len(a.Genres) > 0 {
		out["disambiguation"] = a.Genres[0]
	}
	return out
}

// mdiscAlbumView is the public album record. release_type defaults to
// "Album"; the artwork field is an identity the client turns into a relay
// path, never a URL it dereferences itself.
func mdiscAlbumView(album *DemoAlbum) map[string]any {
	artistName := ""
	artists := []map[string]any{}
	lidMu.Lock()
	if a := lidArtistsByID[album.ArtistID]; a != nil {
		artistName = a.Name
		artists = append(artists, mdiscArtistView(a))
	}
	lidMu.Unlock()
	releaseType := album.AlbumType
	if releaseType == "" {
		releaseType = "Album"
	}
	out := map[string]any{
		"foreign_id":   album.ForeignID,
		"title":        album.Title,
		"artist":       artistName,
		"artists":      artists,
		"release_date": album.ReleaseDate,
		"release_type": releaseType,
		"artwork":      album.ForeignID,
	}
	if len(album.SecondaryTypes) > 0 {
		out["disambiguation"] = strings.Join(album.SecondaryTypes, ", ")
	}
	return out
}

// mdiscPage renders one page of albums with the app's paging contract:
// next_page is present only when it is strictly greater than page.
func mdiscPage(w http.ResponseWriter, albums []*DemoAlbum, page int, emptyMessage string) {
	if page < 1 {
		page = 1
	}
	start := (page - 1) * mdiscPageSize
	results := []map[string]any{}
	if start < len(albums) {
		end := start + mdiscPageSize
		if end > len(albums) {
			end = len(albums)
		}
		for _, album := range albums[start:end] {
			results = append(results, mdiscAlbumView(album))
		}
	}
	out := map[string]any{"page": page, "results": results}
	if start+mdiscPageSize < len(albums) {
		out["next_page"] = page + 1
	}
	if len(results) == 0 && emptyMessage != "" {
		out["empty_message"] = emptyMessage
	}
	writeJSON(w, http.StatusOK, out)
}

// ─── Feeds ──────────────────────────────────────────────

// mdiscFeedHandler serves the two browse feeds. `popular` is ordered by the
// demo's own standing (release recency inside the chosen window); `genre`
// filters the corpus by tag.
func mdiscFeedHandler(w http.ResponseWriter, r *http.Request) {
	if !mdiscAuthorize(w, r) {
		return
	}
	feed := chi.URLParam(r, "feed")
	switch feed {
	case "popular", "new-releases", "genre":
	default:
		writeErr(w, http.StatusNotFound, "unknown music feed")
		return
	}
	if feed == "popular" {
		if period := r.URL.Query().Get("period"); period != "" && !mdiscPeriods[period] {
			writeErr(w, http.StatusBadRequest, "unknown period")
			return
		}
	}

	albums := allAlbums()
	if feed == "genre" {
		tag := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("genre")))
		known := false
		for _, g := range mdiscGenres {
			if g.ID == tag {
				tag = strings.ToLower(g.Tag)
				known = true
				break
			}
		}
		if !known {
			writeErr(w, http.StatusBadRequest, "unknown genre")
			return
		}
		filtered := []*DemoAlbum{}
		for _, album := range albums {
			for _, genre := range album.Genres {
				if strings.ToLower(genre) == tag {
					filtered = append(filtered, album)
					break
				}
			}
		}
		albums = filtered
	}

	// Newest first for new-releases; the popular feed leads with the
	// best-represented artists, which is the demo's stand-in for listens.
	sort.SliceStable(albums, func(i, j int) bool {
		if feed == "new-releases" {
			return albums[i].ReleaseDate > albums[j].ReleaseDate
		}
		return albums[i].ID < albums[j].ID
	})

	empty := "Nothing is trending in this window yet."
	if feed == "genre" {
		empty = "No albums in this genre yet."
	}
	mdiscPage(w, albums, queryInt(r, "page", 1), empty)
}

func mdiscSearchHandler(w http.ResponseWriter, r *http.Request) {
	if !mdiscAuthorize(w, r) {
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	includeSingles := r.URL.Query().Get("include_singles") == "true"
	matches := []*DemoAlbum{}
	for _, album := range allAlbums() {
		if !includeSingles && album.AlbumType == "Single" {
			continue
		}
		if query == "" {
			continue
		}
		haystack := strings.ToLower(album.Title)
		lidMu.Lock()
		if a := lidArtistsByID[album.ArtistID]; a != nil {
			haystack += " " + strings.ToLower(a.Name)
		}
		lidMu.Unlock()
		if strings.Contains(haystack, query) {
			matches = append(matches, album)
		}
	}
	mdiscPage(w, matches, queryInt(r, "page", 1),
		"No albums matched that search. Singles and EPs are hidden unless you ask for them.")
}

func mdiscArtistsHandler(w http.ResponseWriter, r *http.Request) {
	if !mdiscAuthorize(w, r) {
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	matches := []map[string]any{}
	lidMu.Lock()
	for _, a := range lidArtists {
		if query != "" && !strings.Contains(strings.ToLower(a.Name), query) {
			continue
		}
		matches = append(matches, mdiscArtistView(a))
	}
	lidMu.Unlock()

	start := (page - 1) * mdiscPageSize
	results := []map[string]any{}
	if start < len(matches) {
		end := start + mdiscPageSize
		if end > len(matches) {
			end = len(matches)
		}
		results = matches[start:end]
	}
	out := map[string]any{"page": page, "results": results}
	if start+mdiscPageSize < len(matches) {
		out["next_page"] = page + 1
	}
	if len(results) == 0 {
		out["empty_message"] = "No artists matched that search."
	}
	writeJSON(w, http.StatusOK, out)
}

func mdiscGenresHandler(w http.ResponseWriter, r *http.Request) {
	if !mdiscAuthorize(w, r) {
		return
	}
	genres := []map[string]any{}
	for _, g := range mdiscGenres {
		genres = append(genres, map[string]any{"id": g.ID, "name": g.Name, "tag": g.Tag})
	}
	writeJSON(w, http.StatusOK, map[string]any{"genres": genres})
}

// ─── Records ────────────────────────────────────────────

func mdiscAlbumHandler(w http.ResponseWriter, r *http.Request) {
	if !mdiscAuthorize(w, r) {
		return
	}
	// A merged release group resolves to the record that survived, so a
	// client addressing the old id still lands on the right album.
	mbid, _ := lidCanonicalForeignID(chi.URLParam(r, "mbid"))
	album, ok := albumByForeignID(mbid)
	if !ok {
		writeErr(w, http.StatusNotFound, "album not found")
		return
	}
	writeJSON(w, http.StatusOK, mdiscAlbumView(album))
}

func mdiscArtistHandler(w http.ResponseWriter, r *http.Request) {
	if !mdiscAuthorize(w, r) {
		return
	}
	lidMu.Lock()
	a := lidArtistsByFID[strings.TrimSpace(chi.URLParam(r, "mbid"))]
	var view map[string]any
	if a != nil {
		view = mdiscArtistView(a)
	}
	lidMu.Unlock()
	if view == nil {
		writeErr(w, http.StatusNotFound, "artist not found")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func mdiscArtistAlbumsHandler(w http.ResponseWriter, r *http.Request) {
	if !mdiscAuthorize(w, r) {
		return
	}
	mbid := strings.TrimSpace(chi.URLParam(r, "mbid"))
	lidMu.Lock()
	a := lidArtistsByFID[mbid]
	artistID := 0
	if a != nil {
		artistID = a.ID
	}
	lidMu.Unlock()
	if artistID == 0 {
		writeErr(w, http.StatusNotFound, "artist not found")
		return
	}
	albums := []*DemoAlbum{}
	for _, album := range allAlbums() {
		if album.ArtistID == artistID {
			albums = append(albums, album)
		}
	}
	sort.SliceStable(albums, func(i, j int) bool {
		return albums[i].ReleaseDate > albums[j].ReleaseDate
	})
	mdiscPage(w, albums, queryInt(r, "page", 1), "No albums for this artist yet.")
}

// mdiscArtworkHandler is the cover relay: the client builds this path from
// the album's identity and never dereferences a provider URL itself.
func mdiscArtworkHandler(w http.ResponseWriter, r *http.Request) {
	if !mdiscAuthorize(w, r) {
		return
	}
	mbid, _ := lidCanonicalForeignID(chi.URLParam(r, "mbid"))
	album, ok := albumByForeignID(mbid)
	if !ok {
		writeErr(w, http.StatusNotFound, "artwork not found")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(lidCoverPNG(album.ID))
}
