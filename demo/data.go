package main

import (
	"fmt"
	"strings"
	"time"
)

// ─── TMDB-shaped types ──────────────────────────────────

type TMDBListResponse struct {
	Page         int           `json:"page"`
	Results      []interface{} `json:"results"`
	TotalPages   int           `json:"total_pages"`
	TotalResults int           `json:"total_results"`
}

type TMDBMovie struct {
	ID               int     `json:"id"`
	Title            string  `json:"title"`
	Overview         string  `json:"overview"`
	ReleaseDate      string  `json:"release_date"`
	VoteAverage      float64 `json:"vote_average"`
	VoteCount        int     `json:"vote_count"`
	Popularity       float64 `json:"popularity"`
	PosterPath       *string `json:"poster_path"`
	BackdropPath     *string `json:"backdrop_path"`
	GenreIDs         []int   `json:"genre_ids"`
	OriginalLanguage string  `json:"original_language"`
	OriginalTitle    string  `json:"original_title"`
	Adult            bool    `json:"adult"`
	Video            bool    `json:"video"`
	MediaType        string  `json:"media_type,omitempty"`
}

type TMDBMovieDetail struct {
	ID               int         `json:"id"`
	Title            string      `json:"title"`
	Overview         string      `json:"overview"`
	ReleaseDate      string      `json:"release_date"`
	VoteAverage      float64     `json:"vote_average"`
	VoteCount        int         `json:"vote_count"`
	Popularity       float64     `json:"popularity"`
	PosterPath       *string     `json:"poster_path"`
	BackdropPath     *string     `json:"backdrop_path"`
	Genres           []TMDBGenre `json:"genres"`
	Runtime          int         `json:"runtime"`
	Status           string      `json:"status"`
	Tagline          string      `json:"tagline"`
	Budget           int         `json:"budget"`
	Revenue          int         `json:"revenue"`
	ImdbID           string      `json:"imdb_id"`
	OriginalLanguage string      `json:"original_language"`
	OriginalTitle    string      `json:"original_title"`
	Adult            bool        `json:"adult"`
	Video            bool        `json:"video"`
	Videos           TMDBVideos  `json:"videos"`
	Homepage         string      `json:"homepage"`
}

type TMDBTVShow struct {
	ID               int      `json:"id"`
	Name             string   `json:"name"`
	Overview         string   `json:"overview"`
	FirstAirDate     string   `json:"first_air_date"`
	VoteAverage      float64  `json:"vote_average"`
	VoteCount        int      `json:"vote_count"`
	Popularity       float64  `json:"popularity"`
	PosterPath       *string  `json:"poster_path"`
	BackdropPath     *string  `json:"backdrop_path"`
	GenreIDs         []int    `json:"genre_ids"`
	OriginalLanguage string   `json:"original_language"`
	OriginalName     string   `json:"original_name"`
	MediaType        string   `json:"media_type,omitempty"`
	OriginCountry    []string `json:"origin_country"`
}

type TMDBTVDetail struct {
	ID               int             `json:"id"`
	Name             string          `json:"name"`
	Overview         string          `json:"overview"`
	FirstAirDate     string          `json:"first_air_date"`
	VoteAverage      float64         `json:"vote_average"`
	VoteCount        int             `json:"vote_count"`
	Popularity       float64         `json:"popularity"`
	PosterPath       *string         `json:"poster_path"`
	BackdropPath     *string         `json:"backdrop_path"`
	Genres           []TMDBGenre     `json:"genres"`
	NumberOfSeasons  int             `json:"number_of_seasons"`
	NumberOfEpisodes int             `json:"number_of_episodes"`
	Status           string          `json:"status"`
	Tagline          string          `json:"tagline"`
	Type             string          `json:"type"`
	OriginalLanguage string          `json:"original_language"`
	OriginalName     string          `json:"original_name"`
	OriginCountry    []string        `json:"origin_country"`
	Videos           TMDBVideos      `json:"videos"`
	ExternalIDs      TMDBExternalIDs `json:"external_ids"`
	Homepage         string          `json:"homepage"`
}

type TMDBGenre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type TMDBVideos struct {
	Results []TMDBVideo `json:"results"`
}

type TMDBVideo struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Name     string `json:"name"`
	Site     string `json:"site"`
	Size     int    `json:"size"`
	Type     string `json:"type"`
	Official bool   `json:"official"`
}

