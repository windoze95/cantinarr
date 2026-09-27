package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"time"
)

// The catalog records the creator's release pages, CC0 sources, and cover hashes.
// Audio is not bundled; downloads and library state remain simulated.
//
//go:embed assets/music/*.png assets/music/catalog.json
var publicDomainMusicFS embed.FS

var lidAlbumArtwork = map[int][]byte{}

type publicDomainAlbum struct {
	ID          int
	Slug        string
	Title       string
	ReleaseDate string `json:"release_date"`
	Genres      []string
	Tracks      []struct {
		Title      string
		DurationMS int `json:"duration_ms"`
	}
}

// Called inside the music seed's lock, before history is derived from files.
func lidSeedPublicDomainAlbums() {
	data, err := publicDomainMusicFS.ReadFile("assets/music/catalog.json")
	if err != nil {
		panic(err)
	}
	var albums []publicDomainAlbum
	if err := json.Unmarshal(data, &albums); err != nil {
		panic(err)
	}
	artist := lidSeedArtist(7, "John Oestmann",
		"Composer of electronic, chiptune, and soundtrack albums released under CC0.",
		"Person", []string{"Electronic", "Chiptune", "Soundtrack"}, 2)
	for i, record := range albums {
		cover, err := publicDomainMusicFS.ReadFile("assets/music/" + record.Slug + ".png")
		if err != nil {
			panic(err)
		}
		lidAlbumArtwork[record.ID] = cover
		tracks := make([]string, len(record.Tracks))
		for j, track := range record.Tracks {
			tracks[j] = track.Title
		}
		// Two owned releases fill Recently Added; two remain requestable.
		state := lidAlbumSeed{}
		if i < 2 {
			state = lidAlbumSeed{InLibrary: true, Monitored: true, Files: -1,
				Quality: lidQualityFLAC, AddedAgo: time.Duration(8+i) * time.Hour}
		}
		album := lidSeedAlbum(artist, record.ID, record.Title,
			fmt.Sprintf("%s by John Oestmann. Music and cover art released under CC0.", record.Title),
			record.ReleaseDate, "Album", nil, record.Genres, state, tracks...)
		for j, track := range record.Tracks {
			album.Tracks[j].Duration = track.DurationMS
		}
	}
}
