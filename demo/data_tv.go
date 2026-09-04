// data_tv.go — the fictional TV catalog (D3 discover).
//
// Ported from the old demo: 7 invented public-domain-themed series with
// fictional TMDB ids 90001-90007 and TVDB ids 390001-390007 (no real PD TV
// catalog exists on TMDB, so the shows are fiction by design). Extended
// beyond the old demo with per-season arrays (unique season ids, no season
// 0) and full episode fixtures — the Sonarr fake and the request season
// table build on these via findShow.
//
// Deterministic id scheme (stable across restarts):
//
//	season id  = tmdbID*10   + seasonNumber        (e.g. 900011)
//	episode id = tmdbID*1000 + seasonNumber*100 + episodeNumber
//
// Fixed dates go stale, so the latest season of every returning series is
// anchored at process start (discAnchorStraddle: the middle episode airs
// today, one a week either side), and the one unaired show premieres a
// fixed number of days out (discAnchorPremiere). Restarts re-anchor; the
// "Airing This Week" and "Coming Soon" rows, the Sonarr calendar, and the
// Trakt calendar all read these dates, so none of them go quiet.
//
// The shows are fiction, so their synthesized IMDb ids used to resolve to
// unrelated real titles; ImdbID is deliberately blank (rendered null) and
// every consumer treats an absent id as "no link".
package main

import (
	"fmt"
	"time"
)

// demoShows is the shared TV catalog (contract.md §7 — D3 exports it).
var demoShows []*DemoShow

// findShow is the cross-domain catalog lookup hook (contract.md §7).
func findShow(tmdbID int) (*DemoShow, bool) {
	for _, s := range demoShows {
		if s.TmdbID == tmdbID {
			return s, true
		}
	}
	return nil, false
}

// discTVPosters — fictional shows borrow posters from thematically similar
// real shows (old-demo decision, previously accepted for store review).
var discTVPosters = map[int]string{
	90001: "/ivUXXK80C8Uy4SKQ8IS0LB9zNDX.jpg",
	90002: "/nNeb35RFoeLwxu0CFzZ9NAI5UTA.jpg",
	90003: "/7uY4pCOxbEdv4M8jTE4uMPVoSIW.jpg",
	90004: "/sKlU5jCHKhP8v3wk7VjbLOXrFef.jpg",
	90005: "/265Gpw7wSwwMkUQlksot8B2chRg.jpg",
	90006: "/dY7zYWlHoctqD5iKbEpv3f07ysO.jpg",
	90007: "/wmqEZOkfILOmGplpOyTuAiq9vs6.jpg",
}

var discTVBackdrops = map[int]string{
	90001: "/fnArp9iDW0mM5Hu6JbIUlVuHRGj.jpg",
	90002: "/dnePq1kDs0On398Oc7sVUCZgtft.jpg",
	90003: "/fg5GcstgP9H8OCNS0MjOMp6MH8R.jpg",
	90004: "/fNA8lGi9dgZ6o66OQpMYJQFmb3X.jpg",
	90005: "/nwtdRZHTlDqYwuJDc7oVQueHY2l.jpg",
	90006: "/jZGxKCxDbepPaARnSu74Df9UE5Y.jpg",
	90007: "/6E1GcbN4ZLqp8aUiGsW7YgCZcnA.jpg",
}

// discAnchor says how a season's start date is chosen.
type discAnchor int

const (
	discAnchorFixed    discAnchor = iota // episode 1 airs on the spec's start date
	discAnchorStraddle                   // episode eps/2+1 airs today; weekly either side
	discAnchorPremiere                   // episode 1 airs today + discPremiereLeadDays
)

// discPremiereLeadDays is how far out the unaired show's premiere sits:
// inside the three-month "Coming Soon" window, past "Airing This Week".
const discPremiereLeadDays = 45

// discToday is the process-start date (UTC, midnight) every anchored
// season is computed from.
var discToday = time.Now().UTC().Truncate(24 * time.Hour)

