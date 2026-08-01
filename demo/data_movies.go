// data_movies.go — the curated public-domain film catalog (D3 discover).
//
// Ported from the old demo's data.go: 18 real public-domain films with
// verified TMDB ids and hand-checked poster/backdrop paths (two bugfix
// passes went into getting those right — treat them as authoritative).
// original_language values are honest (Metropolis/Nosferatu/Caligari "de",
// A Trip to the Moon "fr"); the demo seeds english_only:false so nothing
// hides from the feed rows.
package main

// discMoviePosters holds the real TMDB poster paths keyed by TMDB id.
var discMoviePosters = map[int]string{
	10331: "/rb2NWyb008u1EcKCOyXs2Nmj0ra.jpg", // Night of the Living Dead
	653:   "/zv7J85D8CC9qYagAEhPM63CIG6j.jpg", // Nosferatu
	3085:  "/lu86Y9zTPH3neCiUzLzHCEFHf7f.jpg", // His Girl Friday
	961:   "/nIp4gIXogCjfB1QABNsWwa9gSca.jpg", // The General
	19:    "/kr9wXRN23zLuWJIelahas1mtnYj.jpg", // Metropolis
	775:   "/9o0v5LLFk51nyTBHZSre6OB37n2.jpg", // A Trip to the Moon
	234:   "/myK9DeIsXWGKgUTZyGXg2IfFk0W.jpg", // The Cabinet of Dr. Caligari
	4808:  "/qqaPjC5FQidtKY65jbAKZPiOTaS.jpg", // Charade
	18995: "/m7wZXWOt9XQKJT8r9pbAFo7jB46.jpg", // D.O.A.
	964:   "/mvaYpAYj957C2tlq3vVJPSzGJXK.jpg", // The Phantom of the Opera
	24452: "/s9MrumN9oCfv1rFoEvMLwucWD7V.jpg", // The Little Shop of Horrors
	10513: "/bmicZi7PvlnZ9rZqp6QXN2Db0pT.jpg", // Plan 9 from Outer Space
	21159: "/m0zIz963wNxAqqxiKYdZS9cRScT.jpg", // The Last Man on Earth
	18398: "/v0eQfRMVJtvEkaJjBsSrd6ri1UI.jpg", // Suddenly
	20367: "/gJb9HRAs1V4bA0VKsWpT6mhv2RT.jpg", // Detour
	260:   "/oC81jK6aAug4MA0xzYVngHmjsZS.jpg", // The 39 Steps
	15263: "/gjaAtWJqGomsE5qNQStHcHhSzES.jpg", // McLintock!
	16093: "/9ddPGH7kMe81xznwIKCt17VFUPi.jpg", // Carnival of Souls
}

// discMovieBackdrops holds the real TMDB backdrop paths keyed by TMDB id.
var discMovieBackdrops = map[int]string{
	10331: "/5KtmBSqFtHY3I9t8lgH27Mc0bqY.jpg",
	653:   "/u06SympEEMb7pLhR9W3tvydHT9L.jpg",
	3085:  "/ihoQaiMFAz4YCTAaSIsQjRknsNH.jpg",
	961:   "/mzox4HbcV9W42MH2dB1QsdDVGo3.jpg",
	19:    "/eeMoFKxjjiCi6iep2GEZtSAMYIr.jpg",
	775:   "/u6cyzNiF95GaZFRmNYRDQJoU0Av.jpg",
	234:   "/iNNNkYuUU365LhnPwQqfzBvKnsg.jpg",
	4808:  "/gj2TBYmOIy4E2GjMIpnbAEZNCQx.jpg",
	18995: "/rsl2YCe2bSeni2qGdaMoDBS7sLo.jpg",
	964:   "/6FSs7jMimg1PBYdhZtPNQhtKtEc.jpg",
	24452: "/sGz7JApiDtClRFxffIw3WgRK8y2.jpg",
	10513: "/9pHCAT1ScILdkhY8ErfOma8W4kB.jpg",
	21159: "/2peNKd9nJkGUofBRhS9LYPTcXnz.jpg",
	18398: "/mxW3h5E71IOnctX65wRWBTqwm5i.jpg",
	20367: "/uGOKN4VeCsf249zypWfaAzfTxAB.jpg",
	260:   "/zqLR4jm0lPFGnyN55QBSf4pzz5n.jpg",
	15263: "/zTLtdaz8kIEQfCZNzQzBx5xNkxt.jpg",
	16093: "/esIoQw7VaykfHsw6fx2VltZ1R7U.jpg",
}

// demoMovies is the shared movie catalog (contract.md §7 — D3 exports it;
// seed order = old-demo order, which is the "popular"/trending feed order).
var demoMovies []*DemoMovie

