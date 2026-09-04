// data_credits.go — per-title extras (D3 discover): cast and crew, studios,
// countries, budget and revenue, historical release milestones, networks,
// and creators. One table owns who did what on which title: the person
// catalog's CastCredits/CrewCredits are DERIVED from these maps at init
// (discLinkCredits), never written by hand.
//
// Facts for the real films (cast, crew, money, milestones) were checked
// against TMDB when this file was written; figures TMDB carries in a
// currency other than dollars (Metropolis, Caligari, The 39 Steps) are left
// at 0 rather than shown as if they were dollars. The show people are
// invented, like the shows.
//
// Cast and crew refer to people by name and are resolved once at init; an
// unknown name panics at startup so a typo can never ship as a silently
// missing credit.
package main

import (
	"fmt"
	"sort"
)

// discCastRef is one billed performer; billing order is slice order.
type discCastRef struct {
	Person    string
	Character string
}

// discCrewRef is one crew credit (TMDB job + department).
type discCrewRef struct {
	Person     string
	Job        string
	Department string
}

// discRelease is one release milestone in one region: TMDB type (1
// premiere, 2 limited, 3 theatrical, 4 digital, 5 physical, 6 TV) and the
// calendar date.
type discRelease struct {
	Type int
	Date string // "YYYY-MM-DD"
}

// discMovieExtra is everything a movie detail carries beyond DemoMovie.
// Countries[0] is the origin region: its theatrical (type 3) milestone is
// the catalog release_date and is added at render time, so the two never
// drift; Releases holds only the verified milestones beyond that.
type discMovieExtra struct {
	Companies []int
	Countries []string
	Budget    int
	Revenue   int
	Cast      []discCastRef
	Crew      []discCrewRef
	Releases  map[string][]discRelease
}

// discShowExtra is everything a TV detail carries beyond DemoShow.
type discShowExtra struct {
	Companies []int
	Networks  []int
	CreatedBy []string
	Countries []string
	Cast      []discCastRef
	Crew      []discCrewRef
}

// discNetworks are the invented networks the shows air on.
var discNetworks = []discCompany{
	{9101, "Cantina", "US"},
	{9102, "Archive Channel", "US"},
}

const (
	deptDirecting  = "Directing"
	deptWriting    = "Writing"
	deptProduction = "Production"
)

func director(name string) discCrewRef { return discCrewRef{name, "Director", deptDirecting} }
func writer(name string) discCrewRef   { return discCrewRef{name, "Writer", deptWriting} }
func screenplay(name string) discCrewRef {
	return discCrewRef{name, "Screenplay", deptWriting}
}
func novel(name string) discCrewRef    { return discCrewRef{name, "Novel", deptWriting} }
func producer(name string) discCrewRef { return discCrewRef{name, "Producer", deptProduction} }

