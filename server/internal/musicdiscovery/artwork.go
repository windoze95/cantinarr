package musicdiscovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
)

func artworkRedirect(req *http.Request, via []*http.Request) error {
	host := strings.ToLower(req.URL.Hostname())
	if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.User != nil ||
		(req.URL.Port() != "" && req.URL.Port() != "443") ||
		!(host == "coverartarchive.org" || host == "archive.org" || strings.HasSuffix(host, ".archive.org")) {
		return errors.New("artwork redirect is outside the artwork providers")
	}
	return nil
}

func newArtworkClient() *http.Client {
	return &http.Client{Transport: httpx.External(), Timeout: 15 * time.Second, CheckRedirect: artworkRedirect}
}

// Only a validated release-group ID constructs the origin URL. No upstream
// URL from a feed, metadata field, or caller is ever dereferenced.
func (s *Service) Artwork(ctx context.Context, id string) ([]byte, error) {
	if !validID(id) {
		return nil, errors.New("invalid MusicBrainz release-group ID")
	}
	return s.cache.get(ctx, "art:"+id, 24*time.Hour, func(ctx context.Context) ([]byte, error) {
		for attempt := 0; attempt < 2; attempt++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://coverartarchive.org/release-group/"+id+"/front-500", nil)
			if err != nil {
				return nil, errUnavailable
			}
			req.Header.Set("User-Agent", userAgent)
			resp, err := s.art.Do(req)
			if err != nil {
				return nil, errUnavailable
			}
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
			resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				return []byte{}, nil
			}
			if resp.StatusCode >= 500 && attempt == 0 {
				if err := pause(ctx, time.Second); err != nil {
					return nil, err
				}
				continue
			}
			if resp.StatusCode != http.StatusOK || readErr != nil || len(data) > 2<<20 {
				return nil, errUnavailable
			}
			switch http.DetectContentType(data) {
			case "image/jpeg", "image/png", "image/webp", "image/gif":
				return data, nil
			default:
				return nil, errors.New("invalid artwork response")
			}
		}
		return nil, errUnavailable
	})
}
