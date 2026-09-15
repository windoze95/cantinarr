// hardcover.go — the Hardcover connection a Chaptarr instance reads its
// trending list through, and the trending feed itself. Two ways to connect:
// an API token typed in, or a device sign-in. Either way the credential is
// write-only — the demo answers a method and a presence, never a value.
//
// The trending list is the only live book discovery route left; Open Library
// discovery retired and its former routes answer `catalog_retired` so an
// older app is told what happened rather than shown an empty shelf.
//
// Prefix: hc…
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	// hcRetiredCode/hcRetiredMessage are verbatim from the real server —
	// older apps display the message.
	hcRetiredCode    = "catalog_retired"
	hcRetiredMessage = "Open Library book discovery has retired. Search your Chaptarr library and select the book to request."
)

// hcConnection is one stored Hardcover credential. Token is never echoed.
type hcConnection struct {
	ID       string
	Method   string // "api_token" | "oauth"
	Label    string
	Revision int
}

var (
	hcMu sync.Mutex

	// hcByInstance is chaptarr instance id -> its connection, or absent.
	hcByInstance = map[string]*hcConnection{}

	// hcFlows is the in-flight device sign-ins, flow id -> state.
	hcFlows = map[string]*hcFlow{}

	hcNextFlow int
)

type hcFlow struct {
	InstanceID string
	UserCode   string
	StartedAt  time.Time
	Cancelled  bool
}

func init() {
	// The seeded Chaptarr instance ships connected by API token, so the book
	// trending row has something to show without an admin doing setup first.
	hcByInstance[instChaptarr] = &hcConnection{
		ID: "hc-conn-demo0001", Method: "api_token", Label: "Demo Hardcover token", Revision: 1,
	}
}

func registerHardcover(r chi.Router) {
	admin := r.With(requireAdmin)
	admin.Get("/instances/{instanceID}/hardcover", hcStatusHandler)
	admin.Put("/instances/{instanceID}/hardcover", hcSaveHandler)
	admin.Delete("/instances/{instanceID}/hardcover", hcClearHandler)
	admin.Post("/instances/{instanceID}/hardcover/device/begin", hcDeviceBeginHandler)
	admin.Get("/instances/{instanceID}/hardcover/device/{flowID}", hcDeviceCheckHandler)
	admin.Delete("/instances/{instanceID}/hardcover/device/{flowID}", hcDeviceCancelHandler)
	admin.Post("/instances/{instanceID}/hardcover/apply", hcApplyHandler)
}

// registerBookDiscovery mounts the book discovery surface: the live trending
// feed, its cover relay, and the retired Open Library routes.
func registerBookDiscovery(r chi.Router) {
	// Static and multi-segment paths first — both must win over {feed}.
	r.Get("/discover/books/trending", hcTrendingHandler)
	r.Get("/discover/books/images/*", hcCoverHandler)
	r.Get("/discover/books/search", hcRetiredHandler)
	r.Get("/discover/books/{feed}", hcRetiredHandler)
	r.Get("/genres/book", hcRetiredHandler)
	r.Get("/media/book/{workId}", hcRetiredHandler)
	r.Get("/media/book/{workId}/request-target", hcRetiredHandler)
}

// ─── Connection ─────────────────────────────────────────

// hcChaptarrInstance resolves the path instance and refuses anything that is
// not a Chaptarr: Hardcover is a book-metadata provider, nothing else.
func hcChaptarrInstance(w http.ResponseWriter, r *http.Request) (*DemoInstance, bool) {
	inst := instanceByID(chi.URLParam(r, "instanceID"))
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return nil, false
	}
	if inst.ServiceType != serviceChaptarr {
		writeErr(w, http.StatusBadRequest, "Hardcover connects to a Chaptarr instance")
		return nil, false
	}
	return inst, true
}

// hcStatusView is what the instance editor renders. `supported` says the
// server knows the feature at all; `configured` says this instance holds a
// credential.
func hcStatusView(instanceID string) map[string]any {
	conn := hcByInstance[instanceID]
	out := map[string]any{
		"supported":          true,
		"configured":         conn != nil,
		"oauth_available":    true,
		"method":             "none",
		"reconnect_required": false,
		"connection_id":      "",
		"revision":           0,
	}
	if conn != nil {
		out["method"] = conn.Method
		out["connection_id"] = conn.ID
		out["revision"] = conn.Revision
	}
	return out
}

func hcStatusHandler(w http.ResponseWriter, r *http.Request) {
	inst, ok := hcChaptarrInstance(w, r)
	if !ok {
		return
	}
	hcMu.Lock()
	defer hcMu.Unlock()
	writeJSON(w, http.StatusOK, hcStatusView(inst.ID))
}