type TMDBExternalIDs struct {
	ImdbID *string `json:"imdb_id"`
	TvdbID *int    `json:"tvdb_id"`
	ID     int     `json:"id"`
}

type TMDBPerson struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Biography    string   `json:"biography"`
	Birthday     string   `json:"birthday"`
	Deathday     *string  `json:"deathday"`
	PlaceOfBirth string   `json:"place_of_birth"`
	ProfilePath  *string  `json:"profile_path"`
	KnownForDept string   `json:"known_for_department"`
	Popularity   float64  `json:"popularity"`
	Gender       int      `json:"gender"`
	Adult        bool     `json:"adult"`
	ImdbID       string   `json:"imdb_id"`
	AlsoKnownAs  []string `json:"also_known_as"`
	Homepage     *string  `json:"homepage"`
	MediaType    string   `json:"media_type,omitempty"`
}

type TMDBPersonCredits struct {
	ID   int                    `json:"id"`
	Cast []TMDBPersonCreditItem `json:"cast"`
	Crew []TMDBPersonCreditItem `json:"crew"`
}

type TMDBPersonCreditItem struct {
	ID           int     `json:"id"`
	Title        string  `json:"title,omitempty"`
	Name         string  `json:"name,omitempty"`
	MediaType    string  `json:"media_type"`
	Overview     string  `json:"overview"`
	PosterPath   *string `json:"poster_path"`
	ReleaseDate  string  `json:"release_date,omitempty"`
	FirstAirDate string  `json:"first_air_date,omitempty"`
	VoteAverage  float64 `json:"vote_average"`
	Character    string  `json:"character,omitempty"`
	Job          string  `json:"job,omitempty"`
	Department   string  `json:"department,omitempty"`
}

// ─── Trakt-shaped types ─────────────────────────────────

type TraktTrendingMovie struct {
	Watchers int        `json:"watchers"`
	Movie    TraktMovie `json:"movie"`
}

type TraktTrendingShow struct {
	Watchers int       `json:"watchers"`
	Show     TraktShow `json:"show"`
}

type TraktMovie struct {
	Title string   `json:"title"`
	Year  int      `json:"year"`
	IDs   TraktIDs `json:"ids"`
}

type TraktShow struct {
	Title string   `json:"title"`
	Year  int      `json:"year"`
	IDs   TraktIDs `json:"ids"`
}

type TraktIDs struct {
	Trakt int    `json:"trakt"`
	Slug  string `json:"slug"`
	IMDB  string `json:"imdb"`
	TMDB  int    `json:"tmdb"`
	TVDB  int    `json:"tvdb,omitempty"`
}

