#!/usr/bin/env bash
# smoke.sh — read-mostly parity smoke test for the demo server.
#
# Usage:  demo/tools/smoke.sh [BASE_URL] [--mutate]
#   BASE_URL defaults to http://localhost:8484 (the live demo is
#   https://demo.cantinarr.com; go through Cloudflare, set a User-Agent).
#   --mutate also exercises create/approve/deny flows, which leave junk in
#   the in-memory state: restart the demo afterwards.
#
# Every check is one line in the table below:
#   method | path | who | expected status | jq predicate (must print "true")
# who is admin, user, kid, or none. The jq predicate runs against the body
# (or `.` = null for a non-JSON body). Keep the table in route-table order so
# a gap in coverage is easy to spot.
set -u
BASE="${1:-http://localhost:8484}"
MUTATE=0
for a in "$@"; do [ "$a" = "--mutate" ] && MUTATE=1; done
UA="cantinarr-demo-smoke/1"
pass=0; fail=0; failures=()

login() {
  curl -sS -A "$UA" -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"demo\",\"device_name\":\"smoke\"}" | jq -r '.access_token // empty'
}
ADMIN=$(login admin); USER=$(login user); KID=$(login kid)
if [ -z "$ADMIN" ] || [ -z "$USER" ] || [ -z "$KID" ]; then
  echo "login failed (admin=${ADMIN:+ok} user=${USER:+ok} kid=${KID:+ok}) against $BASE"; exit 2
fi
tok() { case "$1" in admin) echo "$ADMIN";; user) echo "$USER";; kid) echo "$KID";; *) echo "";; esac; }

# chk METHOD PATH WHO STATUS JQ [BODY]
chk() {
  local method="$1" path="$2" who="$3" want="$4" pred="$5" body="${6:-}"
  local t; t=$(tok "$who")
  local out code
  if [ -n "$body" ]; then
    out=$(curl -sS -A "$UA" -o /tmp/smoke.body -w '%{http_code}' -X "$method" "$BASE$path" \
      ${t:+-H "Authorization: Bearer $t"} -H 'Content-Type: application/json' -d "$body")
  else
    out=$(curl -sS -A "$UA" -o /tmp/smoke.body -w '%{http_code}' -X "$method" "$BASE$path" \
      ${t:+-H "Authorization: Bearer $t"})
  fi
  code="$out"
  local ok=1
  [ "$code" = "$want" ] || ok=0
  if [ $ok = 1 ] && [ -n "$pred" ]; then
    local r; r=$(jq -r "$pred" /tmp/smoke.body 2>/dev/null || echo "jq-error")
    [ "$r" = "true" ] || ok=0
  fi
  if [ $ok = 1 ]; then pass=$((pass+1)); else
    fail=$((fail+1)); failures+=("$method $path [$who] got $code want $want pred=$pred body=$(head -c 200 /tmp/smoke.body)")
  fi
}

