// data_people.go — the person catalog (D3 discover).
//
// Ported from the old demo: 4 real film-history figures with factual bios,
// real TMDB profile image paths and real IMDB ids, but FAKE sequential ids
// 1-4 (NOT real TMDB person ids). Credits point into the movie catalog by
// TMDB id; renderers resolve title/poster/overview via findMovie so the
// data never drifts.
package main

// discPersons holds the person catalog in id order.
var discPersons = []*DemoPerson{
	{
		ID:           1,
		Name:         "Buster Keaton",
		Biography:    "Joseph Frank Keaton, known professionally as Buster Keaton, was an American actor, comedian, film director, producer, screenwriter, and stunt performer. He is best known for his silent films, in which his trademark was physical comedy with a consistently stoic, deadpan expression.",
		Birthday:     "1895-10-04",
		Deathday:     "1966-02-01",
		PlaceOfBirth: "Piqua, Kansas, USA",
		ProfilePath:  "/kEybBFkO5AX83o3WKyNDfuvfVrn.jpg",
		KnownForDept: "Acting",
		Popularity:   35.2,
		Gender:       2,
		ImdbID:       "nm0000036",
		AlsoKnownAs:  []string{"Joseph Frank Keaton", "The Great Stone Face"},
		CastCredits: []DemoCredit{
			{TmdbID: 961, MediaType: mediaTypeMovie, Character: "Johnnie Gray"},
		},
		CrewCredits: []DemoCredit{
			{TmdbID: 961, MediaType: mediaTypeMovie, Job: "Director", Department: "Directing"},
		},
	},
	{
		ID:           2,
		Name:         "Fritz Lang",
		Biography:    "Friedrich Christian Anton Lang was an Austrian-German-American filmmaker, screenwriter, and occasional film producer and actor. One of the best known practitioners of German Expressionism.",
		Birthday:     "1890-12-05",
		Deathday:     "1976-08-02",
		PlaceOfBirth: "Vienna, Austria-Hungary",
		ProfilePath:  "/9dz4PmFzlSyexldWrOXBLLpkBqB.jpg",
		KnownForDept: "Directing",
		Popularity:   28.9,
		Gender:       2,
		ImdbID:       "nm0000485",
		AlsoKnownAs:  []string{"Friedrich Christian Anton Lang"},
		CastCredits:  []DemoCredit{},
		CrewCredits: []DemoCredit{
			{TmdbID: 19, MediaType: mediaTypeMovie, Job: "Director", Department: "Directing"},
		},
	},
	{
		ID:           3,
		Name:         "F.W. Murnau",
		Biography:    "Friedrich Wilhelm Murnau was a German film director who was a prominent figure during the Golden Age of Weimar cinema.",
		Birthday:     "1888-12-28",
		Deathday:     "1931-03-11",
		PlaceOfBirth: "Bielefeld, Germany",
		ProfilePath:  "/keLp43iiIkkllroUwExIJITeR7a.jpg",
		KnownForDept: "Directing",
		Popularity:   22.4,
		Gender:       2,
		ImdbID:       "nm0003638",
		AlsoKnownAs:  []string{"Friedrich Wilhelm Plumpe"},
		CastCredits:  []DemoCredit{},
		CrewCredits: []DemoCredit{
			{TmdbID: 653, MediaType: mediaTypeMovie, Job: "Director", Department: "Directing"},
		},
	},
	{
		ID:           4,
		Name:         "George A. Romero",
		Biography:    "George Andrew Romero was an American-Canadian filmmaker, writer, and editor, best known for his series of zombie films. He is considered the father of the modern zombie genre.",
		Birthday:     "1940-02-04",
		Deathday:     "2017-07-16",
		PlaceOfBirth: "The Bronx, New York, USA",
		ProfilePath:  "/w2zVF92x149qK79ZxwUowcSp2c6.jpg",
		KnownForDept: "Directing",
		Popularity:   25.1,
		Gender:       2,
		ImdbID:       "nm0001681",
		AlsoKnownAs:  []string{"George Andrew Romero"},
		CastCredits:  []DemoCredit{},
		CrewCredits: []DemoCredit{
			{TmdbID: 10331, MediaType: mediaTypeMovie, Job: "Director", Department: "Directing"},
		},
	},
}

// discPersonByID looks a person up by fake sequential id.
func discPersonByID(id int) (*DemoPerson, bool) {
	for _, p := range discPersons {
		if p.ID == id {
			return p, true
		}
	}
	return nil, false
}
