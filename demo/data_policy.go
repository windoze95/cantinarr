// data_policy.go — kids accounts: the per-title certification maps and the
// seeded policy for user 4 (kid). The certification catalog, the policy
// routes, and the decision helpers live in contentpolicy.go.
//
// Ratings are keyed TMDB id -> ISO 3166-1 region -> certification. A missing
// region key is "no entry in that region": unrated there, decided by the
// policy's block_unrated. The values are chosen so the seeded policy (US,
// movies up to PG, shows up to TV-PG, unrated hidden, Horror hidden) thins
// the catalog visibly: 7 of 18 films and 3 of 6 shows survive. TMDB itself
// lists 16 of the 18 films as NR or absent in the US (checked 2026-09-04),
// which would hide nearly everything, so the demo keeps plausible values of
// the kind TMDB carries for films of each era; GB follows TMDB where it has
// an entry. The discover domain reads these maps to emit
// release_dates[].certification and content_ratings on detail payloads.
package main

// cpMovieCerts: film TMDB id -> region -> certification.
var cpMovieCerts = map[int]map[string]string{
	// Shown under the seeded policy (G / PG in the US scheme).
	961:   {"US": "G", "GB": "U"},   // The General
	775:   {"US": "G", "GB": "U"},   // A Trip to the Moon
	3085:  {"US": "PG", "GB": "U"},  // His Girl Friday
	19:    {"US": "PG", "GB": "PG"}, // Metropolis
	4808:  {"US": "PG", "GB": "PG"}, // Charade
	260:   {"US": "PG", "GB": "U"},  // The 39 Steps
	15263: {"US": "PG", "GB": "U"},  // McLintock!
	// Hidden as unrated: "Approved" and "Passed" are Production Code era
	// stamps outside TMDB's US scheme, so the evaluator treats them exactly
	// like a missing rating. They reappear when Hide unrated titles is off,
	// and under a GB policy, where they carry a PG.
	18995: {"US": "Approved", "GB": "PG"}, // D.O.A.
	20367: {"US": "Passed", "GB": "PG"},   // Detour
	18398: {"US": "Approved", "GB": "PG"}, // Suddenly
	// Hidden by the Horror block. Carnival of Souls and The Little Shop of
	// Horrors sit at PG and come back when Horror is un-hidden; the rest are
	// also over the cap or unrated in the US, so un-hiding the genre alone
	// never surfaces them.
	10331: {"US": "R", "GB": "15"},     // Night of the Living Dead
	21159: {"US": "PG-13", "GB": "15"}, // The Last Man on Earth
	16093: {"US": "PG", "GB": "12"},    // Carnival of Souls
	24452: {"US": "PG", "GB": "PG"},    // The Little Shop of Horrors
	653:   {"GB": "PG"},                // Nosferatu (no US entry)
	234:   {"GB": "U"},                 // The Cabinet of Dr. Caligari (no US entry)
	964:   {"GB": "PG"},                // The Phantom of the Opera (no US entry)
	10513: {"GB": "PG"},                // Plan 9 from Outer Space (no US entry)
}

// cpShowCerts: show TMDB id -> region -> content rating. The shows are
// invented, so the values are assigned for the split they produce.
var cpShowCerts = map[int]map[string]string{
	90001: {"US": "TV-PG", "GB": "PG"}, // Sherlock Holmes Adventures
	90003: {"US": "TV-PG", "GB": "PG"}, // The Public Domain Players
	90004: {"US": "TV-G", "GB": "U"},   // Vintage Comedy Hour
	90007: {"US": "TV-PG", "GB": "PG"}, // The Lantern Society (Coming Soon)
	90002: {"US": "TV-14", "GB": "12"}, // Classic Science Theater (over the TV-PG cap)
	90005: {"US": "TV-14", "GB": "15"}, // Tales from the Public Domain (over the TV-PG cap)
	// 90006 Silent Film Classics carries no entry in any region: the TV
	// counterpart of D.O.A., hidden only while unrated titles are hidden.
}

// init seeds user 4 (kid) as a kids account. The policy is exactly the row
// the app writes when an admin flips the Kids account switch (PG / TV-PG
// are the catalog's suggested defaults, Hide unrated titles is the app's
// default) and then adds the Horror chip. Only the frozen user id is
// referenced: no state accessor runs from init().
func init() {
	cpMu.Lock()
	cpPolicies[4] = &cpPolicy{
		MaxMovieRating:     "PG",
		MaxTVRating:        "TV-PG",
		RatingRegion:       "US",
		BlockUnrated:       true,
		BlockedMovieGenres: []int{27},
		BlockedTVGenres:    []int{},
	}
	cpMu.Unlock()
}
