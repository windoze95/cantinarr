package tmdb

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const baseURL = "https://api.themoviedb.org/3"

type Client struct {
	accessToken string
	httpClient  *http.Client
}

func NewClient(accessToken string) *Client {
	return &Client{
		accessToken: accessToken,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// doGet creates an authenticated GET request with the Bearer token.
func (c *Client) doGet(reqURL string) (*http.Response, error) {
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")
	return c.httpClient.Do(req)
}

type ExternalIDs struct {
	IMDBID *string `json:"imdb_id"`
	TVDBID *int    `json:"tvdb_id"`
	TMDbID int     `json:"id"`
}

type MovieDetails struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	ReleaseDate string  `json:"release_date"`
	IMDBID      string  `json:"imdb_id"`
	PosterPath  string  `json:"poster_path,omitempty"`
	Overview    string  `json:"overview,omitempty"`
	VoteAverage float64 `json:"vote_average,omitempty"`
}

type TVDetails struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	FirstAir    string  `json:"first_air_date"`
	PosterPath  string  `json:"poster_path,omitempty"`
	Overview    string  `json:"overview,omitempty"`
	VoteAverage float64 `json:"vote_average,omitempty"`
}

// DoGetRaw fetches a TMDB API path and returns the raw JSON bytes.
// Used by the discover handler for passthrough caching.
func (c *Client) DoGetRaw(path string, params url.Values) ([]byte, error) {
	u := fmt.Sprintf("%s%s?language=en-US", baseURL, path)
	if params != nil {
		u += "&" + params.Encode()
	}
	resp, err := c.doGet(u)
	if err != nil {
		return nil, fmt.Errorf("tmdb raw request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB API returned status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) GetTVExternalIDs(tmdbID int) (*ExternalIDs, error) {
	u := fmt.Sprintf("%s/tv/%d/external_ids", baseURL, tmdbID)
	resp, err := c.doGet(u)
	if err != nil {
		return nil, fmt.Errorf("request external IDs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB API returned status %d", resp.StatusCode)
	}

	var ids ExternalIDs
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		return nil, fmt.Errorf("decode external IDs: %w", err)
	}
	return &ids, nil
}

func (c *Client) GetMovieDetails(tmdbID int) (*MovieDetails, error) {
	u := fmt.Sprintf("%s/movie/%d", baseURL, tmdbID)
	resp, err := c.doGet(u)
	if err != nil {
		return nil, fmt.Errorf("request movie details: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB API returned status %d", resp.StatusCode)
	}

	var movie MovieDetails
	if err := json.NewDecoder(resp.Body).Decode(&movie); err != nil {
		return nil, fmt.Errorf("decode movie details: %w", err)
	}
	return &movie, nil
}

func (c *Client) GetTVDetails(tmdbID int) (*TVDetails, error) {
	u := fmt.Sprintf("%s/tv/%d", baseURL, tmdbID)
	resp, err := c.doGet(u)
	if err != nil {
		return nil, fmt.Errorf("request TV details: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB API returned status %d", resp.StatusCode)
	}

	var tv TVDetails
	if err := json.NewDecoder(resp.Body).Decode(&tv); err != nil {
		return nil, fmt.Errorf("decode TV details: %w", err)
	}
	return &tv, nil
}

// SearchResult represents a movie or TV show from search results.
type SearchResult struct {
	ID           int     `json:"id"`
	Title        string  `json:"title,omitempty"`
	Name         string  `json:"name,omitempty"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path,omitempty"`
	ReleaseDate  string  `json:"release_date,omitempty"`
	FirstAirDate string  `json:"first_air_date,omitempty"`
	VoteAverage  float64 `json:"vote_average"`
	MediaType    string  `json:"media_type,omitempty"`
}

type searchResponse struct {
	Results []SearchResult `json:"results"`
}

type CollectionSearchResult struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	PosterPath   string `json:"poster_path,omitempty"`
	BackdropPath string `json:"backdrop_path,omitempty"`
}

type MovieCollection struct {
	ID           int            `json:"id"`
	Name         string         `json:"name"`
	Overview     string         `json:"overview"`
	PosterPath   string         `json:"poster_path,omitempty"`
	BackdropPath string         `json:"backdrop_path,omitempty"`
	Parts        []SearchResult `json:"parts"`
}

type collectionSearchResponse struct {
	Results []CollectionSearchResult `json:"results"`
}

func (c *Client) SearchMovies(query string) ([]SearchResult, error) {
	u := fmt.Sprintf("%s/search/movie?query=%s", baseURL, url.QueryEscape(query))
	resp, err := c.doGet(u)
	if err != nil {
		return nil, fmt.Errorf("search movies: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB API returned status %d", resp.StatusCode)
	}

	var result searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode search results: %w", err)
	}
	setSearchResultMediaType(result.Results, "movie")
	return result.Results, nil
}

