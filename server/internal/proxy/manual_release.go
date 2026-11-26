package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

const (
	manualReleasePrefix = "cantinarr:release:"
	manualReleaseTTL    = 15 * time.Minute // within the arrs' 30-minute search cache
	maxReleaseRefs      = 8192
	maxReleaseGUIDBytes = 16 << 20 // total across all searches
)

var errReleaseSearchTooLarge = errors.New("interactive release search exceeds reference limit")

type manualReleaseScope struct {
	userID      int64
	instanceID  string
	instanceURL string // the same instance ID can be edited to point at a different arr
}

func manualReleaseScopeFor(r *http.Request, inst *instance.Instance) (manualReleaseScope, bool) {
	if (inst.ServiceType != "radarr" && inst.ServiceType != "sonarr") ||
		strings.TrimSuffix(r.URL.Path, "/") != "/api/instances/"+inst.ID+"/api/v3/release" {
		return manualReleaseScope{}, false
	}
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		return manualReleaseScope{}, false
	}
	return manualReleaseScope{claims.UserID, inst.ID, inst.URL}, true
}

func isManualReleaseRef(guid string) bool {
	id, ok := strings.CutPrefix(guid, manualReleasePrefix)
	return ok && len(id) == 36 && uuid.Validate(id) == nil
}

type manualRelease struct {
	guid      string
	indexerID int64
}

type manualReleaseSearch struct {
	scope     manualReleaseScope
	releases  map[string]manualRelease
	guidBytes int
	expires   time.Time
}

// Keep exact selections in memory only. Evict whole searches, oldest first,
// so one large search cannot issue references it has already evicted.
type manualReleaseReferences struct {
	mu        sync.Mutex
	searches  []manualReleaseSearch
	count     int
	guidBytes int
}

func (s *manualReleaseReferences) protectSearch(scope manualReleaseScope, value any) error {
	rows, _ := value.([]any)
	search := manualReleaseSearch{scope: scope}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		guid, _ := row["guid"].(string)
		// An upstream ID in our reserved wire format needs escaping too.
		if sanitizeURLCredentials(guid) == guid && !isManualReleaseRef(guid) {
			continue
		}
		number, _ := row["indexerId"].(json.Number)
		indexerID, err := number.Int64()
		if err != nil || indexerID <= 0 {
			continue // not a usable arr selection; the normal scrub still runs
		}
		search.guidBytes += len(guid)
		if len(search.releases) >= maxReleaseRefs || search.guidBytes > maxReleaseGUIDBytes {
			return errReleaseSearchTooLarge
		}
		if search.releases == nil {
			search.releases = make(map[string]manualRelease)
		}
		ref := manualReleasePrefix + uuid.NewString()
		search.releases[ref] = manualRelease{guid, indexerID}
		row["guid"] = ref
	}
	if len(search.releases) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.searches) == 0 {
		time.AfterFunc(manualReleaseTTL, s.expire)
	}
	for s.count+len(search.releases) > maxReleaseRefs || s.guidBytes+search.guidBytes > maxReleaseGUIDBytes {
		s.dropOldest()
	}
	search.expires = time.Now().Add(manualReleaseTTL)
	s.searches = append(s.searches, search)
	s.count += len(search.releases)
	s.guidBytes += search.guidBytes
	return nil
}

func (s *manualReleaseReferences) dropOldest() {
	oldest := &s.searches[0]
	s.count -= len(oldest.releases)
	s.guidBytes -= oldest.guidBytes
	*oldest = manualReleaseSearch{}
	s.searches = s.searches[1:]
}

// A single timer releases expired credentials even if nobody searches again.
func (s *manualReleaseReferences) expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.searches) > 0 {
		if remaining := time.Until(s.searches[0].expires); remaining > 0 {
			time.AfterFunc(remaining, s.expire)
			return
		}
		s.dropOldest()
	}
	s.searches = nil
}

func (s *manualReleaseReferences) resolve(scope manualReleaseScope, ref string, indexerID int64) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, search := range s.searches {
		if search.scope == scope && now.Before(search.expires) {
			if release, ok := search.releases[ref]; ok && release.indexerID == indexerID {
				return release.guid, true
			}
		}
	}
	return "", false
}

func (s *manualReleaseReferences) prepareGrab(w http.ResponseWriter, r *http.Request, scope manualReleaseScope) bool {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Body == nil || mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return true
	}
	// The API router already bounds the request body and its read deadline.
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		enforcePrivateProxyCachePolicy(w.Header())
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, `{"error":"could not read release selection"}`, status)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var selected struct {
		GUID      string          `json:"guid"`
		IndexerID json.RawMessage `json:"indexerId"`
	}
	if json.Unmarshal(body, &selected) != nil || !isManualReleaseRef(selected.GUID) {
		return true // forward ordinary grabs byte for byte
	}
	var indexerID int64
	idErr := json.Unmarshal(selected.IndexerID, &indexerID)
	guid, ok := s.resolve(scope, selected.GUID, indexerID)
	if idErr != nil || !ok {
		enforcePrivateProxyCachePolicy(w.Header())
		http.Error(w, `{"error":"This release selection is no longer available. Search again and choose a release."}`, http.StatusGone)
		return false
	}

	// Some clients also send override fields. Preserve them, including their
	// numeric precision. The arr binds field names case-insensitively.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		http.Error(w, `{"error":"invalid release selection"}`, http.StatusBadRequest)
		return false
	}
	for name := range fields {
		if strings.EqualFold(name, "guid") || strings.EqualFold(name, "indexerId") {
			delete(fields, name)
		}
	}
	fields["guid"], _ = json.Marshal(guid)
	fields["indexerId"], _ = json.Marshal(indexerID)
	body, err = json.Marshal(fields)
	if err != nil {
		http.Error(w, `{"error":"invalid release selection"}`, http.StatusBadRequest)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Content-Length")
	r.TransferEncoding = nil
	r.GetBody = nil
	return true
}