func hcSaveHandler(w http.ResponseWriter, r *http.Request) {
	inst, ok := hcChaptarrInstance(w, r)
	if !ok {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
		writeErr(w, http.StatusBadRequest, "invalid Hardcover token")
		return
	}
	if len(strings.TrimSpace(body.Token)) < 8 {
		// The real server verifies the token against Hardcover and refuses a
		// rejected one, leaving any previous connection in place.
		writeErr(w, http.StatusBadRequest, "Hardcover did not accept that token")
		return
	}
	hcMu.Lock()
	defer hcMu.Unlock()
	previous := hcByInstance[inst.ID]
	revision := 1
	if previous != nil {
		revision = previous.Revision + 1
	}
	hcByInstance[inst.ID] = &hcConnection{
		ID: "hc-conn-" + randomHex(4), Method: "api_token",
		Label: "Hardcover API token", Revision: revision,
	}
	writeJSON(w, http.StatusOK, hcStatusView(inst.ID))
}

func hcClearHandler(w http.ResponseWriter, r *http.Request) {
	inst, ok := hcChaptarrInstance(w, r)
	if !ok {
		return
	}
	hcMu.Lock()
	defer hcMu.Unlock()
	delete(hcByInstance, inst.ID)
	writeJSON(w, http.StatusOK, hcStatusView(inst.ID))
}

// ─── Device sign-in ─────────────────────────────────────

// hcFlowView is the safe device-flow metadata: a user code and where to type
// it. The provider's device code never crosses the server boundary.
func hcFlowView(id string, f *hcFlow, status string) map[string]any {
	out := map[string]any{
		"flow_id":  id,
		"status":   status,
		"interval": 5,
	}
	if status == "pending" {
		out["user_code"] = f.UserCode
		out["verification_uri"] = "https://hardcover.app/link"
		out["expires_at"] = f.StartedAt.Add(10 * time.Minute).UTC().Format(time.RFC3339)
	}
	if conn := hcByInstance[f.InstanceID]; status == "connected" && conn != nil {
		out["connection_id"] = conn.ID
	}
	return out
}

func hcDeviceBeginHandler(w http.ResponseWriter, r *http.Request) {
	inst, ok := hcChaptarrInstance(w, r)
	if !ok {
		return
	}
	hcMu.Lock()
	defer hcMu.Unlock()
	hcNextFlow++
	id := fmt.Sprintf("hc-flow-%d", hcNextFlow)
	f := &hcFlow{InstanceID: inst.ID, UserCode: strings.ToUpper(randomHex(3)), StartedAt: time.Now()}
	hcFlows[id] = f
	writeJSON(w, http.StatusOK, hcFlowView(id, f, "pending"))
}

// hcDeviceCheckHandler completes the sign-in after a few seconds of polling —
// the demo's stand-in for somebody typing the code on hardcover.app. It never
// contacts Hardcover.
func hcDeviceCheckHandler(w http.ResponseWriter, r *http.Request) {
	inst, ok := hcChaptarrInstance(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "flowID")
	hcMu.Lock()
	defer hcMu.Unlock()
	f := hcFlows[id]
	if f == nil || f.InstanceID != inst.ID {
		writeErr(w, http.StatusNotFound, "that sign-in is no longer waiting")
		return
	}
	if f.Cancelled {
		writeJSON(w, http.StatusOK, hcFlowView(id, f, "cancelled"))
		return
	}
	if time.Since(f.StartedAt) < 8*time.Second {
		writeJSON(w, http.StatusOK, hcFlowView(id, f, "pending"))
		return
	}
	previous := hcByInstance[inst.ID]
	revision := 1
	if previous != nil {
		revision = previous.Revision + 1
	}
	hcByInstance[inst.ID] = &hcConnection{
		ID: "hc-conn-" + randomHex(4), Method: "oauth",
		Label: "Hardcover account", Revision: revision,
	}
	delete(hcFlows, id)
	writeJSON(w, http.StatusOK, hcFlowView(id, f, "connected"))
}

func hcDeviceCancelHandler(w http.ResponseWriter, r *http.Request) {
	inst, ok := hcChaptarrInstance(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "flowID")
	hcMu.Lock()
	defer hcMu.Unlock()
	f := hcFlows[id]
	if f == nil || f.InstanceID != inst.ID {
		writeErr(w, http.StatusNotFound, "that sign-in is no longer waiting")
		return
	}
	f.Cancelled = true
	view := hcFlowView(id, f, "cancelled")
	delete(hcFlows, id)
	writeJSON(w, http.StatusOK, view)
}