func (c *Client) SearchMovieCollections(query string) ([]CollectionSearchResult, error) {
	u := fmt.Sprintf("%s/search/collection?query=%s", baseURL, url.QueryEscape(query))
	resp, err := c.doGet(u)
	if err != nil {
		return nil, fmt.Errorf("search movie collections: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB API returned status %d", resp.StatusCode)
	}

	var result collectionSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode collection search results: %w", err)
	}
	return result.Results, nil
}

func (c *Client) GetMovieCollection(collectionID int) (*MovieCollection, error) {
	u := fmt.Sprintf("%s/collection/%d", baseURL, collectionID)
	resp, err := c.doGet(u)
	if err != nil {
		return nil, fmt.Errorf("get movie collection: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB API returned status %d", resp.StatusCode)
	}

	var collection MovieCollection
	if err := json.NewDecoder(resp.Body).Decode(&collection); err != nil {
		return nil, fmt.Errorf("decode movie collection: %w", err)
	}
	setSearchResultMediaType(collection.Parts, "movie")
	sort.SliceStable(collection.Parts, func(i, j int) bool {
		left := collection.Parts[i].ReleaseDate
		right := collection.Parts[j].ReleaseDate
		if left == "" {
			return false
		}
		if right == "" {
			return true
		}
		return left < right
	})
	return &collection, nil
}

func (c *Client) SearchTV(query string) ([]SearchResult, error) {
	u := fmt.Sprintf("%s/search/tv?query=%s", baseURL, url.QueryEscape(query))
	resp, err := c.doGet(u)
	if err != nil {
		return nil, fmt.Errorf("search TV: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB API returned status %d", resp.StatusCode)
	}

	var result searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode search results: %w", err)
	}
	setSearchResultMediaType(result.Results, "tv")
	return result.Results, nil
}

func (c *Client) GetTrending(mediaType, timeWindow string) ([]SearchResult, error) {
	mediaType = normalizeTrendingMediaType(mediaType)
	timeWindow = normalizeTrendingTimeWindow(timeWindow)
	if mediaType == "all" {
		movies, err := c.getTrendingByType("movie", timeWindow)
		if err != nil {
			return nil, err
		}
		tv, err := c.getTrendingByType("tv", timeWindow)
		if err != nil {
			return nil, err
		}
		return balancedTrendingResults(movies, tv, 10), nil
	}
	return c.getTrendingByType(mediaType, timeWindow)
}

func (c *Client) getTrendingByType(mediaType, timeWindow string) ([]SearchResult, error) {
	u := fmt.Sprintf("%s/trending/%s/%s", baseURL, mediaType, timeWindow)
	resp, err := c.doGet(u)
	if err != nil {
		return nil, fmt.Errorf("get trending: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB API returned status %d", resp.StatusCode)
	}

	var result searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode trending results: %w", err)
	}
	setSearchResultMediaType(result.Results, mediaType)
	return result.Results, nil
}

func normalizeTrendingMediaType(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "movie", "movies":
		return "movie"
	case "tv", "show", "shows", "series":
		return "tv"
	default:
		return "all"
	}
}

func normalizeTrendingTimeWindow(timeWindow string) string {
	switch strings.ToLower(strings.TrimSpace(timeWindow)) {
	case "week":
		return "week"
	default:
		return "day"
	}
}

func setSearchResultMediaType(results []SearchResult, mediaType string) {
	if mediaType != "movie" && mediaType != "tv" {
		return
	}
	for i := range results {
		results[i].MediaType = mediaType
	}
}

func balancedTrendingResults(movies, tv []SearchResult, limit int) []SearchResult {
	if limit <= 0 {
		return nil
	}
	setSearchResultMediaType(movies, "movie")
	setSearchResultMediaType(tv, "tv")

	out := make([]SearchResult, 0, limit)
	for movieIndex, tvIndex := 0, 0; len(out) < limit && (movieIndex < len(movies) || tvIndex < len(tv)); {
		if movieIndex < len(movies) {
			out = append(out, movies[movieIndex])
			movieIndex++
			if len(out) == limit {
				break
			}
		}
		if tvIndex < len(tv) {
			out = append(out, tv[tvIndex])
			tvIndex++
		}
	}
	return out
}

func (c *Client) GetRecommendations(tmdbID int, mediaType string) ([]SearchResult, error) {
	u := fmt.Sprintf("%s/%s/%d/recommendations", baseURL, mediaType, tmdbID)
	resp, err := c.doGet(u)
	if err != nil {
		return nil, fmt.Errorf("get recommendations: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB API returned status %d", resp.StatusCode)
	}

	var result searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode recommendations: %w", err)
	}
	return result.Results, nil
}