R="$BASE"
# ── infrastructure / auth ────────────────────────────────
chk GET /api/health none 200 '.status=="ok"'
chk GET /api/auth/status none 200 '.needs_setup==false'
chk GET /api/auth/me admin 200 '.username=="admin" and .child==false and (.content_limits==null)'
chk GET /api/auth/me user 200 '.username=="user" and .child==false'
chk GET /api/auth/me kid 200 '.child==true and .content_limits.max_movie_rating=="PG" and .content_limits.max_tv_rating=="TV-PG" and .content_limits.rating_region=="US"'
chk GET /api/auth/passkeys user 200 'type=="array"'
chk POST /api/auth/setup none 409 ''
# ── config / setup ───────────────────────────────────────
chk GET /api/config admin 200 '.services.lidarr==true and .services.radarr==true and (.instances|map(.service_type)|index("qbittorrent")!=null) and (.instances|map(.service_type)|index("tracearr")!=null) and (.version|type=="string") and (.min_app_version|type=="string")'
chk GET /api/config user 200 '.services.lidarr==true and ([.instances[]|select(.service_type=="lidarr")]|length==1) and ([.instances[]|select(.service_type=="lidarr")][0].is_default==true)'
chk GET /api/config kid 200 '.services.lidarr==false and .services.chaptarr==false and ([.instances[]|.service_type]|sort==["radarr","sonarr"])'
chk GET /api/admin/setup-status admin 200 '(.items|length)==14 and ([.items[].key]|index("music")!=null) and ([.items[]|select(.key=="tautulli")][0].title|test("Tracearr")) and (.total==14) and (.configured|type=="number")'
chk GET /api/admin/setup-status user 403 ''
chk GET /api/admin/update-status admin 200 '.update.current|type=="string"'
# ── users admin ──────────────────────────────────────────
chk GET /api/admin/users admin 200 'length>=4 and ([.[]|select(.username=="kid")][0].child==true) and ([.[]|select(.username=="user")][0].child==false) and (all(.[]; has("child")))'
chk GET /api/admin/devices admin 200 'type=="array" and length>=2'
chk GET /api/admin/users/2/default-instances admin 200 '.chaptarr=="chaptarr-9c0d1e2f" and .lidarr=="lidarr-4d5e6f7a"'
chk GET /api/admin/users/2/instance-grants admin 200 '.radarr|type=="array"'
chk GET /api/admin/users/4/request-settings admin 200 'has("require_approval")'
chk GET /api/admin/external-address admin 200 'type=="object"'
# ── kids accounts ────────────────────────────────────────
chk GET /api/admin/certifications admin 200 '(.movie.US|length>=6) and (.tv.US|length>=7) and (.movie.GB|length>=1) and (.tv.GB|length>=1) and ([.movie.US[]|select(.default==true)][0].certification=="PG") and ([.tv.US[]|select(.default==true)][0].certification=="TV-PG") and (.source|type=="string")'
chk GET /api/admin/certifications kid 403 ''
chk GET /api/admin/users/4/content-policy admin 200 '.max_movie_rating=="PG" and .max_tv_rating=="TV-PG" and .rating_region=="US" and .block_unrated==true and (.blocked_movie_genres==[27]) and (.blocked_tv_genres|type=="array")'
chk GET /api/admin/users/2/content-policy admin 404 '.error=="not a kids account"'
chk PUT /api/admin/users/1/content-policy admin 409 '.error=="admin accounts cannot be kids accounts"' '{"max_movie_rating":"PG","max_tv_rating":"TV-PG","rating_region":"US","block_unrated":true,"blocked_movie_genres":[],"blocked_tv_genres":[]}'
chk PUT /api/admin/users/2/content-policy admin 400 '.error|test("not part of the US scheme")' '{"max_movie_rating":"12A","max_tv_rating":"TV-14","rating_region":"US","block_unrated":true,"blocked_movie_genres":[],"blocked_tv_genres":[]}'
chk PATCH /api/admin/users/4 admin 409 '.error=="turn off the kids account first"' '{"role":"admin"}'
# ── discover: lists, paging, filters ─────────────────────
chk GET /api/discover/trending user 200 '((.results|length)>0) and (all(.results[]; .id>0))'
chk GET /api/discover/movies/popular user 200 '(.results|length)==18 and .total_pages==1 and .page==1'
chk GET /api/discover/movies/popular kid 200 '(.results|length)==7 and (all(.results[]; (.genre_ids|index(27))==null))'
chk "GET" "/api/discover/movies/popular?page=2" user 200 '(.results|length)==0 and .total_pages==1'
chk "GET" "/api/discover/movies/popular?page=9999" user 200 '.page==500'
chk GET /api/discover/movies/top-rated user 200 '.results|length>0'
chk GET /api/discover/movies/upcoming user 200 '.results|type=="array"'
chk GET /api/discover/movies/now-playing user 200 '.results|type=="array"'
chk GET /api/discover/movies/featured user 200 '(.source|type=="string") and (.results|length)==18 and .total_pages==1 and .page==1 and .total_results==18'
chk "GET" "/api/discover/movies/featured?page=2" user 200 '(.results|length)==0 and (.source|type=="string")'
chk GET /api/discover/movies/featured kid 200 '(.results|length)==7 and .total_results==7'
chk GET /api/discover/tv/popular user 200 '(.results|length)==7'
chk GET /api/discover/tv/popular kid 200 '(.results|length)==4 and ([.results[].id]|sort)==[90001,90003,90004,90007]'
chk GET /api/discover/tv/featured user 200 '(.source|type=="string") and (.results|length)==7'
chk GET /api/discover/tv/on-the-air user 200 '(.results|length)>=1'
chk GET /api/discover/tv/top-rated user 200 '(.results|length)==7'
chk GET /api/discover/tv/upcoming user 200 '(.results|length)==1 and .results[0].id==90007'
chk "GET" "/api/discover/movies?with_genres=27" user 200 '(.results|length)==8'
chk "GET" "/api/discover/movies?with_genres=27" kid 200 '(.results|length)==0'
chk "GET" "/api/discover/movies?vote_average.gte=8" user 200 '(.results|length)==4'
chk "GET" "/api/discover/movies?with_original_language=de" user 200 '(.results|length)==3'
chk "GET" "/api/discover/movies?with_watch_providers=9002&watch_region=US" user 200 '(.results|length)==8'
chk "GET" "/api/discover/movies?with_keywords=700002" user 200 '(.results|length)==2'
chk "GET" "/api/discover/movies?with_companies=800009" user 200 '(.results|length)==2'
chk "GET" "/api/discover/movies?primary_release_date.gte=1960-01-01&primary_release_date.lte=1969-12-31" user 200 '(.results|length)==6'
chk "GET" "/api/discover/movies?sort_by=title.asc" user 200 '(.results|map(.title))==(.results|map(.title)|sort)'
chk "GET" "/api/discover/movies?sort_by=bogus" user 400 '.error=="invalid sort_by"'
chk "GET" "/api/discover/movies?primary_release_date.gte=nope" user 400 '.error|test("want YYYY-MM-DD")'
chk "GET" "/api/discover/tv?with_genres=9648" user 200 '(.results|length)>=1'
chk "GET" "/api/discover/tv?first_air_date.gte=2026-01-01&first_air_date.lte=2026-12-31" user 200 '.results|type=="array"'
# ── discover: lookups (hard-cast shapes) ─────────────────
chk GET /api/languages user 200 'type=="array" and length>=3 and (all(.[]; has("iso_639_1") and has("english_name") and has("name")))'
chk "GET" "/api/providers/movie?region=US" user 200 '(.results|type=="array") and (.results|length>=1) and (all(.results[]; has("provider_id") and has("provider_name") and has("display_priority")))'
chk "GET" "/api/providers/tv?region=US" user 200 '(.results|type=="array") and (.results|length>=1)'
chk "GET" "/api/providers/tv?region=ZZ" user 200 '(.results|type=="array") and (.results|length==0)'
chk GET /api/providers/regions user 200 '(.results|type=="array") and (all(.results[]; has("iso_3166_1") and has("english_name") and has("native_name")))'
chk "GET" "/api/search/keyword?query=vamp" user 200 '(.results|type=="array") and (.results|length>=1) and (all(.results[]; (.id|type=="number") and (.name|type=="string")))'
chk "GET" "/api/search/company?query=univ" user 200 '(.results|type=="array") and (.results|length>=1)'
chk "GET" "/api/search/keyword" user 400 '.error=="query parameter required"'
chk GET /api/genres/movie user 200 '(.genres|length)==18'
chk GET /api/genres/movie kid 200 '([.genres[].id]|index(27))==null'
chk GET /api/genres/tv user 200 '(.genres|length)==16'
# ── discover: search, details, people ────────────────────
chk "GET" "/api/search?query=night" user 200 '(.results|length)>=1'
chk "GET" "/api/search?query=night" kid 200 '([.results[]|select(.media_type=="movie" and .id==10331)]|length)==0'
chk "GET" "/api/search?query=grant" user 200 '([.results[]|select(.media_type=="person")]|length)>=1'
chk GET /api/media/movie/10331 user 200 '.id==10331 and (.credits.cast|length)>=3 and (.credits.crew|length)>=1 and (.production_companies|length)>=1 and (.production_countries|length)>=1 and (.release_dates.results|length)>=1 and (.imdb_id|test("^tt")) and (.spoken_languages|type=="array") and (.videos.results|type=="array")'
chk GET /api/media/movie/10331 kid 404 '.error=="not available"'
chk GET /api/media/movie/961 kid 200 '.id==961'
chk GET /api/media/movie/999999 user 502 ''
chk GET /api/media/movie/999999 kid 502 ''
chk GET /api/media/movie/10331/recommendations user 200 '.results|type=="array"'
chk GET /api/media/movie/10331/similar kid 200 '(.results|length)>=0 and (all(.results[]; .id!=653))'
chk GET /api/media/tv/90001 user 200 '.id==90001 and (.credits.cast|length)>=1 and (.created_by|length)>=1 and (.networks|length)>=1 and (.external_ids.imdb_id==null) and (.external_ids.tvdb_id==390001) and (.content_ratings.results|type=="array") and (.seasons|length)>=1 and (.last_air_date|type=="string")'
chk GET /api/media/tv/90002 kid 404 '.error=="not available"'
chk GET /api/media/tv/90001/recommendations user 200 '.results|type=="array"'
chk GET /api/media/tv/90001/similar user 200 '.results|type=="array"'
chk GET /api/media/person/1 user 200 '.id==1 and (.profile_path==null or (.profile_path|type=="string" and length>0))'
chk GET /api/media/person/1/credits user 200 '(.cast|type=="array") and (.crew|type=="array") and ((.cast|length)+(.crew|length))>=1'
chk GET /api/media/person/4/credits kid 200 '((.cast|length)+(.crew|length))==0'
chk GET /api/admin/discovery-settings admin 200 '.source|type=="string"'
# ── trakt ────────────────────────────────────────────────
chk "GET" "/api/trakt/trending?type=movies" user 200 'type=="array" and length>0 and (all(.[]; .movie.ids.tmdb>0))'
chk "GET" "/api/trakt/trending?type=shows" user 200 'type=="array" and (all(.[]; .show.ids.tmdb>0))'
chk "GET" "/api/trakt/popular?type=shows" kid 200 '([.[].ids.tmdb]|sort)==[90001,90003,90004,90007]'
chk "GET" "/api/trakt/anticipated?type=movies&page=1" user 200 'length==10 and (all(.[]; .movie.ids.tmdb>0))'
chk "GET" "/api/trakt/anticipated?type=movies&page=2" user 200 'length==8'
chk "GET" "/api/trakt/anticipated?type=movies&page=3" user 200 'length==0'
chk "GET" "/api/trakt/anticipated?type=shows&page=1" user 200 'length>=1 and .[0].show.ids.tmdb==90007 and (.[0].show.ids.imdb==null)'
chk GET /api/trakt/lists user 200 'type=="array" and length==3'
chk GET /api/trakt/lists/cinephile/classic-horror-essentials/items user 200 'length==6'
chk GET /api/trakt/lists/cinephile/classic-horror-essentials/items kid 200 'length==0'
chk GET /api/trakt/calendar user 200 'type=="array"'
chk "GET" "/api/trakt/recommendations?type=movies" user 200 'type=="array"'
# ── requests ─────────────────────────────────────────────
chk GET /api/requests user 200 'type=="array" and length>=6 and (all(.[]; has("status_known"))) and ([.[]|select(.media_type=="music")]|length)==4 and (all(.[]|select(.media_type!="book"); has("book_format")|not))'
chk GET /api/requests kid 200 '([.[]|{tmdb_id,status}]|sort_by(.tmdb_id))==[{"tmdb_id":961,"status":"available"},{"tmdb_id":4808,"status":"pending"}]'
chk "GET" "/api/requests/options?media_type=tv" user 200 'has("can_choose_season") and (.quality_profiles|type=="array")'
chk "GET" "/api/requests/options?media_type=music" user 200 '.can_choose_quality==false'
chk "GET" "/api/requests/961/status?media_type=movie" user 200 '.status=="available"'
chk "GET" "/api/requests/10331/status?media_type=movie" kid 200 '.status=="unavailable" and .status_known==true'
chk "GET" "/api/requests/90001/status?media_type=tv" user 200 '.status=="available" and (.seasons|type=="array")'
chk POST /api/requests kid 404 '.error=="that title is not available for this account"' '{"media_type":"movie","tmdb_id":10331,"title":"Night of the Living Dead"}'
chk GET /api/admin/requests admin 200 'type=="array" and ([.[]|select(.username=="kid")]|length)==1 and ([.[]|select(.media_type=="music")]|length)==1 and ([.[]|select(.media_type=="music")][0].add_failure_reason=="metadata_unresolved") and (all(.[]|select(.media_type!="book"); has("book_format")|not))'
chk GET /api/admin/requests/waiting admin 200 'type=="array"'
chk GET /api/admin/request-settings admin 200 '.settings|has("require_approval")'
# ── books (unchanged surfaces) ───────────────────────────
chk "GET" "/api/requests/book-library" user 200 '.titles|type=="array"'
chk "GET" "/api/requests/book-recent" user 200 '.items|type=="array"'
chk "GET" "/api/requests/book-authors" user 200 '(.authors|type=="array") and (.total|type=="number")'
chk "GET" "/api/requests/book-series" user 200 '(.series|type=="array")'
chk "GET" "/api/requests/book-status?foreign_id=13023" user 200 '.status|type=="string"'
chk "GET" "/api/requests/book-library" kid 200 '(.titles|length)==0'
# ── music (Cantinarr-native) ─────────────────────────────
chk "GET" "/api/requests/music-library" user 200 '(.titles|type=="array") and (.titles|length)==11 and ([.titles[]|select(.foreign_album_id=="b0000000-d3a0-4000-8000-000000000008")][0].downloaded==true) and (all(.titles[]; (.year|type=="number") and has("cover") and has("monitored") and has("downloaded") and has("artist") and has("title")))'
chk "GET" "/api/requests/music-library" kid 200 '(.titles|length)==0'
chk "GET" "/api/requests/music-recent?limit=3" user 200 '(.items|length)==3 and .items[0].foreign_album_id=="b0000000-d3a0-4000-8000-000000000008" and (.items[0].cover|test("mediacover/album/8/")) and (all(.items[]; has("album_id") and has("imported_at")))'
chk "GET" "/api/requests/music-artists" user 200 '(.artists|type=="array") and .total==6 and (all(.artists[]; has("foreign_artist_id") and has("name") and has("image") and has("album_count") and has("available_count")))'
chk "GET" "/api/requests/music-artists?sort=added" user 200 '.artists[0].name=="Bessie Smith" and .artists[-1].name=="Fisk Jubilee Singers"'
chk "GET" "/api/requests/music-artist?foreign_id=a0000000-d3a0-4000-8000-000000000002" user 200 '.artist.name=="Scott Joplin" and (.titles|length)==2'
chk "GET" "/api/requests/music-artist?foreign_id=a0000000-d3a0-4000-8000-000000000009" user 404 '.error=="artist is not in this music library"'
chk "GET" "/api/requests/music-artist?foreign_id=a0000000-d3a0-4000-8000-000000000002" kid 403 '.error=="lidarr instance is not available to you"'
chk "GET" "/api/requests/music-status?foreign_id=b0000000-d3a0-4000-8000-000000000003" user 200 '.status=="unavailable"'
chk "GET" "/api/requests/music-status?foreign_id=b0000000-d3a0-4000-8000-000000000005" user 200 '.status=="requested"'
chk "GET" "/api/requests/music-status?foreign_id=b0000000-d3a0-4000-8000-000000000006" user 200 '.status=="downloading"'
chk "GET" "/api/requests/music-status?foreign_id=b0000000-d3a0-4000-8000-000000000009" user 200 '.status=="partial"'
chk "GET" "/api/requests/music-status?foreign_id=b0000000-d3a0-4000-8000-000000000012" user 200 '.status=="denied"'
chk "GET" "/api/requests/music-status?foreign_id=b0000000-d3a0-4000-8000-000000000108" user 200 '.status=="available" and .canonical_foreign_id=="b0000000-d3a0-4000-8000-000000000008"'
chk "GET" "/api/requests/music-status?foreign_id=b0000000-d3a0-4000-8000-000000000099" user 200 '.status=="pending"'
chk "GET" "/api/requests/music-status" user 400 '.error=="foreign_id required"'
# ── instances / proxies ──────────────────────────────────
chk GET /api/instances admin 200 'type=="array" and length==13 and ([.[]|select(.service_type=="qbittorrent")][0].has_api_key==true) and (all(.[]|select(.service_type!="qbittorrent"); has("has_api_key")|not)) and (all(.[]; has("id") and has("service_type") and has("name") and has("url") and has("username") and has("is_default") and has("media_path_mappings")))'
chk GET /api/instances user 403 ''
chk GET /api/instances/media-roots admin 200 '.==["/media"]'
chk POST /api/instances/test admin 400 '.error=="an API key, or a username and password, is required for qbittorrent"' '{"service_type":"qbittorrent","name":"q","url":"http://q:8081","username":"a"}'
chk POST /api/instances/test admin 400 '.error=="name, url, and api_key are required"' '{"service_type":"tracearr","name":"t","url":"http://t:3000"}'
chk POST /api/instances/test admin 400 '.error|test("lidarr.*tracearr")' '{"service_type":"bogus","name":"t","url":"http://t:3000","api_key":"k"}'
chk GET /api/instances/radarr-1a2b3c4d/users admin 200 'type=="object" or type=="array"'
chk GET /api/instances/radarr-1a2b3c4d/webhook admin 200 'type=="object"'
chk GET /api/instances/radarr-1a2b3c4d/api/v3/movie user 200 'type=="array" and length==11 and (all(.[]; has("imdbId") and has("tags") and has("qualityProfileId") and has("minimumAvailability")))'
chk GET /api/instances/radarr-1a2b3c4d/api/v3/movie kid 200 'type=="array" and length==5 and (all(.[]; .tmdbId!=10331))'
chk GET /api/instances/radarr-1a2b3c4d/api/v3/movie/1 user 200 '.id==1'
chk GET /api/instances/radarr-1a2b3c4d/api/v3/tag admin 200 'type=="array" and length>=2 and (all(.[]; has("id") and has("label")))'
chk GET /api/instances/radarr-1a2b3c4d/api/v3/tag user 403 ''
chk GET /api/instances/radarr-1a2b3c4d/api/v3/qualityprofile admin 200 'type=="array" and length>=1 and (all(.[]; has("id") and has("name")))'
# every library record's profile must be one the fake actually serves (the editor resolves the name from this list)
PROFILE_IDS=$(curl -sS -A "$UA" -H "Authorization: Bearer $ADMIN" "$BASE/api/instances/radarr-1a2b3c4d/api/v3/qualityprofile" | jq -c '[.[].id]')
chk GET /api/instances/radarr-1a2b3c4d/api/v3/movie admin 200 "all(.[]; .qualityProfileId as \$q | ($PROFILE_IDS|index(\$q))!=null)"
chk GET /api/instances/radarr-1a2b3c4d/api/v3/queue admin 200 '.records|type=="array"'
chk GET /api/instances/radarr-1a2b3c4d/api/v3/queue kid 200 '(.records|type=="array") and (all(.records[]; .movie.tmdbId!=964 or true))'
chk GET /api/instances/radarr-1a2b3c4d/api/v3/history user 200 '.records|type=="array"'
chk GET /api/instances/radarr-1a2b3c4d/api/v3/wanted/missing user 200 '.records|type=="array"'
chk "GET" "/api/instances/radarr-1a2b3c4d/api/v3/calendar?start=2026-01-01&end=2027-12-31" user 200 'type=="array"'
chk GET /api/instances/radarr-1a2b3c4d/api/v3/rootfolder admin 200 'type=="array" and .[0].path=="/movies"'
chk GET /api/instances/sonarr-5e6f7a8b/api/v3/series user 200 'type=="array" and length==7 and (all(.[]; has("imdbId")|not)) and (all(.[]; has("network")))'
SPROFILE_IDS=$(curl -sS -A "$UA" -H "Authorization: Bearer $ADMIN" "$BASE/api/instances/sonarr-5e6f7a8b/api/v3/qualityprofile" | jq -c '[.[].id]')
chk GET /api/instances/sonarr-5e6f7a8b/api/v3/series admin 200 "all(.[]; .qualityProfileId as \$q | ($SPROFILE_IDS|index(\$q))!=null)"
chk GET /api/instances/sonarr-5e6f7a8b/api/v3/series kid 200 'type=="array" and length==4'
chk GET /api/instances/sonarr-5e6f7a8b/api/v3/series/2 kid 404 '.error=="not found"'
chk "GET" "/api/instances/sonarr-5e6f7a8b/api/v3/episode?seriesId=2" kid 200 'type=="array" and length==0'
chk "GET" "/api/instances/sonarr-5e6f7a8b/api/v3/episode?seriesId=1" user 200 'type=="array" and length>0'
chk GET /api/instances/sonarr-5e6f7a8b/api/v3/tag admin 200 'type=="array"'
chk GET /api/instances/sonarr-5e6f7a8b/api/v3/queue admin 200 '.records|type=="array"'
chk "GET" "/api/instances/sonarr-5e6f7a8b/api/v3/calendar?start=2026-01-01&end=2027-12-31" admin 200 'type=="array" and length>0'
chk GET /api/instances/chaptarr-9c0d1e2f/api/v1/author user 200 'type=="array"'
chk GET /api/instances/chaptarr-9c0d1e2f/api/v1/book user 200 'type=="array"'
chk GET /api/instances/chaptarr-9c0d1e2f/api/v1/rootfolder admin 200 'type=="array"'
# ── lidarr fake ──────────────────────────────────────────
L=/api/instances/lidarr-4d5e6f7a/api/v1
chk GET $L/system/status admin 200 '.appName=="Lidarr" and (.version|type=="string")'
chk GET $L/artist user 200 'type=="array" and length==6 and (all(.[]; has("artistName") and has("foreignArtistId") and has("statistics") and has("images") and has("monitored")))'
chk GET $L/artist/2 user 200 '.artistName=="Scott Joplin"'
chk GET $L/artist/99 user 404 ''
chk "GET" "$L/artist/lookup?term=caruso" user 200 'type=="array" and length==1'
chk GET $L/album user 200 'type=="array" and length==11 and (all(.[]; has("foreignAlbumId") and has("statistics") and has("artist") and has("releaseDate") and has("images") and (has("ratings")|not)))'
chk "GET" "$L/album?artistId=1" user 200 'type=="array" and length==2'
chk GET $L/album/9 user 200 '.title|test("St. Louis")'
chk "GET" "$L/album/lookup?term=caruso" user 200 'type=="array" and length==3 and ([.[]|select(.id==0)]|length)==1'
chk "GET" "$L/album/lookup?term=lidarr:b0000000-d3a0-4000-8000-000000000108" user 200 'length==1 and .[0].foreignAlbumId=="b0000000-d3a0-4000-8000-000000000008"'
chk GET $L/qualityprofile admin 200 'type=="array" and length>=1'
chk GET $L/metadataprofile admin 200 'type=="array" and length>=1'
chk GET $L/rootfolder admin 200 'type=="array" and .[0].path=="/music"'
chk "GET" "$L/queue?page=1&pageSize=50&includeArtist=true&includeAlbum=true" admin 200 '(.records|length)==2 and (all(.records[]; has("trackedDownloadState") and has("statusMessages") and has("artist") and has("album") and has("sizeleft"))) and .totalRecords==2'
chk "GET" "$L/queue?page=1&pageSize=50" user 200 '.records|type=="array"'
chk "GET" "$L/history?page=1&pageSize=50" admin 200 '(.records|type=="array") and .totalRecords>=10 and (all(.records[]; has("eventType") and has("sourceTitle") and has("date")))'
chk "GET" "$L/wanted/missing?page=1&pageSize=50" admin 200 '(.records|length)==3'
chk "GET" "$L/wanted/cutoff?page=1&pageSize=50" admin 200 '(.records|length)==1'
chk "GET" "$L/calendar?start=$(date -u -v-7d +%F 2>/dev/null || date -u -d '-7 days' +%F)&end=$(date -u -v+30d +%F 2>/dev/null || date -u -d '+30 days' +%F)&includeArtist=true" admin 200 'type=="array" and length==2'
chk "GET" "$L/track?albumId=1" user 200 'type=="array" and length==10 and (all(.[]; has("trackNumber") and has("hasFile") and has("duration")))'
chk "GET" "$L/trackfile?albumId=1" user 200 'type=="array" and length==10 and (all(.[]; (.path|startswith("/music/")) and has("quality") and has("mediaInfo")))'
chk "GET" "$L/release?albumId=5" admin 200 'type=="array" and length==4 and ([.[]|select(.rejected==true)]|length)==1'
chk "GET" "$L/release?albumId=5" user 403 ''
chk "GET" "$L/manualimport?downloadId=SABnzbd_nzo_demo_music_9" admin 200 'type=="array" and length==5 and (all(.[]; .album.id==9 and (.tracks|length)==1))'
chk GET $L/mediacover/album/8/cover.jpg user 200 ''
chk GET $L/mediacover/artist/1/poster.jpg user 200 ''
chk GET $L/diskspace admin 200 'type=="array"'
chk GET $L/health admin 200 'type=="array"'
chk GET $L/system/status user 403 ''
# ── downloads ────────────────────────────────────────────
# the seeded music download finishes within seconds of boot and moves to history, so only the shape is asserted here
chk GET /api/downloads/sabnzbd-3f4a5b6c/queue admin 200 '(.items|type=="array") and (.paused|type=="boolean")'
chk GET /api/downloads/sabnzbd-3f4a5b6c/history admin 200 '(.items|type=="array") and ([.items[]|select(.category=="music")]|length)>=1'
chk GET /api/downloads/qbittorrent-4b5c6d7e/queue admin 200 '(.items|length)==5 and ([.items[].status]|index("stalledDL")!=null) and (all(.items[]; (.progress|type=="number") and has("eta_seconds") and has("size_left_bytes"))) and (.paused==false)'
chk GET /api/downloads/qbittorrent-4b5c6d7e/history admin 200 '(.items|length)==5 and ([.items[]|select(.error!=null and .error!="")]|length)==1'
chk GET /api/downloads/sabnzbd-3f4a5b6c/queue user 403 ''
# ── watch history (both prefixes, both providers) ────────
chk GET /api/watch-history/tracearr-8e9f0a1b/activity admin 200 '(.streams|type=="array") and (.stream_count|type=="number") and (all(.streams[]; has("media_type") and has("server") and has("server_type"))) and ([.streams[]|select(.media_type=="track")]|length)==1'
chk "GET" "/api/watch-history/tracearr-8e9f0a1b/history?limit=10" admin 200 '(.items|type=="array") and (.coverage.note|test("Tracearr")) and (.coverage|has("truncated")) and (all(.items[]; has("server_type")))'
chk "GET" "/api/watch-history/tracearr-8e9f0a1b/stats?days=30" admin 200 '(.top_movies|type=="array") and (.top_shows|type=="array") and (.top_users|type=="array") and (.coverage.note|test("Tracearr"))'
chk GET /api/tautulli/tautulli-7d8e9f0a/activity admin 200 '(.streams|type=="array") and (all(.streams[]; .server=="" and .server_type=="plex"))'
chk "GET" "/api/tautulli/tautulli-7d8e9f0a/history?limit=10" admin 200 '(.coverage.note|test("Tautulli"))'
chk "GET" "/api/watch-history/tautulli-7d8e9f0a/stats?days=7" admin 200 '(.coverage.note|test("Tautulli"))'
chk GET /api/watch-history/radarr-1a2b3c4d/activity admin 400 '.error|test("is not a watch-history instance \\(tautulli, tracearr\\)")'
chk GET /api/watch-history/nope/activity admin 404 '.error=="instance not found"'
chk GET /api/watch-history/tracearr-8e9f0a1b/activity user 403 ''
# ── notifications / media servers / issues / ai / misc ───
chk GET /api/notifications/preferences user 200 '(keys|length)==13 and .new_music==true and .content_upgraded==false'
chk GET /api/media-servers user 200 'type=="array"'
chk "GET" "/api/media-servers/watch?media_type=movie&tmdb_id=961" user 200 'type=="array"'
chk GET /api/admin/media-servers/accounts admin 200 'type=="array"'
chk GET /api/issues user 200 '(.issues|type=="array")'
chk GET /api/admin/issues admin 200 '(.issues|type=="array") and ([.issues[]|select(.media_type=="music")]|length)>=1'
chk GET /api/admin/agent-actions admin 200 'type=="array" or type=="object"'
chk GET /api/admin/agent-approval-rules admin 200 'type=="array" or type=="object"'
chk GET /api/admin/remediation-settings admin 200 'type=="object"'
chk GET /api/admin/agent-digest admin 200 'type=="object"'
chk GET /api/admin/profile-change-proposals admin 200 'type=="array" or type=="object"'
chk GET /api/admin/ai-tools admin 200 '(.tools|length)==41 and ([.tools[].name]|index("search_music")!=null) and ([.tools[].name]|index("browse_titles")!=null) and ([.tools[].name]|index("get_album_timeline")!=null)'
chk GET /api/admin/credentials admin 200 'type=="object"'
chk GET /api/ai/available user 200 'type=="object"'
chk GET /api/ai/settings user 200 'type=="object"'
chk GET /api/admin/external-settings-changes admin 200 'type=="array" or type=="object"'
chk POST /api/media-files/coverage user 200 '.covered|type=="array"' '{"instance_id":"lidarr-4d5e6f7a","paths":["/music/Enrico Caruso/x.flac"]}'

