package proxy

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
)

// Kids accounts on the arr proxy. A requester reads Radarr and Sonarr
// through here — the whole library, the queue, history, wanted, calendar —
// and the app's Movies and TV tabs build their library rows from those
// reads. For a kids account every such body is cut to the records the
// account may see, decided from the record's own certification and genre
// names (no TMDB lookups: a library page is hundreds of titles and lands
// on the home screen). Records that carry no parent record to judge are
// dropped, a single blocked record is a 404, and a body the gate cannot
// read is the proxy's opaque 502. Chaptarr and Lidarr carry no ratings and
// are not gated: those modules are granted per user on purpose.

var (
	// errContentPolicyUnfilterable is a gated read whose body the gate could
	// not judge (not JSON, an unexpected shape, a resource with no rule).
	errContentPolicyUnfilterable = errors.New("content limits could not be applied to the upstream response")
	// errContentPolicyBlocked is a single record the account may not see.
	errContentPolicyBlocked = errors.New("record hidden by content limits")
)

// arrGate is one request's gate: the service type and the compiled policy.
type arrGate struct {
	serviceType string
	ev          *contentpolicy.Evaluator
}

// SetContentPolicy wires the kids-account service.
func (h *Handler) SetContentPolicy(svc *contentpolicy.Service) { h.contentPolicy = svc }

// childGate resolves the caller's gate: nil for admins, non-children, and
// services that carry no ratings. A kids account whose policy cannot be
// read or ranked is an error, answered 503 by the caller.
func (h *Handler) childGate(r *http.Request, serviceType string) (*arrGate, error) {
	if serviceType != "radarr" && serviceType != "sonarr" {
		return nil, nil
	}
	ctx := r.Context()
	var (
		userID int64
		role   string
	)
	if user := auth.GetUserFromContext(ctx); user != nil {
		if !user.Child || user.Role == auth.RoleAdmin {
			return nil, nil
		}
		userID, role = user.ID, user.Role
	} else if claims := auth.GetClaims(ctx); claims != nil {
		if claims.Role == auth.RoleAdmin {
			return nil, nil
		}
		if h.contentPolicy == nil {
			return nil, nil
		}
		userID, role = claims.UserID, claims.Role
	} else {
		return nil, nil
	}
	if h.contentPolicy == nil {
		return nil, errors.New("content policy service is not wired")
	}
	policy, err := h.contentPolicy.PolicyFor(userID, role)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, nil
	}
	ev, err := h.contentPolicy.EvaluatorFor(ctx, policy)
	if err != nil {
		return nil, err
	}
	return &arrGate{serviceType: serviceType, ev: ev}, nil
}

// arrResource classifies an arr API path after its version marker.
type arrResource struct {
	name   string // movie, series, queue, history, wanted, calendar, episode
	single bool   // /{resource}/{id}
}