// findMovie is the cross-domain catalog lookup hook (contract.md §7).
func findMovie(tmdbID int) (*DemoMovie, bool) {
	for _, m := range demoMovies {
		if m.TmdbID == tmdbID {
			return m, true
		}
	}
	return nil, false
}

func init() {
	type discMovieRow struct {
		id       int
		title    string
		overview string
		date     string
		vote     float64
		votes    int
		pop      float64
		genres   []DemoGenre
		runtime  int
		tagline  string
		imdb     string
		lang     string
		origT    string
	}

	rows := []discMovieRow{
		{10331, "Night of the Living Dead", "A group of people hide from bloodthirsty zombies in a farmhouse. This groundbreaking 1968 horror film by George A. Romero established the modern zombie genre and remains one of the most influential independent films ever made.", "1968-10-01", 7.5, 1842, 42.5, []DemoGenre{{27, "Horror"}}, 96, "They won't stay dead!", "tt0063350", "en", "Night of the Living Dead"},
		{653, "Nosferatu", "Vampire Count Orlok expresses interest in a new residence and real estate agent Hutter's wife. A masterpiece of German Expressionism and one of the earliest surviving vampire films.", "1922-03-04", 7.8, 2134, 38.2, []DemoGenre{{27, "Horror"}}, 94, "A Symphony of Horror", "tt0013442", "de", "Nosferatu, eine Symphonie des Grauens"},
		{3085, "His Girl Friday", "A newspaper editor uses every trick in the book to keep his ace reporter ex-wife from remarrying. Howard Hawks' rapid-fire comedy is considered one of the greatest screwball comedies ever made.", "1940-01-11", 7.7, 892, 28.1, []DemoGenre{{35, "Comedy"}, {10749, "Romance"}}, 92, "The funniest love story ever told!", "tt0032599", "en", "His Girl Friday"},
		{961, "The General", "When Union spies steal an engineer's beloved locomotive, he pursues it single-handedly and straight through enemy lines. Buster Keaton's silent masterpiece is widely regarded as one of the greatest films ever made.", "1926-12-31", 8.1, 1567, 35.7, []DemoGenre{{35, "Comedy"}, {28, "Action"}, {10752, "War"}}, 67, "The greatest locomotive chase in history!", "tt0017925", "en", "The General"},
		{19, "Metropolis", "In a futuristic city sharply divided between the working class and the city planners, the son of the city's mastermind falls in love with a working-class prophet who predicts the coming of a savior to mediate their differences.", "1927-01-10", 8.3, 3456, 48.9, []DemoGenre{{878, "Science Fiction"}, {18, "Drama"}}, 153, "There can be no understanding between the hands and the brain unless the heart acts as mediator.", "tt0017136", "de", "Metropolis"},
		{775, "A Trip to the Moon", "A group of astronomers go on an expedition to the Moon. Georges Méliès' pioneering 1902 film is considered one of the first science fiction films and a landmark in cinema history.", "1902-09-01", 8.0, 2890, 33.4, []DemoGenre{{878, "Science Fiction"}, {12, "Adventure"}}, 13, "An extraordinary voyage to the Moon", "tt0000417", "fr", "Le Voyage dans la Lune"},
		{234, "The Cabinet of Dr. Caligari", "Francis, a young man, recalls in his memory the disturbing events involving himself and his close friend Alan with the insane Dr. Caligari and his sleepwalking accomplice Cesare.", "1920-02-26", 8.0, 1678, 31.2, []DemoGenre{{27, "Horror"}, {18, "Drama"}}, 76, "You must become Caligari!", "tt0010323", "de", "Das Cabinet des Dr. Caligari"},
		{4808, "Charade", "Romance and suspense ensue in Paris as a woman is pursued by several men who want a fortune her late husband had stolen. Often called 'the best Hitchcock movie Hitchcock never made.'", "1963-12-05", 7.8, 1234, 36.8, []DemoGenre{{53, "Thriller"}, {35, "Comedy"}, {9648, "Mystery"}}, 113, "You can expect the unexpected when they play...", "tt0056923", "en", "Charade"},
		{18995, "D.O.A.", "A businessman has been poisoned with a slow-acting toxin and has only a day or two to live. He sets out to find his own killer in this classic film noir.", "1949-12-30", 7.2, 456, 18.5, []DemoGenre{{53, "Thriller"}, {80, "Crime"}}, 83, "A man investigates his own murder", "tt0041699", "en", "D.O.A."},
		{964, "The Phantom of the Opera", "A disfigured musical genius lurks beneath the Paris Opera House, exercising a reign of terror over all who inhabit it. Lon Chaney's legendary performance in this 1925 silent classic remains iconic.", "1925-09-06", 7.5, 789, 24.3, []DemoGenre{{27, "Horror"}, {18, "Drama"}}, 93, "The terror beneath the opera", "tt0016220", "en", "The Phantom of the Opera"},
		{24452, "The Little Shop of Horrors", "A clumsy young man nurtures a plant and discovers that it's carnivorous, forcing him to kill to feed it. Roger Corman's legendary low-budget comedy was famously shot in just two days.", "1960-09-14", 6.8, 567, 22.1, []DemoGenre{{35, "Comedy"}, {27, "Horror"}}, 72, "Don't feed the plants!", "tt0054033", "en", "The Little Shop of Horrors"},
		{10513, "Plan 9 from Outer Space", "Aliens resurrect dead humans as zombies and vampires to stop humanity from creating the Solaranite bomb. Ed Wood's legendary film has been called 'the worst movie ever made,' gaining a massive cult following.", "1957-07-22", 5.2, 1023, 26.7, []DemoGenre{{878, "Science Fiction"}, {27, "Horror"}}, 79, "Unspeakable horrors from outer space paralyze the living and resurrect the dead!", "tt0052077", "en", "Plan 9 from Outer Space"},
		{21159, "The Last Man on Earth", "When a disease turns all of humanity into the undead, the last man alive must struggle to survive using science as his weapon. Vincent Price stars in this influential 1964 adaptation of Richard Matheson's 'I Am Legend.'", "1964-03-08", 6.9, 678, 19.8, []DemoGenre{{27, "Horror"}, {878, "Science Fiction"}}, 86, "Alive among the walking dead!", "tt0058700", "en", "The Last Man on Earth"},
		{18398, "Suddenly", "Three assassins, led by a psychopathic ex-soldier, take over a house overlooking a railroad station in a small town, in order to assassinate the President.", "1954-10-07", 6.9, 345, 15.2, []DemoGenre{{53, "Thriller"}, {80, "Crime"}}, 77, "In a split second, a town is taken hostage!", "tt0047556", "en", "Suddenly"},
		{20367, "Detour", "A down-on-his-luck New York musician hitchhikes to California, but his luck changes for the worse when he meets a woman with a dark secret. Edgar G. Ulmer's masterpiece of low-budget film noir.", "1945-11-30", 7.2, 412, 14.8, []DemoGenre{{53, "Thriller"}, {80, "Crime"}, {18, "Drama"}}, 67, "He went searching for love... but fate forced a DETOUR!", "tt0037638", "en", "Detour"},
		{260, "The 39 Steps", "A man in London tries to help a counter-espionage agent but finds himself on the run from the police as a murder suspect. Alfred Hitchcock's classic 1935 thriller set the template for the 'wrong man' genre.", "1935-06-06", 7.6, 1089, 27.4, []DemoGenre{{53, "Thriller"}, {9648, "Mystery"}}, 86, "The man with the missing finger!", "tt0026029", "en", "The 39 Steps"},
		{15263, "McLintock!", "Cattle baron John McLintock deals with his estranged wife, a young woman's parents, and local politics. John Wayne stars in this rollicking 1963 Western comedy.", "1963-11-13", 6.9, 456, 20.3, []DemoGenre{{37, "Western"}, {35, "Comedy"}}, 127, "He tames the West and the women!", "tt0057298", "en", "McLintock!"},
		{16093, "Carnival of Souls", "After a traumatic accident, a woman becomes drawn to a mysterious abandoned carnival. Herk Harvey's 1962 low-budget horror film has become one of the most influential cult classics in cinema.", "1962-09-26", 7.1, 567, 21.6, []DemoGenre{{27, "Horror"}, {9648, "Mystery"}}, 78, "She escaped death... only to face something worse", "tt0055830", "en", "Carnival of Souls"},
	}

	demoMovies = make([]*DemoMovie, 0, len(rows))
	for _, row := range rows {
		demoMovies = append(demoMovies, &DemoMovie{
			TmdbID:           row.id,
			ImdbID:           row.imdb,
			Title:            row.title,
			OriginalTitle:    row.origT,
			Overview:         row.overview,
			Tagline:          row.tagline,
			PosterPath:       discMoviePosters[row.id],
			BackdropPath:     discMovieBackdrops[row.id],
			ReleaseDate:      row.date,
			Genres:           row.genres,
			VoteAverage:      row.vote,
			VoteCount:        row.votes,
			Popularity:       row.pop,
			Runtime:          row.runtime,
			OriginalLanguage: row.lang,
		})
	}
}