var discMovieExtras = map[int]*discMovieExtra{
	10331: { // Night of the Living Dead
		Companies: []int{800001}, Countries: []string{"US"}, Budget: 114000, Revenue: 30236452,
		Cast:     []discCastRef{{"Duane Jones", "Ben"}, {"Judith O'Dea", "Barbra"}, {"Karl Hardman", "Harry Cooper"}},
		Crew:     []discCrewRef{director("George A. Romero"), writer("George A. Romero"), writer("John A. Russo"), producer("Russell Streiner")},
		Releases: map[string][]discRelease{"US": {{5, "1978-10-24"}}},
	},
	653: { // Nosferatu
		Companies: []int{800002}, Countries: []string{"DE"},
		Cast: []discCastRef{{"Max Schreck", "Graf Orlok"}, {"Gustav von Wangenheim", "Hutter"}, {"Greta Schröder", "Ellen"}},
		Crew: []discCrewRef{director("F.W. Murnau"), writer("Henrik Galeen"), producer("Albin Grau")},
		Releases: map[string][]discRelease{
			"DE": {{5, "2014-06-27"}},
			"US": {{3, "1929-06-01"}, {4, "2007-04-25"}, {5, "1995-08-12"}},
		},
	},
	3085: { // His Girl Friday
		Companies: []int{800003}, Countries: []string{"US"},
		Cast: []discCastRef{{"Cary Grant", "Walter Burns"}, {"Rosalind Russell", "Hildy Johnson"}, {"Ralph Bellamy", "Bruce Baldwin"}},
		Crew: []discCrewRef{director("Howard Hawks"), producer("Howard Hawks"), screenplay("Charles Lederer"), {"Ben Hecht", "Theatre Play", deptWriting}},
	},
	961: { // The General
		Companies: []int{800004}, Countries: []string{"US"}, Budget: 750000, Revenue: 1000000,
		Cast: []discCastRef{{"Buster Keaton", "Johnnie Gray"}, {"Marion Mack", "Annabelle Lee"}, {"Glen Cavender", "Captain Anderson"}},
		Crew: []discCrewRef{director("Buster Keaton"), director("Clyde Bruckman"), writer("Clyde Bruckman"), writer("Al Boasberg")},
	},
	19: { // Metropolis
		Companies: []int{800005}, Countries: []string{"DE"},
		Cast:     []discCastRef{{"Brigitte Helm", "Maria"}, {"Gustav Fröhlich", "Freder"}, {"Alfred Abel", "Joh Fredersen"}, {"Rudolf Klein-Rogge", "Rotwang"}},
		Crew:     []discCrewRef{director("Fritz Lang"), screenplay("Thea von Harbou"), novel("Thea von Harbou"), producer("Erich Pommer")},
		Releases: map[string][]discRelease{"US": {{3, "1927-03-06"}, {5, "1984-10-13"}}},
	},
	775: { // A Trip to the Moon
		Companies: []int{800006}, Countries: []string{"FR"},
		Cast: []discCastRef{{"Georges Méliès", "Professor Barbenfouillis"}, {"Bleuette Bernon", "Phoebe"}, {"Victor André", "Astronomer"}},
		Crew: []discCrewRef{director("Georges Méliès"), writer("Georges Méliès"), producer("Georges Méliès")},
		Releases: map[string][]discRelease{
			"FR": {{5, "2012-04-23"}},
			"US": {{3, "1902-10-04"}, {5, "2012-04-10"}},
		},
	},
	234: { // The Cabinet of Dr. Caligari
		Companies: []int{800007}, Countries: []string{"DE"},
		Cast:     []discCastRef{{"Werner Krauss", "Dr. Caligari"}, {"Conrad Veidt", "Cesare"}, {"Lil Dagover", "Jane"}},
		Crew:     []discCrewRef{director("Robert Wiene"), writer("Carl Mayer"), writer("Hans Janowitz")},
		Releases: map[string][]discRelease{"US": {{1, "1921-04-03"}, {5, "1996-05-01"}}},
	},
	4808: { // Charade
		Companies: []int{800008, 800009}, Countries: []string{"US"}, Budget: 4000000, Revenue: 13475000,
		Cast: []discCastRef{{"Cary Grant", "Peter Joshua"}, {"Audrey Hepburn", "Regina Lampert"}, {"Walter Matthau", "Hamilton Bartholomew"}},
		Crew: []discCrewRef{director("Stanley Donen"), producer("Stanley Donen"), screenplay("Peter Stone")},
	},
	18995: { // D.O.A.
		Companies: []int{800010}, Countries: []string{"US"},
		Cast: []discCastRef{{"Edmond O'Brien", "Frank Bigelow"}, {"Pamela Britton", "Paula Gibson"}, {"Luther Adler", "Majak"}},
		Crew: []discCrewRef{director("Rudolph Maté"), writer("Russell Rouse"), writer("Clarence Greene"), producer("Clarence Greene")},
	},
	964: { // The Phantom of the Opera
		Companies: []int{800009}, Countries: []string{"US"}, Revenue: 2000000,
		Cast: []discCastRef{{"Lon Chaney", "Erik, the Phantom"}, {"Mary Philbin", "Christine Daaé"}, {"Norman Kerry", "Raoul de Chagny"}},
		Crew: []discCrewRef{director("Rupert Julian"), novel("Gaston Leroux"), producer("Carl Laemmle")},
	},
	24452: { // The Little Shop of Horrors
		Companies: []int{800011}, Countries: []string{"US"}, Budget: 31000, Revenue: 25066,
		Cast: []discCastRef{{"Jonathan Haze", "Seymour Krelboined"}, {"Jackie Joseph", "Audrey Fulquard"}, {"Jack Nicholson", "Wilbur Force"}},
		Crew: []discCrewRef{director("Roger Corman"), producer("Roger Corman"), writer("Charles B. Griffith")},
	},
	10513: { // Plan 9 from Outer Space
		Companies: []int{800012}, Countries: []string{"US"}, Budget: 60000,
		Cast:     []discCastRef{{"Gregory Walcott", "Jeff Trent"}, {"Bela Lugosi", "Ghoul Man"}, {"Maila Nurmi", "Vampire Girl"}},
		Crew:     []discCrewRef{director("Ed Wood"), writer("Ed Wood"), producer("Ed Wood")},
		Releases: map[string][]discRelease{"US": {{5, "1994-01-20"}}},
	},
	21159: { // The Last Man on Earth
		Companies: []int{800013, 800014}, Countries: []string{"IT", "US"}, Budget: 300000,
		Cast:     []discCastRef{{"Vincent Price", "Dr. Robert Morgan"}, {"Franca Bettoia", "Ruth Collins"}, {"Emma Danieli", "Virginia Morgan"}},
		Crew:     []discCrewRef{director("Ubaldo Ragona"), director("Sidney Salkow"), novel("Richard Matheson"), screenplay("Richard Matheson")},
		Releases: map[string][]discRelease{"US": {{3, "1964-05-06"}}},
	},
	18398: { // Suddenly
		Companies: []int{800015}, Countries: []string{"US"},
		Cast: []discCastRef{{"Frank Sinatra", "John Baron"}, {"Sterling Hayden", "Sheriff Tod Shaw"}, {"James Gleason", "Pop Benson"}},
		Crew: []discCrewRef{director("Lewis Allen"), writer("Richard Sale"), producer("Robert Bassler")},
	},
	20367: { // Detour
		Companies: []int{800016}, Countries: []string{"US"}, Budget: 30000,
		Cast: []discCastRef{{"Tom Neal", "Al Roberts"}, {"Ann Savage", "Vera"}, {"Claudia Drake", "Sue Harvey"}},
		Crew: []discCrewRef{director("Edgar G. Ulmer"), writer("Martin Goldsmith"), novel("Martin Goldsmith"), producer("Leon Fromkess")},
	},
	260: { // The 39 Steps
		Companies: []int{800017}, Countries: []string{"GB"},
		Cast: []discCastRef{{"Robert Donat", "Richard Hannay"}, {"Madeleine Carroll", "Pamela"}, {"Lucie Mannheim", "Annabella Smith"}},
		Crew: []discCrewRef{director("Alfred Hitchcock"), screenplay("Charles Bennett"), novel("John Buchan"), producer("Michael Balcon")},
		Releases: map[string][]discRelease{
			"GB": {{5, "2007-06-19"}},
			"US": {{3, "1935-08-01"}, {4, "2015-12-25"}, {5, "1999-11-02"}},
		},
	},
	15263: { // McLintock!
		Companies: []int{800018}, Countries: []string{"US"}, Budget: 4000000, Revenue: 14500000,
		Cast: []discCastRef{{"John Wayne", "G.W. McLintock"}, {"Maureen O'Hara", "Katherine McLintock"}, {"Patrick Wayne", "Devlin Warren"}},
		Crew: []discCrewRef{director("Andrew V. McLaglen"), writer("James Edward Grant"), producer("Michael Wayne")},
	},
	16093: { // Carnival of Souls
		Companies: []int{800019}, Countries: []string{"US"}, Budget: 30000,
		Cast: []discCastRef{{"Candace Hilligoss", "Mary Henry"}, {"Frances Feist", "Mrs. Thomas"}, {"Sidney Berger", "John Linden"}},
		Crew: []discCrewRef{director("Herk Harvey"), producer("Herk Harvey"), writer("John Clifford")},
	},
}

