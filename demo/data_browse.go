// data_browse.go — the browse-filter vocabulary (D3 discover): watch
// providers by region, keywords, companies, languages, regions, and the
// title→attachment maps the filterable discover routes match against.
//
// Every id here is demo-local and deliberately never a real TMDB id:
// providers 9001+, keywords 700001+, companies 800001+. Company names for the
// real films are plain historical fact; the show companies and every
// provider are invented. The same company table feeds production_companies
// on title detail, so a studio chip on a title page lands on a browse grid
// that contains that title.
package main

import "strings"

// discTag is one keyword ({"id","name"}).
type discTag struct {
	ID   int
	Name string
}

// discCompany is one production company or network
// ({"id","logo_path":null,"name","origin_country"}).
type discCompany struct {
	ID            int
	Name          string
	OriginCountry string
}

// discWatchProvider is one fictional streaming service.
type discWatchProvider struct {
	ID   int
	Name string
}

// discLanguage is one TMDB /configuration/languages row.
type discLanguage struct {
	Code    string
	English string
	Native  string
}

// discRegion is one TMDB /watch/providers/regions row.
type discRegion struct {
	Code    string
	English string
	Native  string
}

// ─── Providers ──────────────────────────────────────────────────────────

var discProviders = []discWatchProvider{
	{9001, "Public Domain Streaming"},
	{9002, "Classic Cinema Channel"},
	{9003, "Archive Films"},
	{9004, "Serial Box"},
}

// discProvidersByRegion lists, per region, which providers carry movies and
// which carry TV. display_priority is the 1-based position in the list; an
// unknown region lists nothing.
var discProvidersByRegion = map[string]struct{ Movie, TV []int }{
	"US": {Movie: []int{9001, 9002, 9003}, TV: []int{9001, 9004}},
	"GB": {Movie: []int{9001, 9003}, TV: []int{9001}},
	"CA": {Movie: []int{9001, 9002}, TV: []int{9001, 9004}},
	"DE": {Movie: []int{9002}, TV: []int{9004}},
	"FR": {Movie: []int{9002, 9003}, TV: []int{9001}},
	"AU": {Movie: []int{9001}, TV: []int{9004}},
}

// Title → provider ids. 9001 carries every film except the four film noirs
// and Plan 9; 9002 the silent/classic set; 9003 the American sound-era
// films; 9004 the shows Public Domain Streaming does not.
var discMovieProviderIDs = map[int][]int{
	10331: {9001, 9003},
	653:   {9001, 9002},
	3085:  {9001, 9002},
	961:   {9001, 9002},
	19:    {9001, 9002},
	775:   {9001, 9002},
	234:   {9001, 9002},
	4808:  {9001, 9003},
	18995: {9003},
	964:   {9001, 9002},
	24452: {9001, 9003},
	10513: {9003},
	21159: {9001, 9003},
	18398: {9003},
	20367: {9003},
	260:   {9001, 9002},
	15263: {9001, 9003},
	16093: {9001, 9003},
}

var discTVProviderIDs = map[int][]int{
	90001: {9001},
	90002: {9004},
	90003: {9001},
	90004: {9004},
	90005: {9001},
	90006: {9004},
	90007: {9004},
}

// ─── Keywords ───────────────────────────────────────────────────────────

var discKeywords = []discTag{
	{700001, "zombie"},
	{700002, "vampire"},
	{700003, "silent film"},
	{700004, "film noir"},
	{700005, "german expressionism"},
	{700006, "space travel"},
	{700007, "train"},
	{700008, "newspaper"},
	{700009, "man-eating plant"},
	{700010, "dystopia"},
	{700011, "assassination"},
	{700012, "wrongly accused"},
	{700013, "cattle rancher"},
	{700014, "carnival"},
	{700015, "paris, france"},
	{700016, "victorian london"},
	{700017, "anthology"},
	{700018, "film history"},
	{700019, "riffing"},
	{700020, "ghost story"},
}

var discMovieKeywordIDs = map[int][]int{
	10331: {700001},
	653:   {700002, 700003, 700005},
	3085:  {700008},
	961:   {700003, 700007},
	19:    {700003, 700005, 700010},
	775:   {700003, 700006},
	234:   {700003, 700005},
	4808:  {700015},
	18995: {700004},
	964:   {700003, 700015},
	24452: {700009},
	10513: {700001, 700006},
	21159: {700001, 700002},
	18398: {700004, 700007, 700011},
	20367: {700004, 700012},
	260:   {700007, 700012},
	15263: {700013},
	16093: {700014},
}

var discTVKeywordIDs = map[int][]int{
	90001: {700016},
	90002: {700019},
	90003: {},
	90004: {700003, 700018},
	90005: {700017},
	90006: {700003, 700018},
	90007: {700017, 700020},
}

