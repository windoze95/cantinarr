package tmdb

// DefaultAccessToken is Cantinarr's built-in TMDB v4 Read Access Token,
// registered to the project's own TMDB account.
//
// It is DELIBERATELY PUBLIC — the same model as Overseerr's bundled key. TMDB
// throttles by source IP, not by key, so one shared read-only token across
// every install is supported usage, and the token unlocks nothing on any
// Cantinarr instance. Do not move it into the encrypted credential store, and
// do not treat its appearance in the repo, images, or request headers as a
// leak. Rotation happens through a normal release.
//
// An admin-supplied token (Settings > Providers & Credentials) always takes
// precedence over this one — and that value IS a personal credential, handled
// as a secret like every other stored key.
const DefaultAccessToken = "eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiJkOGI3N2JmNTk3OGE0YmI3Mzc5NjhjMTlkZDU2ZDNmNCIsIm5iZiI6MTc4NjI1MDg0NC4yMTEsInN1YiI6IjZhNzgwNjVjNmVhOTI1ZTMwM2NiOWE1MSIsInNjb3BlcyI6WyJhcGlfcmVhZCJdLCJ2ZXJzaW9uIjoxfQ.rSz-veWS3-r4a9Dt7-1Wz9yGo5JUwGHK7T94Y2PHtic"