// discSeasonSpec drives episode generation: weekly episodes from start.
type discSeasonSpec struct {
	number int
	start  string // air date of episode 1, "YYYY-MM-DD" (ignored when anchored)
	eps    int
	anchor discAnchor
}

// discSeasonStart resolves a spec's episode-1 air date.
func discSeasonStart(sp discSeasonSpec) time.Time {
	switch sp.anchor {
	case discAnchorStraddle:
		return discToday.AddDate(0, 0, -7*(sp.eps/2))
	case discAnchorPremiere:
		return discToday.AddDate(0, 0, discPremiereLeadDays)
	}
	start, _ := time.Parse("2006-01-02", sp.start)
	return start
}

func discBuildSeasons(tmdbID int, showName, posterPath string, runtime int, specs []discSeasonSpec) []DemoSeason {
	seasons := make([]DemoSeason, 0, len(specs))
	for _, sp := range specs {
		start := discSeasonStart(sp)
		eps := make([]DemoEpisode, 0, sp.eps)
		for n := 1; n <= sp.eps; n++ {
			air := start.AddDate(0, 0, (n-1)*7)
			eps = append(eps, DemoEpisode{
				ID:            tmdbID*1000 + sp.number*100 + n,
				EpisodeNumber: n,
				Name:          fmt.Sprintf("Episode %d", n),
				AirDate:       air.Format("2006-01-02"),
				Overview:      fmt.Sprintf("Episode %d of season %d of %s.", n, sp.number, showName),
				Runtime:       runtime,
			})
		}
		seasons = append(seasons, DemoSeason{
			ID:           tmdbID*10 + sp.number,
			SeasonNumber: sp.number,
			Name:         fmt.Sprintf("Season %d", sp.number),
			EpisodeCount: len(eps),
			AirDate:      start.Format("2006-01-02"),
			PosterPath:   posterPath,
			Episodes:     eps,
		})
	}
	return seasons
}

