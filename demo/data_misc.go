// data_misc.go — genre tables, fictional watch providers, and Trakt list
// fixtures (D3 discover). All ported from the old demo.
package main

// discMovieGenres is the complete real TMDB movie genre table (18 entries).
var discMovieGenres = []DemoGenre{
	{28, "Action"}, {12, "Adventure"}, {16, "Animation"},
	{35, "Comedy"}, {80, "Crime"}, {99, "Documentary"},
	{18, "Drama"}, {10751, "Family"}, {14, "Fantasy"},
	{36, "History"}, {27, "Horror"}, {10402, "Music"},
	{9648, "Mystery"}, {10749, "Romance"}, {878, "Science Fiction"},
	{53, "Thriller"}, {10752, "War"}, {37, "Western"},
}

// discTVGenres is the complete real TMDB TV genre table (16 entries).
var discTVGenres = []DemoGenre{
	{10759, "Action & Adventure"}, {16, "Animation"},
	{35, "Comedy"}, {80, "Crime"}, {99, "Documentary"},
	{18, "Drama"}, {10751, "Family"}, {10762, "Kids"},
	{9648, "Mystery"}, {10763, "News"}, {10764, "Reality"},
	{10765, "Sci-Fi & Fantasy"}, {10766, "Soap"}, {10767, "Talk"},
	{10768, "War & Politics"}, {37, "Western"},
}

// discProvider is one fictional watch provider (all invented for the demo).
type discProvider struct {
	ID   int
	Name string
}

var discProviders = []discProvider{
	{9001, "Public Domain Streaming"},
	{9002, "Classic Cinema Channel"},
	{9003, "Archive Films"},
}

// discTraktList is one curated Trakt list (served FLAT per contract §9).
type discTraktList struct {
	TraktID     int
	Name        string
	Description string
	Slug        string
	Username    string
	ItemCount   int
	Likes       int
	Comments    int
}

var discTraktLists = []discTraktList{
	{101, "Classic Horror Essentials", "The most essential horror films from the public domain era", "classic-horror-essentials", "cinephile", 6, 1234, 12},
	{102, "Silent Film Gems", "Hidden treasures from the silent film era", "silent-film-gems", "filmhistorian", 6, 890, 8},
	{103, "Best of Film Noir", "The finest film noir from the public domain collection", "best-of-film-noir", "noirfan", 4, 567, 5},
}

// discTraktListItems maps list slug -> ordered catalog movie TMDB ids.
// Unknown slugs fall back to the film-noir set (old-demo behavior).
var discTraktListItems = map[string][]int{
	"classic-horror-essentials": {10331, 653, 234, 964, 16093, 21159},
	"silent-film-gems":          {653, 961, 19, 775, 234, 964},
	"best-of-film-noir":         {18995, 20367, 4808, 18398},
}