// ─── Companies ──────────────────────────────────────────────────────────

var discCompanies = []discCompany{
	{800001, "Image Ten", "US"},
	{800002, "Prana-Film", "DE"},
	{800003, "Columbia Pictures", "US"},
	{800004, "Buster Keaton Productions", "US"},
	{800005, "UFA", "DE"},
	{800006, "Star Film", "FR"},
	{800007, "Decla-Bioscop", "DE"},
	{800008, "Stanley Donen Films", "US"},
	{800009, "Universal Pictures", "US"},
	{800010, "Cardinal Pictures", "US"},
	{800011, "Santa Clara Productions", "US"},
	{800012, "Reynolds Pictures", "US"},
	{800013, "Associated Producers", "US"},
	{800014, "Produzioni La Regina", "IT"},
	{800015, "Libra Productions", "US"},
	{800016, "Producers Releasing Corporation", "US"},
	{800017, "Gaumont-British Picture Corporation", "GB"},
	{800018, "Batjac Productions", "US"},
	{800019, "Harcourt Productions", "US"},
	{800101, "Cantina Studios", "US"},
	{800102, "Baker Street Pictures", "GB"},
	{800103, "Riffworks", "US"},
	{800104, "Lantern House Productions", "US"},
}

var discMovieCompanyIDs = map[int][]int{
	10331: {800001},
	653:   {800002},
	3085:  {800003},
	961:   {800004},
	19:    {800005},
	775:   {800006},
	234:   {800007},
	4808:  {800008, 800009},
	18995: {800010},
	964:   {800009},
	24452: {800011},
	10513: {800012},
	21159: {800013, 800014},
	18398: {800015},
	20367: {800016},
	260:   {800017},
	15263: {800018},
	16093: {800019},
}

var discTVCompanyIDs = map[int][]int{
	90001: {800101, 800102},
	90002: {800101, 800103},
	90003: {800101},
	90004: {800101},
	90005: {800101},
	90006: {800101},
	90007: {800101, 800104},
}

// discCompanyByID looks a company (or network) up by demo-local id.
func discCompanyByID(id int) (discCompany, bool) {
	for _, c := range discCompanies {
		if c.ID == id {
			return c, true
		}
	}
	for _, n := range discNetworks {
		if n.ID == id {
			return n, true
		}
	}
	return discCompany{}, false
}

// discCompanyJSON renders the TMDB company shape shared by
// production_companies, networks, and /search/company results.
func discCompanyJSON(c discCompany) map[string]any {
	return map[string]any{
		"id":             c.ID,
		"logo_path":      nil,
		"name":           c.Name,
		"origin_country": c.OriginCountry,
	}
}

// ─── Languages and regions ──────────────────────────────────────────────

// discLanguages is served verbatim by GET /api/languages. Only en, de, and
// fr match a catalog title; the rest exist so the Language menu is a menu
// and an empty grid can say "in Korean".
var discLanguages = []discLanguage{
	{"en", "English", "English"},
	{"de", "German", "Deutsch"},
	{"fr", "French", "Français"},
	{"it", "Italian", "Italiano"},
	{"es", "Spanish", "Español"},
	{"ja", "Japanese", "日本語"},
	{"ko", "Korean", "한국어/조선말"},
	{"sv", "Swedish", "svenska"},
	{"ru", "Russian", "Pусский"},
	{"pt", "Portuguese", "Português"},
	{"xx", "No Language", "No Language"},
}

// discRegions is served by GET /api/providers/regions.
var discRegions = []discRegion{
	{"US", "United States of America", "United States"},
	{"GB", "United Kingdom", "United Kingdom"},
	{"CA", "Canada", "Canada"},
	{"DE", "Germany", "Germany"},
	{"FR", "France", "France"},
	{"AU", "Australia", "Australia"},
}

// discCountryNames names the production countries the catalog uses, in
// TMDB's English spelling.
var discCountryNames = map[string]string{
	"US": "United States of America",
	"GB": "United Kingdom",
	"DE": "Germany",
	"FR": "France",
	"IT": "Italy",
	"CA": "Canada",
	"AU": "Australia",
}

// discCountryJSON renders one production_countries entry.
func discCountryJSON(code string) map[string]any {
	name, ok := discCountryNames[code]
	if !ok {
		name = code
	}
	return map[string]any{"iso_3166_1": code, "name": name}
}

// discSpokenLanguageJSON renders one spoken_languages entry for a language
// code, falling back to the code itself for one the table does not carry.
func discSpokenLanguageJSON(code string) map[string]any {
	for _, l := range discLanguages {
		if strings.EqualFold(l.Code, code) {
			return map[string]any{"english_name": l.English, "iso_639_1": l.Code, "name": l.Native}
		}
	}
	return map[string]any{"english_name": code, "iso_639_1": code, "name": code}
}