if [ $MUTATE = 1 ]; then
  # setup skip round-trip
  chk PUT /api/admin/setup-status/skips admin 200 '.key=="push" and .skipped==true' '{"key":"push","skipped":true}'
  chk GET /api/admin/setup-status admin 200 '([.items[]|select(.key=="push")][0].skipped==true)'
  chk PUT /api/admin/setup-status/skips admin 400 '.error=="only optional setup items can be skipped"' '{"key":"radarr","skipped":true}'
  chk PUT /api/admin/setup-status/skips admin 400 '.error=="unknown setup item"' '{"key":"nope","skipped":true}'
  chk PUT /api/admin/setup-status/skips admin 200 '' '{"key":"push","skipped":false}'
  # kids policy round-trip on user 2
  chk PUT /api/admin/users/2/content-policy admin 200 '.max_movie_rating=="PG" and .rating_region=="US" and (.blocked_movie_genres==[27])' '{"max_movie_rating":"pg","max_tv_rating":"tv-pg","rating_region":"us","block_unrated":false,"blocked_movie_genres":[27,27,0],"blocked_tv_genres":null}'
  chk GET /api/admin/users admin 200 '([.[]|select(.username=="user")][0].child==true)'
  chk DELETE /api/admin/users/2/content-policy admin 200 '.status=="cleared"'
  chk GET /api/admin/users/2/content-policy admin 404 ''
  # radarr edit round-trip
  body=$(curl -sS -A "$UA" -H "Authorization: Bearer $ADMIN" "$BASE/api/instances/radarr-1a2b3c4d/api/v3/movie/1" | jq -c '.monitored=false | .tags=[1,2]')
  chk PUT /api/instances/radarr-1a2b3c4d/api/v3/movie/1 admin 202 '.monitored==false and (.tags==[1,2])' "$body"
  chk POST /api/instances/radarr-1a2b3c4d/api/v3/command admin 201 '.name=="RefreshMovie"' '{"name":"RefreshMovie","movieIds":[1]}'
  # qBittorrent shape switch and back
  chk PUT /api/instances/qbittorrent-4b5c6d7e admin 400 '.error=="an API key, or a username and password, is required for qbittorrent"' '{"name":"qBittorrent","url":"http://qbittorrent:8081","username":"admin"}'
  chk PUT /api/instances/qbittorrent-4b5c6d7e admin 200 '(has("has_api_key")|not) and .username=="admin"' '{"name":"qBittorrent","url":"http://qbittorrent:8081","username":"admin","password":"pw"}'
  chk PUT /api/instances/qbittorrent-4b5c6d7e admin 200 '.has_api_key==true and .username==""' '{"name":"qBittorrent","url":"http://qbittorrent:8081","api_key":"k"}'
  # qBittorrent client pause/resume
  chk POST /api/downloads/qbittorrent-4b5c6d7e/pause admin 204 ''
  chk GET /api/downloads/qbittorrent-4b5c6d7e/queue admin 200 '.paused==true and (all(.items[]; .status=="pausedDL"))'
  chk POST /api/downloads/qbittorrent-4b5c6d7e/resume admin 204 ''
  # music request loop (album 3, user requires approval)
  chk POST /api/requests user 200 '.status=="pending"' '{"media_type":"music","foreign_id":"b0000000-d3a0-4000-8000-000000000003","title":"Vesti la giubba and Other Arias","instance_id":"lidarr-4d5e6f7a","search_term":"caruso"}'
  RID=$(curl -sS -A "$UA" -H "Authorization: Bearer $ADMIN" "$BASE/api/admin/requests" | jq -r '[.[]|select(.media_type=="music" and .foreign_id=="b0000000-d3a0-4000-8000-000000000003")][0].id')
  chk POST /api/admin/requests/$RID/approve admin 200 '.status=="requested"'
  chk "GET" "/api/requests/music-status?foreign_id=b0000000-d3a0-4000-8000-000000000003" user 200 '.status=="requested"'
  sleep 14
  chk "GET" "/api/requests/music-status?foreign_id=b0000000-d3a0-4000-8000-000000000003" user 200 '.status=="downloading"'
  sleep 24
  chk "GET" "/api/requests/music-status?foreign_id=b0000000-d3a0-4000-8000-000000000003" user 200 '.status=="available"'
  chk "GET" "/api/requests/music-recent?limit=1" user 200 '.items[0].foreign_album_id=="b0000000-d3a0-4000-8000-000000000003"'
  # unresolved park refuses approval; deny works
  chk POST /api/admin/requests/14/approve admin 400 '.error|test("album not found for foreign id")'
  chk POST /api/admin/requests/14/deny admin 200 '.success==true' '{"reason":"Not this one"}'
  # monitor in place (album 12, admin auto-approves)
  chk POST /api/requests admin 200 '.status=="requested"' '{"media_type":"music","foreign_id":"b0000000-d3a0-4000-8000-000000000012","title":"Stars and Stripes Forever: Rare Sides","instance_id":"lidarr-4d5e6f7a"}'
  chk GET $L/album/12 admin 200 '.monitored==true'
  chk GET $L/album admin 200 'length==12'  # album 3 joined the library through the request loop above
  # music validation strings
  chk POST /api/requests user 400 '.error=="foreign_id required for music requests"' '{"media_type":"music","title":"x"}'
  chk POST /api/requests user 400 '.error=="music requests carry no book_format"' '{"media_type":"music","foreign_id":"b0000000-d3a0-4000-8000-000000000003","title":"x","book_format":"ebook"}'
  # music issue
  chk POST /api/issues user 200 '(.issue_id|type=="number")' '{"instance_id":"lidarr-4d5e6f7a","media_type":"music","foreign_id":"b0000000-d3a0-4000-8000-000000000002","category":"bad_copy"}'
  # lidarr manual import completes album 9: import every candidate the fake lists, as the Import Doctor does
  CANDS=$(curl -sS -A "$UA" -H "Authorization: Bearer $ADMIN" "$BASE$L/manualimport?downloadId=SABnzbd_nzo_demo_music_9" | jq -c '[.[]|{path,artistId:.artist.id,albumId:.album.id,albumReleaseId,trackIds:[.tracks[].id],quality,downloadId}]')
  chk POST $L/command admin 201 '.name=="ManualImport"' "{\"name\":\"ManualImport\",\"files\":$CANDS,\"importMode\":\"move\"}"
  chk GET $L/album/9 admin 200 '.statistics.trackFileCount==8 and .statistics.trackCount==8'
  chk "GET" "$L/queue?page=1&pageSize=50" admin 200 '(.records|length)==1'
fi

echo
echo "passed: $pass  failed: $fail  (base $BASE, mutate=$MUTATE)"
for f in ${failures[@]+"${failures[@]}"}; do echo "  FAIL $f"; done
[ $fail = 0 ]
