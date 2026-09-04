// data_credits.go — per-title extras: cast and crew, studios, countries,
// budget and revenue, release milestones, networks, and creators.
//
// SKELETON: the discover domain replaces this file wholesale. The only
// cross-domain export other files compile against is discShowNetworkName.
package main

// discShowNetworkName is the network a show airs on, shared by the TMDB
// detail payload, the Sonarr series document, and the Trakt show object so
// the three surfaces agree.
func discShowNetworkName(tmdbID int) string {
	return "Cantina"
}
