package musicdiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Service struct {
	mb, lb *provider
	art    *http.Client
	cache  memo
	now    func() time.Time
}

func NewService() *Service {
	return &Service{mb: newProvider("https://musicbrainz.org/ws/2"),
		lb: newProvider("https://api.listenbrainz.org/1"), art: newArtworkClient(), now: time.Now}
}

func (s *Service) Feed(ctx context.Context, feed, period, genreID string, page int) ([]byte, error) {
	today := s.now().UTC().Truncate(24 * time.Hour)
	ttl := time.Hour
	if feed == "genre" {
		ttl = 6 * time.Hour
	}
	key := fmt.Sprintf("feed:%s:%s:%s:%d:%s", feed, period, genreID, page, today.Format(time.DateOnly))
	return s.cache.get(ctx, key, ttl, func(ctx context.Context) ([]byte, error) {
		var result Page
		var err error
		switch feed {
		case "popular":
			result, err = s.popular(ctx, period, page)
		case "new-releases":
			result, err = s.fresh(ctx, page, today)
		case "genre":
			genre, _ := genreByID(genreID)
			result, err = s.genre(ctx, genre, page)
		}
		if err != nil {
			return nil, err
		}
		if len(result.Results) == 0 {
			result.EmptyMessage = "No albums or EPs found on this page of " + result.Scope + "."
			if result.NextPage > 0 {
				result.EmptyMessage += " More provider results are available on the next page."
			}
		}
		return json.Marshal(result)
	})
}