var discShowExtras = map[int]*discShowExtra{
	90001: { // Sherlock Holmes Adventures
		Companies: []int{800101, 800102}, Networks: []int{9101}, CreatedBy: []string{"Helena Ashworth"}, Countries: []string{"US"},
		Cast: []discCastRef{{"Edmund Hargreave", "Sherlock Holmes"}, {"Tobias Renwick", "Dr. John Watson"}},
		Crew: []discCrewRef{writer("Helena Ashworth"), director("Callum Reyes")},
	},
	90002: { // Classic Science Theater
		Companies: []int{800101, 800103}, Networks: []int{9101}, CreatedBy: []string{"Marta Oyelaran"}, Countries: []string{"US"},
		Cast: []discCastRef{{"Jonah Kestrel", "Host"}, {"Wren Okafor", "Voice of Gizmo"}},
		Crew: []discCrewRef{director("Felix Brandt")},
	},
	90003: { // The Public Domain Players
		Companies: []int{800101}, Networks: []int{9101}, CreatedBy: []string{"Lydia Marchetti"}, Countries: []string{"US"},
		Cast: []discCastRef{{"Ines Calloway", "Herself"}, {"Rafael Duarte", "Himself"}},
		Crew: []discCrewRef{director("Owen Fairweather")},
	},
	90004: { // Vintage Comedy Hour
		Companies: []int{800101}, Networks: []int{9102}, CreatedBy: []string{"Samuel Achebe"}, Countries: []string{"US"},
		Cast: []discCastRef{{"Beatrix Lindqvist", "Host"}, {"Samuel Achebe", "Film historian"}},
		Crew: []discCrewRef{director("Nora Halvorsen")},
	},
	90005: { // Tales from the Public Domain
		Companies: []int{800101}, Networks: []int{9101}, CreatedBy: []string{"Dev Raghunathan"}, Countries: []string{"US"},
		Cast: []discCastRef{{"Amara Sotelo", "Various"}, {"Kit Bramwell", "Various"}},
		Crew: []discCrewRef{director("Ingrid Solvang")},
	},
	90006: { // Silent Film Classics
		Companies: []int{800101}, Networks: []int{9102}, CreatedBy: []string{"Clara Voss"}, Countries: []string{"US"},
		Cast: []discCastRef{{"Theodore Mbeki", "Narrator"}, {"Clara Voss", "Herself"}},
		Crew: []discCrewRef{director("Anselm Richter")},
	},
	90007: { // The Lantern Society
		Companies: []int{800101, 800104}, Networks: []int{9101}, CreatedBy: []string{"Juno Castellane"}, Countries: []string{"US"},
		Cast: []discCastRef{{"Rosalind Okonkwo", "The Archivist"}, {"Milo Tremaine", "The Skeptic"}},
		Crew: []discCrewRef{director("Ezra Lindahl")},
	},
}