// hcApplyHandler shares one connection with the exact instances (and
// revisions) the admin was shown. Each target is independent: a stale
// revision fails that row alone.
func hcApplyHandler(w http.ResponseWriter, r *http.Request) {
	source, ok := hcChaptarrInstance(w, r)
	if !ok {
		return
	}
	var body struct {
		ConnectionID string `json:"connection_id"`
		Instances    []struct {
			InstanceID string `json:"instance_id"`
			Revision   int    `json:"revision"`
		} `json:"instances"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil ||
		body.ConnectionID == "" || len(body.Instances) == 0 || len(body.Instances) > 100 {
		writeErr(w, http.StatusBadRequest,
			"provide a connection ID and 1 to 100 explicit instances with their revisions")
		return
	}
	hcMu.Lock()
	defer hcMu.Unlock()
	from := hcByInstance[source.ID]
	if from == nil || from.ID != body.ConnectionID {
		writeErr(w, http.StatusConflict, "that connection is no longer available")
		return
	}
	results := []map[string]any{}
	for _, target := range body.Instances {
		inst := instanceByID(target.InstanceID)
		if inst == nil || inst.ServiceType != serviceChaptarr {
			results = append(results, map[string]any{
				"instance_id": target.InstanceID, "applied": false,
				"error": "that instance is not a Chaptarr library",
			})
			continue
		}
		current := 0
		if existing := hcByInstance[inst.ID]; existing != nil {
			current = existing.Revision
		}
		if target.Revision != current {
			results = append(results, map[string]any{
				"instance_id": inst.ID, "applied": false,
				"error": "that instance changed while you were choosing; reload and try again",
			})
			continue
		}
		hcByInstance[inst.ID] = &hcConnection{
			ID: from.ID, Method: from.Method, Label: from.Label, Revision: current + 1,
		}
		results = append(results, map[string]any{"instance_id": inst.ID, "applied": true})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// ─── Retired Open Library routes ────────────────────────

func hcRetiredHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusGone, map[string]string{
		"code":        hcRetiredCode,
		"error":       hcRetiredMessage,
		"message":     hcRetiredMessage,
		"search_path": "/dashboard/books",
	})
}

// ─── Trending (Hardcover) ───────────────────────────────

// hcTrendingSeed is one row of the demo's Hardcover trending list: the
// Hardcover id the feed is keyed by, the seeded Chaptarr book it is the same
// work as, and the social numbers Hardcover states beside it.
type hcTrendingSeed struct {
	HardcoverID  int
	ChaptarrFID  string
	ISBN13       string
	Rating       float64
	RatingsCount int
	ReadersCount int
}

// hcTrending is the fixed trending list. Real public-domain works, keyed by
// invented Hardcover ids: the demo never reads Hardcover, so these numbers
// are the demo's own, not a claim about the real site's rankings.
var hcTrending = []hcTrendingSeed{
	{440001, "1885", "9780141439518", 4.3, 41280, 98210},
	{440002, "17245", "9780141439846", 4.0, 23140, 51880},
	{440003, "18490", "9780141439471", 3.9, 31770, 74320},
	{440004, "2493", "9780141439976", 4.1, 12480, 29940},
	{440005, "295", "9780141321004", 3.8, 9760, 21430},
	{440006, "13023", "9780141439761", 4.2, 18930, 44870},
	{440007, "3590", "9780140437713", 4.5, 27340, 62110},
	{440008, "153747", "9780142437247", 3.7, 15620, 38290},
}

// hcTrendingByFID indexes the list by the Chaptarr foreign id so the library
// digest can state the Hardcover identity key for a book it owns.
var hcTrendingByFID = map[string]*hcTrendingSeed{}

func init() {
	for i := range hcTrending {
		seed := &hcTrending[i]
		hcTrendingByFID[seed.ChaptarrFID] = seed
		// A trending card's tap opens /detail/book/hc:<id>, so that id has to
		// resolve to the same Chaptarr record the library already holds —
		// the alias-to-canonical-sibling rule the book lookup already follows.
		if b := chapBooksByFID[seed.ChaptarrFID]; b != nil {
			chapBooksByFID[hcForeignID(seed.HardcoverID)] = b
		}
	}
}

func hcForeignID(hardcoverID int) string { return fmt.Sprintf("hc:%d", hardcoverID) }

// hcIdentityKeys are the typed identity keys a library row states so an
// owned-book badge is an exact key match rather than a title guess. Empty for
// a book no external provider knows, which is the honest answer.
func hcIdentityKeys(foreignID string) []string {
	seed := hcTrendingByFID[foreignID]
	if seed == nil {
		return nil
	}
	keys := []string{fmt.Sprintf("hc-book:%d", seed.HardcoverID)}
	if seed.ISBN13 != "" {
		keys = append(keys, "isbn:"+seed.ISBN13)
	}
	return keys
}

// hcCoverPath is the assets.hardcover.app path a trending cover is published
// at. The web client rewrites this host to the relay below; native clients
// hit the real CDN, where the demo's invented paths do not exist, and fall
// back to the book placeholder.
func hcCoverPath(hardcoverID int) string {
	return fmt.Sprintf("editions/demo-%d.png", hardcoverID)
}

// hcTrendingHandler is the one live book discovery feed. `connected` is the
// fact the row branches on: false offers an admin the way to Settings rather
// than rendering an empty shelf as if Hardcover had nothing.
func hcTrendingHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	// The Chaptarr instance is authorized before the feed is built and its
	// connection read — the same order, and the same refusals, the real
	// trending handler uses: a requester with no grant is told books are not
	// available to them, never handed a server error.
	instanceID, ok := hcAuthorize(w, u, strings.TrimSpace(r.URL.Query().Get("instance_id")))
	if !ok {
		return
	}

	hcMu.Lock()
	connected := instanceID != "" && hcByInstance[instanceID] != nil
	hcMu.Unlock()

	books := []map[string]any{}
	if connected {
		for i := range hcTrending {
			seed := &hcTrending[i]
			b := chapBooksByFID[seed.ChaptarrFID]
			if b == nil {
				continue
			}
			meta := chapMetaByFID[seed.ChaptarrFID]
			row := map[string]any{
				"hardcover_id":  seed.HardcoverID,
				"foreign_id":    hcForeignID(seed.HardcoverID),
				"title":         b.Title,
				"authors":       []string{b.AuthorName},
				"description":   b.Overview,
				"rating":        seed.Rating,
				"ratings_count": seed.RatingsCount,
				"readers_count": seed.ReadersCount,
				"image_url":     "https://assets.hardcover.app/" + hcCoverPath(seed.HardcoverID),
				"isbn13s":       []string{seed.ISBN13},
			}
			if b.Year > 0 {
				row["year"] = b.Year
			}
			if meta != nil && meta.SeriesTitle != "" {
				name, position := chapParseSeriesTitle(meta.SeriesTitle)
				row["series"] = name
				if n, err := strconv.ParseFloat(position, 64); err == nil && n > 0 {
					row["series_position"] = n
				}
			}
			books = append(books, row)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"instance_id": instanceID,
		"connected":   connected,
		"source":      "Hardcover",
		"scope":       "Trending on Hardcover right now",
		"books":       books,
	})
}

// hcAuthorize resolves and authorizes the Chaptarr instance the feed is
// scoped to. Unlike music, books have NO admin-browses-without-a-library
// bypass: the trending list is read through a Chaptarr instance's own
// Hardcover connection, so with no instance there is nothing to read and
// everyone — admins included — is told so.
func hcAuthorize(w http.ResponseWriter, u *DemoUser, explicit string) (string, bool) {
	if explicit == "" {
		if inst := effectiveInstanceFor(u, serviceChaptarr); inst != nil {
			explicit = inst.ID
		} else {
			writeErr(w, http.StatusForbidden, "books are not available to you")
			return "", false
		}
	}
	inst := instanceByID(explicit)
	if inst == nil || inst.ServiceType != serviceChaptarr {
		writeErr(w, http.StatusForbidden, "books are not available to you")
		return "", false
	}
	if u == nil || u.Role != roleAdmin {
		held := false
		for _, id := range visibleInstanceIDs(u, serviceChaptarr) {
			if id == inst.ID {
				held = true
				break
			}
		}
		if !held {
			writeErr(w, http.StatusForbidden, "books are not available to you")
			return "", false
		}
	}
	return inst.ID, true
}

// hcCoverHandler stands in for the Hardcover cover CDN relay. The real one
// forwards bytes from assets.hardcover.app; the demo draws its own, so no
// request leaves the building.
func hcCoverHandler(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "*")
	id := 0
	if _, err := fmt.Sscanf(path, "editions/demo-%d.png", &id); err != nil || id <= 0 {
		writeErr(w, http.StatusNotFound, "image not found")
		return
	}
	seed := 0
	for i := range hcTrending {
		if hcTrending[i].HardcoverID == id {
			if b := chapBooksByFID[hcTrending[i].ChaptarrFID]; b != nil {
				seed = b.CanonicalBookID()
			}
			break
		}
	}
	if seed == 0 {
		writeErr(w, http.StatusNotFound, "image not found")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(chapCoverPNG(seed))
}
