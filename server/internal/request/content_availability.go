package request

import (
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/lidarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// One import batch bypasses pre-import caches and shares the resulting source
// reads across all saved requests. Errors are cached within the batch too.
type contentAvailabilityRead struct {
	freshLibrary, freshEpisodes bool
	movies                      map[string]map[int]bool
	books                       map[string]*bookLiveProjection
	music                       map[string]*musicLiveProjection
	errors                      map[string]error
	tv                          map[string]contentTVSnapshot
}

type contentTVSnapshot struct {
	Series   *sonarr.Series   `json:"series"`
	Episodes []sonarr.Episode `json:"episodes"`
}

func (r *contentAvailabilityRead) rememberError(key string, err error) {
	if r.errors == nil {
		r.errors = map[string]error{}
	}
	r.errors[key] = err
}

func (s *Service) contentMovieAvailable(instanceID string, tmdbID int, read *contentAvailabilityRead) (bool, error) {
	if !read.freshLibrary {
		digest, known := s.movieAvailabilityDigestForInstance(instanceID)
		if !known {
			return false, fmt.Errorf("movie library could not be read")
		}
		return digest[tmdbID].HasFile, nil
	}
	if err := read.errors[instanceID]; err != nil {
		return false, err
	}
	if files, ok := read.movies[instanceID]; ok {
		return files[tmdbID], nil
	}
	client, err := s.registry.GetRadarrClient(instanceID)
	if err != nil {
		read.rememberError(instanceID, err)
		return false, err
	}
	movies, err := client.GetMovies()
	if err != nil {
		read.rememberError(instanceID, err)
		return false, err
	}
	files := map[int]bool{}
	for _, movie := range movies {
		files[movie.TmdbID] = movie.HasFile
	}
	if read.movies == nil {
		read.movies = map[string]map[int]bool{}
	}
	read.movies[instanceID] = files
	return files[tmdbID], nil
}

func (s *Service) contentBookProjection(client *chaptarr.Client, instanceID string, read *contentAvailabilityRead) (*bookLiveProjection, error) {
	if !read.freshLibrary {
		return s.liveBookProjectionCached(client, instanceID)
	}
	if err := read.errors[instanceID]; err != nil {
		return nil, err
	}
	if projection := read.books[instanceID]; projection != nil {
		return projection, nil
	}
	projection, err := buildBookLiveProjection(client)
	if err != nil {
		read.rememberError(instanceID, err)
		return nil, err
	}
	if read.books == nil {
		read.books = map[string]*bookLiveProjection{}
	}
	read.books[instanceID] = projection
	return projection, nil
}

func (s *Service) contentMusicProjection(client *lidarr.Client, instanceID string, read *contentAvailabilityRead) (*musicLiveProjection, error) {
	if !read.freshLibrary {
		return s.liveMusicProjectionCached(client, instanceID)
	}
	if err := read.errors[instanceID]; err != nil {
		return nil, err
	}
	if projection := read.music[instanceID]; projection != nil {
		return projection, nil
	}
	projection, err := buildMusicLiveProjection(client)
	if err != nil {
		read.rememberError(instanceID, err)
		return nil, err
	}
	if read.music == nil {
		read.music = map[string]*musicLiveProjection{}
	}
	read.music[instanceID] = projection
	return projection, nil
}