// discShowNetworkName is the network a show airs on, shared by the TMDB
// detail payload, the Sonarr series document, and the Trakt show object so
// the three surfaces agree. "Cantina" for a show the table does not know.
func discShowNetworkName(tmdbID int) string {
	if x := discShowExtras[tmdbID]; x != nil && len(x.Networks) > 0 {
		if n, ok := discCompanyByID(x.Networks[0]); ok {
			return n.Name
		}
	}
	return "Cantina"
}

// discMustPerson resolves a credited name or panics: the tables above are
// the one place a credit is written, and a typo must fail at startup.
func discMustPerson(name string, where any) *DemoPerson {
	p, ok := discPersonByName(name)
	if !ok {
		panic(fmt.Sprintf("data_credits: unknown person %q credited on %v", name, where))
	}
	return p
}

// discLinkCredits derives every person's filmography from the per-title
// tables, in ascending title id order. It runs from init (both tables are
// package-level literals, so no cross-file init ordering is involved and
// no state accessor is touched).
func discLinkCredits() {
	for _, p := range discPersons {
		p.CastCredits = []DemoCredit{}
		p.CrewCredits = []DemoCredit{}
	}
	movieIDs := make([]int, 0, len(discMovieExtras))
	for id := range discMovieExtras {
		movieIDs = append(movieIDs, id)
	}
	sort.Ints(movieIDs)
	for _, id := range movieIDs {
		x := discMovieExtras[id]
		for _, c := range x.Cast {
			p := discMustPerson(c.Person, id)
			p.CastCredits = append(p.CastCredits, DemoCredit{TmdbID: id, MediaType: mediaTypeMovie, Character: c.Character})
		}
		for _, c := range x.Crew {
			p := discMustPerson(c.Person, id)
			p.CrewCredits = append(p.CrewCredits, DemoCredit{TmdbID: id, MediaType: mediaTypeMovie, Job: c.Job, Department: c.Department})
		}
	}
	showIDs := make([]int, 0, len(discShowExtras))
	for id := range discShowExtras {
		showIDs = append(showIDs, id)
	}
	sort.Ints(showIDs)
	for _, id := range showIDs {
		x := discShowExtras[id]
		for _, name := range x.CreatedBy {
			discMustPerson(name, id)
		}
		for _, c := range x.Cast {
			p := discMustPerson(c.Person, id)
			p.CastCredits = append(p.CastCredits, DemoCredit{TmdbID: id, MediaType: mediaTypeTV, Character: c.Character})
		}
		for _, c := range x.Crew {
			p := discMustPerson(c.Person, id)
			p.CrewCredits = append(p.CrewCredits, DemoCredit{TmdbID: id, MediaType: mediaTypeTV, Job: c.Job, Department: c.Department})
		}
	}
}

func init() {
	discLinkCredits()
}