type TraktList struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Privacy        string `json:"privacy"`
	ShareLink      string `json:"share_link"`
	Type           string `json:"type"`
	DisplayNumbers bool   `json:"display_numbers"`
	AllowComments  bool   `json:"allow_comments"`
	SortBy         string `json:"sort_by"`
	SortHow        string `json:"sort_how"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	ItemCount      int    `json:"item_count"`
	CommentCount   int    `json:"comment_count"`
	Likes          int    `json:"likes"`
	IDs            struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
	} `json:"ids"`
	User struct {
		Username string `json:"username"`
		Private  bool   `json:"private"`
		Name     string `json:"name"`
		VIP      bool   `json:"vip"`
		IDs      struct {
			Slug string `json:"slug"`
		} `json:"ids"`
	} `json:"user"`
}

type TraktCalendarItem struct {
	FirstAired string `json:"first_aired"`
	Episode    struct {
		Season int      `json:"season"`
		Number int      `json:"number"`
		Title  string   `json:"title"`
		IDs    TraktIDs `json:"ids"`
	} `json:"episode"`
	Show TraktShow `json:"show"`
}

type TraktAnticipatedItem struct {
	ListCount int         `json:"list_count"`
	Movie     *TraktMovie `json:"movie,omitempty"`
	Show      *TraktShow  `json:"show,omitempty"`
}

type TraktListItem struct {
	Rank     int         `json:"rank"`
	ID       int         `json:"id"`
	ListedAt string      `json:"listed_at"`
	Type     string      `json:"type"`
	Movie    *TraktMovie `json:"movie,omitempty"`
	Show     *TraktShow  `json:"show,omitempty"`
}

// ─── Catalog data ───────────────────────────────────────

type movieEntry struct {
	tmdb   TMDBMovie
	detail TMDBMovieDetail
}

type tvEntry struct {
	tmdb   TMDBTVShow
	detail TMDBTVDetail
}

type personEntry struct {
	person  TMDBPerson
	credits TMDBPersonCredits
}

var movies []movieEntry
var tvShows []tvEntry
var persons []personEntry

var movieGenres = []TMDBGenre{
	{ID: 28, Name: "Action"}, {ID: 12, Name: "Adventure"}, {ID: 16, Name: "Animation"},
	{ID: 35, Name: "Comedy"}, {ID: 80, Name: "Crime"}, {ID: 99, Name: "Documentary"},
	{ID: 18, Name: "Drama"}, {ID: 10751, Name: "Family"}, {ID: 14, Name: "Fantasy"},
	{ID: 36, Name: "History"}, {ID: 27, Name: "Horror"}, {ID: 10402, Name: "Music"},
	{ID: 9648, Name: "Mystery"}, {ID: 10749, Name: "Romance"}, {ID: 878, Name: "Science Fiction"},
	{ID: 53, Name: "Thriller"}, {ID: 10752, Name: "War"}, {ID: 37, Name: "Western"},
}

var tvGenres = []TMDBGenre{
	{ID: 10759, Name: "Action & Adventure"}, {ID: 16, Name: "Animation"},
	{ID: 35, Name: "Comedy"}, {ID: 80, Name: "Crime"}, {ID: 99, Name: "Documentary"},
	{ID: 18, Name: "Drama"}, {ID: 10751, Name: "Family"}, {ID: 10762, Name: "Kids"},
	{ID: 9648, Name: "Mystery"}, {ID: 10763, Name: "News"}, {ID: 10764, Name: "Reality"},
	{ID: 10765, Name: "Sci-Fi & Fantasy"}, {ID: 10766, Name: "Soap"}, {ID: 10767, Name: "Talk"},
	{ID: 10768, Name: "War & Politics"}, {ID: 37, Name: "Western"},
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func init() {
	type md struct {
		id       int
		title    string
		overview string
		date     string
		vote     float64
		votes    int
		pop      float64
		genres   []TMDBGenre
		genreIDs []int
		runtime  int
		tagline  string
		imdb     string
		lang     string
		origT    string
	}

	movieData := []md{
		{987, "Night of the Living Dead", "A group of people hide from bloodthirsty zombies in a farmhouse. This groundbreaking 1968 horror film by George A. Romero established the modern zombie genre and remains one of the most influential independent films ever made.", "1968-10-01", 7.5, 1842, 42.5, []TMDBGenre{{27, "Horror"}}, []int{27}, 96, "They won't stay dead!", "tt0063350", "en", "Night of the Living Dead"},
		{653, "Nosferatu", "Vampire Count Orlok expresses interest in a new residence and real estate agent Hutter's wife. A masterpiece of German Expressionism and one of the earliest surviving vampire films.", "1922-03-04", 7.8, 2134, 38.2, []TMDBGenre{{27, "Horror"}}, []int{27}, 94, "A Symphony of Horror", "tt0013442", "de", "Nosferatu, eine Symphonie des Grauens"},
		{3085, "His Girl Friday", "A newspaper editor uses every trick in the book to keep his ace reporter ex-wife from remarrying. Howard Hawks' rapid-fire comedy is considered one of the greatest screwball comedies ever made.", "1940-01-11", 7.7, 892, 28.1, []TMDBGenre{{35, "Comedy"}, {10749, "Romance"}}, []int{35, 10749}, 92, "The funniest love story ever told!", "tt0032599", "en", "His Girl Friday"},
		{961, "The General", "When Union spies steal an engineer's beloved locomotive, he pursues it single-handedly and straight through enemy lines. Buster Keaton's silent masterpiece is widely regarded as one of the greatest films ever made.", "1926-12-31", 8.1, 1567, 35.7, []TMDBGenre{{35, "Comedy"}, {28, "Action"}, {10752, "War"}}, []int{35, 28, 10752}, 67, "The greatest locomotive chase in history!", "tt0017925", "en", "The General"},
		{19, "Metropolis", "In a futuristic city sharply divided between the working class and the city planners, the son of the city's mastermind falls in love with a working-class prophet who predicts the coming of a savior to mediate their differences.", "1927-01-10", 8.3, 3456, 48.9, []TMDBGenre{{878, "Science Fiction"}, {18, "Drama"}}, []int{878, 18}, 153, "There can be no understanding between the hands and the brain unless the heart acts as mediator.", "tt0017136", "de", "Metropolis"},
		{1942, "A Trip to the Moon", "A group of astronomers go on an expedition to the Moon. Georges Méliès' pioneering 1902 film is considered one of the first science fiction films and a landmark in cinema history.", "1902-09-01", 8.0, 2890, 33.4, []TMDBGenre{{878, "Science Fiction"}, {12, "Adventure"}}, []int{878, 12}, 13, "An extraordinary voyage to the Moon", "tt0000417", "fr", "Le Voyage dans la Lune"},
		{234, "The Cabinet of Dr. Caligari", "Francis, a young man, recalls in his memory the disturbing events involving himself and his close friend Alan with the insane Dr. Caligari and his sleepwalking accomplice Cesare.", "1920-02-26", 8.0, 1678, 31.2, []TMDBGenre{{27, "Horror"}, {18, "Drama"}}, []int{27, 18}, 76, "You must become Caligari!", "tt0010323", "de", "Das Cabinet des Dr. Caligari"},
		{1480, "Charade", "Romance and suspense ensue in Paris as a woman is pursued by several men who want a fortune her late husband had stolen. Often called 'the best Hitchcock movie Hitchcock never made.'", "1963-12-05", 7.8, 1234, 36.8, []TMDBGenre{{53, "Thriller"}, {35, "Comedy"}, {9648, "Mystery"}}, []int{53, 35, 9648}, 113, "You can expect the unexpected when they play...", "tt0056923", "en", "Charade"},
		{25862, "D.O.A.", "A businessman has been poisoned with a slow-acting toxin and has only a day or two to live. He sets out to find his own killer in this classic film noir.", "1949-12-30", 7.2, 456, 18.5, []TMDBGenre{{53, "Thriller"}, {80, "Crime"}}, []int{53, 80}, 83, "A man investigates his own murder", "tt0041699", "en", "D.O.A."},
		{27573, "The Phantom of the Opera", "A disfigured musical genius lurks beneath the Paris Opera House, exercising a reign of terror over all who inhabit it. Lon Chaney's legendary performance in this 1925 silent classic remains iconic.", "1925-09-06", 7.5, 789, 24.3, []TMDBGenre{{27, "Horror"}, {18, "Drama"}}, []int{27, 18}, 93, "The terror beneath the opera", "tt0016220", "en", "The Phantom of the Opera"},
		{27405, "The Little Shop of Horrors", "A clumsy young man nurtures a plant and discovers that it's carnivorous, forcing him to kill to feed it. Roger Corman's legendary low-budget comedy was famously shot in just two days.", "1960-09-14", 6.8, 567, 22.1, []TMDBGenre{{35, "Comedy"}, {27, "Horror"}}, []int{35, 27}, 72, "Don't feed the plants!", "tt0054033", "en", "The Little Shop of Horrors"},
		{2108, "Plan 9 from Outer Space", "Aliens resurrect dead humans as zombies and vampires to stop humanity from creating the Solaranite bomb. Ed Wood's legendary film has been called 'the worst movie ever made,' gaining a massive cult following.", "1957-07-22", 5.2, 1023, 26.7, []TMDBGenre{{878, "Science Fiction"}, {27, "Horror"}}, []int{878, 27}, 79, "Unspeakable horrors from outer space paralyze the living and resurrect the dead!", "tt0052077", "en", "Plan 9 from Outer Space"},
		{42832, "The Last Man on Earth", "When a disease turns all of humanity into the undead, the last man alive must struggle to survive using science as his weapon. Vincent Price stars in this influential 1964 adaptation of Richard Matheson's 'I Am Legend.'", "1964-03-08", 6.9, 678, 19.8, []TMDBGenre{{27, "Horror"}, {878, "Science Fiction"}}, []int{27, 878}, 86, "Alive among the walking dead!", "tt0058700", "en", "The Last Man on Earth"},
		{43861, "Suddenly", "Three assassins, led by a psychopathic ex-soldier, take over a house overlooking a railroad station in a small town, in order to assassinate the President.", "1954-10-07", 6.9, 345, 15.2, []TMDBGenre{{53, "Thriller"}, {80, "Crime"}}, []int{53, 80}, 77, "In a split second, a town is taken hostage!", "tt0047556", "en", "Suddenly"},
		{44208, "Detour", "A down-on-his-luck New York musician hitchhikes to California, but his luck changes for the worse when he meets a woman with a dark secret. Edgar G. Ulmer's masterpiece of low-budget film noir.", "1945-11-30", 7.2, 412, 14.8, []TMDBGenre{{53, "Thriller"}, {80, "Crime"}, {18, "Drama"}}, []int{53, 80, 18}, 67, "He went searching for love... but fate forced a DETOUR!", "tt0037638", "en", "Detour"},
		{3087, "The 39 Steps", "A man in London tries to help a counter-espionage agent but finds himself on the run from the police as a murder suspect. Alfred Hitchcock's classic 1935 thriller set the template for the 'wrong man' genre.", "1935-06-06", 7.6, 1089, 27.4, []TMDBGenre{{53, "Thriller"}, {9648, "Mystery"}}, []int{53, 9648}, 86, "The man with the missing finger!", "tt0026029", "en", "The 39 Steps"},
		{10098, "McLintock!", "Cattle baron John McLintock deals with his estranged wife, a young woman's parents, and local politics. John Wayne stars in this rollicking 1963 Western comedy.", "1963-11-13", 6.9, 456, 20.3, []TMDBGenre{{37, "Western"}, {35, "Comedy"}}, []int{37, 35}, 127, "He tames the West and the women!", "tt0057298", "en", "McLintock!"},
		{17057, "Carnival of Souls", "After a traumatic accident, a woman becomes drawn to a mysterious abandoned carnival. Herk Harvey's 1962 low-budget horror film has become one of the most influential cult classics in cinema.", "1962-09-26", 7.1, 567, 21.6, []TMDBGenre{{27, "Horror"}, {9648, "Mystery"}}, []int{27, 9648}, 78, "She escaped death... only to face something worse", "tt0055830", "en", "Carnival of Souls"},
	}

	for _, m := range movieData {
		poster := "/" + strings.ReplaceAll(strings.ToLower(m.title), " ", "_") + "_poster.jpg"
		backdrop := "/" + strings.ReplaceAll(strings.ToLower(m.title), " ", "_") + "_backdrop.jpg"
		movies = append(movies, movieEntry{
			tmdb: TMDBMovie{
				ID: m.id, Title: m.title, Overview: m.overview, ReleaseDate: m.date,
				VoteAverage: m.vote, VoteCount: m.votes, Popularity: m.pop,
				PosterPath: &poster, BackdropPath: &backdrop, GenreIDs: m.genreIDs,
				OriginalLanguage: m.lang, OriginalTitle: m.origT,
			},
			detail: TMDBMovieDetail{
				ID: m.id, Title: m.title, Overview: m.overview, ReleaseDate: m.date,
				VoteAverage: m.vote, VoteCount: m.votes, Popularity: m.pop,
				PosterPath: &poster, BackdropPath: &backdrop, Genres: m.genres,
				Runtime: m.runtime, Status: "Released", Tagline: m.tagline, ImdbID: m.imdb,
				OriginalLanguage: m.lang, OriginalTitle: m.origT,
				Videos: TMDBVideos{Results: []TMDBVideo{}},
			},
		})
	}

	type td struct {
		id       int
		name     string
		overview string
		date     string
		vote     float64
		votes    int
		pop      float64
		genres   []TMDBGenre
		genreIDs []int
		seasons  int
		episodes int
		status   string
		tvType   string
		tvdbID   int
	}

	tvData := []td{
		{90001, "Sherlock Holmes Adventures", "Follow the world's greatest detective as he solves baffling mysteries in Victorian London alongside his loyal companion Dr. Watson. Based on Arthur Conan Doyle's beloved public domain stories.", "2020-01-15", 8.2, 1234, 45.6, []TMDBGenre{{9648, "Mystery"}, {18, "Drama"}}, []int{9648, 18}, 4, 48, "Returning Series", "Scripted", 390001},
		{90002, "Classic Science Theater", "A witty host and their robot companions watch and humorously comment on classic public domain science fiction films, turning terrible movies into comedy gold.", "2019-06-01", 8.5, 2345, 52.3, []TMDBGenre{{35, "Comedy"}, {10765, "Sci-Fi & Fantasy"}}, []int{35, 10765}, 6, 132, "Returning Series", "Scripted", 390002},
		{90003, "The Public Domain Players", "A talented ensemble cast performs adaptations of classic public domain literature and plays, from Shakespeare to H.G. Wells, breathing new life into timeless stories.", "2021-03-10", 7.8, 678, 28.9, []TMDBGenre{{35, "Comedy"}, {18, "Drama"}}, []int{35, 18}, 3, 36, "Ended", "Scripted", 390003},
		{90004, "Vintage Comedy Hour", "A celebration of classic comedy featuring curated clips and context from the golden age of comedy, featuring works by Buster Keaton, Charlie Chaplin, Harold Lloyd, and other comedy pioneers.", "2018-09-22", 7.5, 890, 32.1, []TMDBGenre{{35, "Comedy"}, {99, "Documentary"}}, []int{35, 99}, 5, 60, "Returning Series", "Scripted", 390004},
		{90005, "Tales from the Public Domain", "An anthology series that adapts classic public domain short stories, fairy tales, and myths into modern dramatic episodes, featuring a rotating cast of acclaimed actors.", "2022-10-31", 7.9, 456, 25.4, []TMDBGenre{{18, "Drama"}, {10765, "Sci-Fi & Fantasy"}}, []int{18, 10765}, 2, 16, "Returning Series", "Scripted", 390005},
		{90006, "Silent Film Classics", "An in-depth documentary series exploring the art, history, and lasting influence of silent cinema, featuring restored footage and expert commentary on public domain masterpieces.", "2021-01-05", 8.0, 345, 18.7, []TMDBGenre{{99, "Documentary"}}, []int{99}, 3, 24, "Ended", "Documentary", 390006},
	}

	for _, t := range tvData {
		poster := "/" + strings.ReplaceAll(strings.ToLower(t.name), " ", "_") + "_poster.jpg"
		backdrop := "/" + strings.ReplaceAll(strings.ToLower(t.name), " ", "_") + "_backdrop.jpg"
		tvShows = append(tvShows, tvEntry{
			tmdb: TMDBTVShow{
				ID: t.id, Name: t.name, Overview: t.overview, FirstAirDate: t.date,
				VoteAverage: t.vote, VoteCount: t.votes, Popularity: t.pop,
				PosterPath: &poster, BackdropPath: &backdrop, GenreIDs: t.genreIDs,
				OriginalLanguage: "en", OriginalName: t.name, OriginCountry: []string{"US"},
			},
			detail: TMDBTVDetail{
				ID: t.id, Name: t.name, Overview: t.overview, FirstAirDate: t.date,
				VoteAverage: t.vote, VoteCount: t.votes, Popularity: t.pop,
				PosterPath: &poster, BackdropPath: &backdrop, Genres: t.genres,
				NumberOfSeasons: t.seasons, NumberOfEpisodes: t.episodes,
				Status: t.status, Type: t.tvType,
				OriginalLanguage: "en", OriginalName: t.name, OriginCountry: []string{"US"},
				Videos: TMDBVideos{Results: []TMDBVideo{}},
				ExternalIDs: TMDBExternalIDs{
					TvdbID: intPtr(t.tvdbID), ID: t.id,
				},
			},
		})
	}

	// Fix external IDs for TV shows
	for i := range tvShows {
		imdb := "tt" + fmt.Sprintf("%07d", tvShows[i].tmdb.ID)
		tvShows[i].detail.ExternalIDs.ImdbID = &imdb
	}

	bkDeathday := "1966-02-01"
	fwmDeathday := "1931-03-11"

	persons = []personEntry{
		{
			person: TMDBPerson{
				ID: 1, Name: "Buster Keaton",
				Biography: "Joseph Frank Keaton, known professionally as Buster Keaton, was an American actor, comedian, film director, producer, screenwriter, and stunt performer. He is best known for his silent films, in which his trademark was physical comedy with a consistently stoic, deadpan expression.",
				Birthday: "1895-10-04", Deathday: &bkDeathday, PlaceOfBirth: "Piqua, Kansas, USA",
				KnownForDept: "Acting", Popularity: 35.2, Gender: 2, ImdbID: "nm0000036",
			},
			credits: TMDBPersonCredits{
				ID: 1,
				Cast: []TMDBPersonCreditItem{
					{ID: 961, Title: "The General", MediaType: "movie", Overview: "When Union spies steal an engineer's beloved locomotive...", VoteAverage: 8.1, Character: "Johnnie Gray"},
				},
				Crew: []TMDBPersonCreditItem{
					{ID: 961, Title: "The General", MediaType: "movie", Overview: "When Union spies steal an engineer's beloved locomotive...", VoteAverage: 8.1, Job: "Director", Department: "Directing"},
				},
			},
		},
		{
			person: TMDBPerson{
				ID: 2, Name: "Fritz Lang",
				Biography: "Friedrich Christian Anton Lang was an Austrian-German-American filmmaker, screenwriter, and occasional film producer and actor. One of the best known practitioners of German Expressionism.",
				Birthday: "1890-12-05", Deathday: strPtr("1976-08-02"), PlaceOfBirth: "Vienna, Austria-Hungary",
				KnownForDept: "Directing", Popularity: 28.9, Gender: 2, ImdbID: "nm0000485",
			},
			credits: TMDBPersonCredits{
				ID: 2,
				Cast: []TMDBPersonCreditItem{},
				Crew: []TMDBPersonCreditItem{
					{ID: 19, Title: "Metropolis", MediaType: "movie", Overview: "In a futuristic city sharply divided...", VoteAverage: 8.3, Job: "Director", Department: "Directing"},
				},
			},
		},
		{
			person: TMDBPerson{
				ID: 3, Name: "F.W. Murnau",
				Biography: "Friedrich Wilhelm Murnau was a German film director who was a prominent figure during the Golden Age of Weimar cinema.",
				Birthday: "1888-12-28", Deathday: &fwmDeathday, PlaceOfBirth: "Bielefeld, Germany",
				KnownForDept: "Directing", Popularity: 22.4, Gender: 2, ImdbID: "nm0003638",
			},
			credits: TMDBPersonCredits{
				ID: 3,
				Cast: []TMDBPersonCreditItem{},
				Crew: []TMDBPersonCreditItem{
					{ID: 653, Title: "Nosferatu", MediaType: "movie", Overview: "Vampire Count Orlok expresses interest in a new residence...", VoteAverage: 7.8, Job: "Director", Department: "Directing"},
				},
			},
		},
		{
			person: TMDBPerson{
				ID: 4, Name: "George A. Romero",
				Biography: "George Andrew Romero was an American-Canadian filmmaker, writer, and editor, best known for his series of zombie films. He is considered the father of the modern zombie genre.",
				Birthday: "1940-02-04", Deathday: strPtr("2017-07-16"), PlaceOfBirth: "The Bronx, New York, USA",
				KnownForDept: "Directing", Popularity: 25.1, Gender: 2, ImdbID: "nm0001681",
			},
			credits: TMDBPersonCredits{
				ID: 4,
				Cast: []TMDBPersonCreditItem{},
				Crew: []TMDBPersonCreditItem{
					{ID: 987, Title: "Night of the Living Dead", MediaType: "movie", Overview: "A group of people hide from bloodthirsty zombies...", VoteAverage: 7.5, Job: "Director", Department: "Directing"},
				},
			},
		},
	}
}

// ─── Lookup helpers ─────────────────────────────────────

func movieByID(id int) *movieEntry {
	for i := range movies {
		if movies[i].tmdb.ID == id {
			return &movies[i]
		}
	}
	return nil
}

func tvByID(id int) *tvEntry {
	for i := range tvShows {
		if tvShows[i].tmdb.ID == id {
			return &tvShows[i]
		}
	}
	return nil
}

func personByID(id int) *personEntry {
	for i := range persons {
		if persons[i].person.ID == id {
			return &persons[i]
		}
	}
	return nil
}

func searchMovies(query string) []movieEntry {
	q := strings.ToLower(query)
	var results []movieEntry
	for _, m := range movies {
		if strings.Contains(strings.ToLower(m.tmdb.Title), q) {
			results = append(results, m)
		}
	}
	return results
}

func searchTV(query string) []tvEntry {
	q := strings.ToLower(query)
	var results []tvEntry
	for _, t := range tvShows {
		if strings.Contains(strings.ToLower(t.tmdb.Name), q) {
			results = append(results, t)
		}
	}
	return results
}

func searchPersons(query string) []personEntry {
	q := strings.ToLower(query)
	var results []personEntry
	for _, p := range persons {
		if strings.Contains(strings.ToLower(p.person.Name), q) {
			results = append(results, p)
		}
	}
	return results
}

func movieTitle(tmdbID int) string {
	if m := movieByID(tmdbID); m != nil {
		return m.tmdb.Title
	}
	return "Unknown Title"
}

func tvTitle(tmdbID int) string {
	if t := tvByID(tmdbID); t != nil {
		return t.tmdb.Name
	}
	return "Unknown Show"
}

func moviesAsResults(entries []movieEntry) []interface{} {
	results := make([]interface{}, len(entries))
	for i, m := range entries {
		mv := m.tmdb
		mv.MediaType = "movie"
		results[i] = mv
	}
	return results
}

func tvAsResults(entries []tvEntry) []interface{} {
	results := make([]interface{}, len(entries))
	for i, t := range entries {
		tv := t.tmdb
		tv.MediaType = "tv"
		results[i] = tv
	}
	return results
}

func allMediaResults() []interface{} {
	var results []interface{}
	mi, ti := 0, 0
	for mi < len(movies) || ti < len(tvShows) {
		if mi < len(movies) {
			mv := movies[mi].tmdb
			mv.MediaType = "movie"
			results = append(results, mv)
			mi++
		}
		if ti < len(tvShows) {
			tv := tvShows[ti].tmdb
			tv.MediaType = "tv"
			results = append(results, tv)
			ti++
		}
	}
	return results
}

var aiResponses = []string{
	"I'd love to help you discover some classic films! Our collection features iconic public domain movies spanning from the early 1900s to the 1960s. What genre are you in the mood for? We have horror classics like Nosferatu and Night of the Living Dead, sci-fi gems like Metropolis, and witty comedies like His Girl Friday.",
	"Great choice! Nosferatu (1922) is a masterpiece of German Expressionism directed by F.W. Murnau. It's an unauthorized adaptation of Bram Stoker's Dracula, and despite the studio being ordered to destroy all copies, it survived and became one of the most influential horror films ever made. Max Schreck's portrayal of Count Orlok is truly unforgettable.",
	"If you're looking for something thrilling, I'd recommend Charade (1963) starring Cary Grant and Audrey Hepburn. It's often called 'the best Hitchcock movie that Hitchcock never made.' The film combines romance, comedy, and suspense in a Parisian setting. Speaking of Hitchcock, we also have The 39 Steps (1935), one of his early British thrillers!",
	"For science fiction fans, Metropolis (1927) by Fritz Lang is an absolute must-watch. It's set in a futuristic city divided between wealthy industrialists and underground workers. The visual effects were groundbreaking for its time, and the film's themes about class division remain relevant today. The iconic robot design has influenced everything from C-3PO to modern art.",
	"Night of the Living Dead (1968) by George A. Romero essentially invented the modern zombie genre. Shot on a shoestring budget in rural Pennsylvania, it became a massive cultural phenomenon. The film entered the public domain because the distributor accidentally failed to include a copyright notice on the prints. That happy accident means we can share this masterpiece freely!",
	"The General (1926) starring Buster Keaton is widely considered one of the greatest comedies ever made. It features an incredible train chase sequence that Keaton performed himself \u2014 no stunt doubles! The physical comedy and timing are simply unmatched. If you enjoy silent film comedy, this is the perfect starting point.",
}

func traktLists() []TraktList {
	now := time.Now().Format(time.RFC3339)
	makeList := func(name, desc, slug, username string, itemCount, likes int) TraktList {
		l := TraktList{
			Name: name, Description: desc, Privacy: "public", Type: "personal",
			ItemCount: itemCount, Likes: likes, SortBy: "rank", SortHow: "asc",
			CreatedAt: now, UpdatedAt: now,
		}
		l.IDs.Slug = slug
		l.User.Username = username
		l.User.Name = username
		l.User.IDs.Slug = username
		return l
	}
	return []TraktList{
		makeList("Classic Horror Essentials", "The most essential horror films from the public domain era", "classic-horror-essentials", "cinephile", 6, 1234),
		makeList("Silent Film Gems", "Hidden treasures from the silent film era", "silent-film-gems", "filmhistorian", 5, 890),
		makeList("Best of Film Noir", "The finest film noir from the public domain collection", "best-of-film-noir", "noirfan", 4, 567),
	}
}

// suppress unused import
var _ = fmt.Sprintf