func init() {
	type discShowRow struct {
		id       int
		tvdbID   int
		name     string
		overview string
		tagline  string
		date     string
		vote     float64
		votes    int
		pop      float64
		genres   []DemoGenre
		status   string
		showType string
		runtime  int
		seasons  []discSeasonSpec
	}

	rows := []discShowRow{
		{
			90001, 390001, "Sherlock Holmes Adventures",
			"Follow the world's greatest detective as he solves baffling mysteries in Victorian London alongside his loyal companion Dr. Watson. Based on Arthur Conan Doyle's beloved public domain stories.",
			"The game is afoot.",
			"2020-01-15", 8.2, 1234, 45.6,
			[]DemoGenre{{9648, "Mystery"}, {18, "Drama"}},
			"Returning Series", "Scripted", 45,
			[]discSeasonSpec{
				{1, "2020-01-15", 12, discAnchorFixed},
				{2, "2021-09-08", 12, discAnchorFixed},
				{3, "2023-02-01", 12, discAnchorFixed},
				{4, "", 12, discAnchorStraddle},
			},
		},
		{
			90002, 390002, "Classic Science Theater",
			"A witty host and their robot companions watch and humorously comment on classic public domain science fiction films, turning terrible movies into comedy gold.",
			"The worst films in the galaxy, riffed live.",
			"2019-06-01", 8.5, 2345, 52.3,
			[]DemoGenre{{35, "Comedy"}, {10765, "Sci-Fi & Fantasy"}},
			"Returning Series", "Scripted", 90,
			[]discSeasonSpec{
				{1, "2019-06-01", 22, discAnchorFixed},
				{2, "2020-06-06", 22, discAnchorFixed},
				{3, "2021-06-05", 22, discAnchorFixed},
				{4, "2022-06-04", 22, discAnchorFixed},
				{5, "2023-06-03", 22, discAnchorFixed},
				{6, "", 22, discAnchorStraddle},
			},
		},
		{
			90003, 390003, "The Public Domain Players",
			"A talented ensemble cast performs adaptations of classic public domain literature and plays, from Shakespeare to H.G. Wells, breathing new life into timeless stories.",
			"Timeless stories, new voices.",
			"2021-03-10", 7.8, 678, 28.9,
			[]DemoGenre{{35, "Comedy"}, {18, "Drama"}},
			"Ended", "Scripted", 50,
			[]discSeasonSpec{
				{1, "2021-03-10", 12, discAnchorFixed},
				{2, "2022-03-09", 12, discAnchorFixed},
				{3, "2023-03-08", 12, discAnchorFixed},
			},
		},
		{
			90004, 390004, "Vintage Comedy Hour",
			"A celebration of classic comedy featuring curated clips and context from the golden age of comedy, featuring works by Buster Keaton, Charlie Chaplin, Harold Lloyd, and other comedy pioneers.",
			"Laughter never goes out of style.",
			"2018-09-22", 7.5, 890, 32.1,
			[]DemoGenre{{35, "Comedy"}, {99, "Documentary"}},
			"Returning Series", "Scripted", 42,
			[]discSeasonSpec{
				{1, "2018-09-22", 12, discAnchorFixed},
				{2, "2019-09-21", 12, discAnchorFixed},
				{3, "2021-09-25", 12, discAnchorFixed},
				{4, "2023-09-23", 12, discAnchorFixed},
				{5, "", 12, discAnchorStraddle},
			},
		},
		{
			90005, 390005, "Tales from the Public Domain",
			"An anthology series that adapts classic public domain short stories, fairy tales, and myths into modern dramatic episodes, featuring a rotating cast of acclaimed actors.",
			"Every story ever told is waiting.",
			"2022-10-31", 7.9, 456, 25.4,
			[]DemoGenre{{18, "Drama"}, {10765, "Sci-Fi & Fantasy"}},
			"Returning Series", "Scripted", 55,
			[]discSeasonSpec{
				{1, "2022-10-31", 8, discAnchorFixed},
				{2, "", 8, discAnchorStraddle},
			},
		},
		{
			90006, 390006, "Silent Film Classics",
			"An in-depth documentary series exploring the art, history, and lasting influence of silent cinema, featuring restored footage and expert commentary on public domain masterpieces.",
			"When pictures learned to move.",
			"2021-01-05", 8.0, 345, 18.7,
			[]DemoGenre{{99, "Documentary"}},
			"Ended", "Documentary", 52,
			[]discSeasonSpec{
				{1, "2021-01-05", 8, discAnchorFixed},
				{2, "2022-01-10", 8, discAnchorFixed},
				{3, "2023-01-09", 8, discAnchorFixed},
			},
		},
		{
			90007, 390007, "The Lantern Society",
			"A rotating troupe retells the ghost stories of the public-domain canon, one each week, from M.R. James to Ambrose Bierce and Edith Wharton.",
			"Every lantern hides a story.",
			"", 7.6, 24, 21.3, // first air date = the anchored premiere, filled in below
			[]DemoGenre{{9648, "Mystery"}, {18, "Drama"}},
			"In Production", "Scripted", 48,
			[]discSeasonSpec{
				{1, "", 8, discAnchorPremiere},
			},
		},
	}

	demoShows = make([]*DemoShow, 0, len(rows))
	for _, row := range rows {
		poster := discTVPosters[row.id]
		seasons := discBuildSeasons(row.id, row.name, poster, row.runtime, row.seasons)
		firstAir := row.date
		if firstAir == "" && len(seasons) > 0 {
			firstAir = seasons[0].AirDate // an anchored premiere
		}
		demoShows = append(demoShows, &DemoShow{
			TmdbID:           row.id,
			TvdbID:           row.tvdbID,
			ImdbID:           "", // fiction: no IMDb record exists (see header)
			Name:             row.name,
			Overview:         row.overview,
			Tagline:          row.tagline,
			PosterPath:       poster,
			BackdropPath:     discTVBackdrops[row.id],
			FirstAirDate:     firstAir,
			Genres:           row.genres,
			VoteAverage:      row.vote,
			VoteCount:        row.votes,
			Popularity:       row.pop,
			Status:           row.status,
			Type:             row.showType,
			OriginalLanguage: "en",
			Seasons:          seasons,
		})
	}
}