// resourceOf reads the resource from an outbound path (which may carry the
// instance's own base path before /api/v3).
func resourceOf(requestPath string) (arrResource, bool) {
	suffix, ok := arrAPIPathSuffix(requestPath)
	if !ok {
		return arrResource{}, false
	}
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	// parts[0] = "api", parts[1] = "v3"
	if len(parts) < 3 {
		return arrResource{}, false
	}
	name := parts[2]
	rest := parts[3:]
	switch name {
	case "wanted":
		if len(rest) == 1 {
			return arrResource{name: name}, true
		}
	case "movie", "series", "episode":
		if len(rest) == 0 {
			return arrResource{name: name}, true
		}
		if len(rest) == 1 && isDigits(rest[0]) {
			return arrResource{name: name, single: true}, true
		}
	case "queue", "history", "calendar":
		if len(rest) == 0 {
			return arrResource{name: name}, true
		}
	}
	return arrResource{}, false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// forceIncludes asks the arr to embed the parent record the gate judges by:
// a queue row's movie, a history row's series, a calendar episode's series.
// Without the embed there is nothing to judge and the row would be dropped.
func (g *arrGate) forceIncludes(req *http.Request) {
	res, ok := resourceOf(req.URL.Path)
	if !ok {
		return
	}
	var key string
	switch g.serviceType {
	case "radarr":
		if res.name == "queue" || res.name == "history" {
			key = "includeMovie"
		}
	case "sonarr":
		switch res.name {
		case "queue", "history", "wanted", "calendar", "episode":
			key = "includeSeries"
		}
	}
	if key == "" {
		return
	}
	q := req.URL.Query()
	q.Set(key, "true")
	req.URL.RawQuery = q.Encode()
}

// transformFor returns the body transform for one response, or nil when the
// response carries no titles (an arr error, no body).
func (g *arrGate) transformFor(resp *http.Response) func(any) (any, error) {
	if g == nil || resp.Request == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	res, ok := resourceOf(resp.Request.URL.Path)
	if !ok {
		return func(any) (any, error) { return nil, errContentPolicyUnfilterable }
	}
	return func(value any) (any, error) { return g.transform(res, value) }
}

// mediaType is the policy's media type for the service.
func (g *arrGate) mediaType() string {
	if g.serviceType == "sonarr" {
		return contentpolicy.MediaTV
	}
	return contentpolicy.MediaMovie
}

// parentOf returns the record the policy judges for one element: the
// element itself for library and wanted-movie rows, its embedded movie or
// series for the rest. nil when the embed is missing.
func (g *arrGate) parentOf(res arrResource, element map[string]any) map[string]any {
	switch g.serviceType {
	case "radarr":
		switch res.name {
		case "movie", "calendar", "wanted":
			return element
		case "queue", "history":
			parent, _ := element["movie"].(map[string]any)
			return parent
		}
	case "sonarr":
		switch res.name {
		case "series":
			return element
		case "queue", "history", "wanted", "calendar", "episode":
			parent, _ := element["series"].(map[string]any)
			return parent
		}
	}
	return nil
}

// allows judges one parent record by its own certification and genres.
func (g *arrGate) allows(parent map[string]any) bool {
	if parent == nil {
		return false
	}
	certification, _ := parent["certification"].(string)
	var genres []string
	if raw, ok := parent["genres"].([]any); ok {
		for _, entry := range raw {
			if name, ok := entry.(string); ok {
				genres = append(genres, name)
			}
		}
	}
	return g.ev.AllowsArrRecord(g.mediaType(), certification, genres)
}

// transform cuts a decoded body to the records the account may see.
func (g *arrGate) transform(res arrResource, value any) (any, error) {
	if res.single {
		element, ok := value.(map[string]any)
		if !ok {
			return nil, errContentPolicyUnfilterable
		}
		if !g.allows(g.parentOf(res, element)) {
			return nil, errContentPolicyBlocked
		}
		return value, nil
	}
	switch body := value.(type) {
	case []any:
		return g.filterElements(res, body), nil
	case map[string]any:
		records, ok := body["records"].([]any)
		if !ok {
			return nil, errContentPolicyUnfilterable
		}
		body["records"] = g.filterElements(res, records)
		return body, nil
	}
	return nil, errContentPolicyUnfilterable
}

func (g *arrGate) filterElements(res arrResource, elements []any) []any {
	kept := make([]any, 0, len(elements))
	for _, raw := range elements {
		element, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if g.allows(g.parentOf(res, element)) {
			kept = append(kept, element)
		}
	}
	return kept
}

// replaceWithHiddenRecord answers a blocked single record as not found,
// the same answer the arr gives for a record that does not exist.
func replaceWithHiddenRecord(resp *http.Response) {
	if resp.Body != nil {
		resp.Body.Close()
	}
	const body = `{"error":"not found"}`
	resp.StatusCode = http.StatusNotFound
	resp.Status = http.StatusText(http.StatusNotFound)
	resp.Header = http.Header{"Content-Type": []string{"application/json"}}
	resp.Header.Set("Content-Length", itoa(len(body)))
	resp.Body = readCloser(body)
	resp.ContentLength = int64(len(body))
	resp.TransferEncoding = nil
}

func itoa(n int) string { return strconv.Itoa(n) }

func readCloser(body string) io.ReadCloser { return io.NopCloser(strings.NewReader(body)) }
