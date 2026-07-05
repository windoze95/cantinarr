// Dev-only demo payloads for the store-screenshot harness
// (test/preview/screenshot_main.dart). NEVER shipped or imported from lib/.
//
// Every builder here returns plain Dart Maps/Lists that JSON-encode into the
// exact shape the app's hand-rolled `fromJson` parsers expect, so the real
// screens render fully populated against a stubbed backend. Contracts were
// traced from:
//   - discover/data/tmdb_models.dart          (TmdbPage / MediaItem / *Detail)
//   - radarr|sonarr/data/*_models.dart         (verbatim v3 proxy shapes)
//   - downloads/data/downloads_models.dart     (normalized queue)
//   - request/data/request_service.dart        (status / options / seasons)
//   - settings/data/request_settings_service.dart (approval queue)
//
// Poster/backdrop paths are real TMDB CDN paths (July 2026); the app loads
// images from image.tmdb.org directly, so they resolve in the browser.
library;

// ─── Image helpers ──────────────────────────────────────────────────────────

const String _tmdbW500 = 'https://image.tmdb.org/t/p/w500';

/// A full TMDB CDN url, for Radarr/Sonarr `images[].remoteUrl` (the app uses
/// it verbatim because it starts with http).
String _remote(String posterPath) => '$_tmdbW500$posterPath';

List<Map<String, dynamic>> _posterImage(String posterPath) => [
      {'coverType': 'poster', 'remoteUrl': _remote(posterPath)},
    ];

// ─── Demo catalogue (real TMDB ids + art) ───────────────────────────────────

class _Movie {
  final int tmdbId;
  final String title;
  final String poster;
  final String? backdrop;
  final double vote;
  final String releaseDate;
  final String overview;
  const _Movie(
    this.tmdbId,
    this.title,
    this.poster, {
    this.backdrop,
    this.vote = 7.0,
    this.releaseDate = '2026-01-01',
    this.overview = '',
  });
}

class _Tv {
  final int tmdbId;
  final String title;
  final String poster;
  final String? backdrop;
  final double vote;
  final String firstAirDate;
  final String overview;
  const _Tv(
    this.tmdbId,
    this.title,
    this.poster, {
    this.backdrop,
    this.vote = 7.5,
    this.firstAirDate = '2020-01-01',
    this.overview = '',
  });
}

const _phm = _Movie(
  687163,
  'Project Hail Mary',
  '/yihdXomYb5kTeSivtFndMy5iDmf.jpg',
  // Textless backdrop (the primary TMDB backdrop is a Prime Video promo card).
  backdrop: '/8Tfys3mDZVp4tNoH2ktm06a0Tau.jpg',
  vote: 7.9,
  releaseDate: '2026-03-20',
  overview:
      'Science teacher Ryland Grace wakes up on a spaceship light years from '
      'home with no recollection of who he is or how he got there. As his '
      'memory returns, he begins to uncover his mission: solve the riddle of '
      'the mysterious substance causing the sun to die out.',
);

const _movies = <_Movie>[
  _phm,
  _Movie(1084244, 'Toy Story 5', '/sfQtVlIHljToOwYjhe21KPGzZWK.jpg',
      vote: 7.2, releaseDate: '2026-06-19'),
  _Movie(1314481, 'The Devil Wears Prada 2', '/fCAURTUx3YfsJ8k9I0UamjSILiR.jpg',
      vote: 7.6, releaseDate: '2026-07-17'),
  _Movie(931285, 'Mortal Kombat II', '/hwRdDFIhaEmpRgoki805YvyyjZf.jpg',
      vote: 6.9, releaseDate: '2026-05-15'),
  _Movie(1081003, 'Supergirl', '/niSvU02l2BONH9ivubV6K1a5QiK.jpg',
      vote: 7.1, releaseDate: '2026-06-26'),
  _Movie(1202033, 'Enola Holmes 3', '/7kRYHH9H9PjBFwz1FprbHB2AAjI.jpg',
      vote: 7.0, releaseDate: '2026-05-01'),
  _Movie(1226863, 'The Super Mario Galaxy Movie',
      '/eJGWx219ZcEMVQJhAgMiqo8tYY.jpg',
      vote: 7.8, releaseDate: '2026-04-03'),
  _Movie(278, 'The Shawshank Redemption', '/9cqNxx0GxF0bflZmeSMuL5tnGzr.jpg',
      vote: 8.7, releaseDate: '1994-09-23'),
  _Movie(1339713, 'Obsession', '/bRwnj8WEKBCvmfeUNOukJPwB43K.jpg',
      vote: 6.4, releaseDate: '2026-08-14'),
  _Movie(1273221, 'Scary Movie', '/1KlYdWoOrbL5ux357rW9LC155qw.jpg',
      vote: 6.2, releaseDate: '2026-10-30'),
  _Movie(1083381, 'Backrooms', '/rhGx6E3qRNMgj3i5su2oukNHwIQ.jpg',
      vote: 6.8, releaseDate: '2026-09-11'),
  _Movie(936075, 'Michael', '/zm0KAbOjlt9eR5y7vDiL2dEOwMl.jpg',
      vote: 7.5, releaseDate: '2026-08-07'),
  _Movie(1169516, 'Welcome to the Jungle', '/1JlfUuvvX5xLP2LIDah4JhWUtTx.jpg',
      vote: 6.6, releaseDate: '2026-02-13'),
  _Movie(1127384, 'Deep Water', '/kjcuS7xaRyqRjVaVcH4t0qHshuX.jpg',
      vote: 6.7, releaseDate: '2026-07-31'),
];

const _hotd = _Tv(
  94997,
  'House of the Dragon',
  '/7V0Ebks0GgpKvQ7QbLAIdX5dos4.jpg',
  // Textless backdrop (the primary TMDB backdrop is an HBO/Prime promo card).
  backdrop: '/etj8E2o0Bud0HkONVQPjyCkIvpv.jpg',
  vote: 8.4,
  firstAirDate: '2022-08-21',
  overview:
      'The Targaryen dynasty is at the absolute apex of its power, with more '
      'than 15 dragons under their yoke. Yet seeds of division sow friction '
      'across the realm.',
);

const _tv = <_Tv>[
  _hotd,
  _Tv(124364, 'FROM', '/pRtJagIxpfODzzb0T0NAvZSzErC.jpg',
      vote: 8.0, firstAirDate: '2022-02-20'),
  _Tv(82452, 'Avatar: The Last Airbender', '/lzZpWEaqzP0qVA5nkCc5ASbNcSy.jpg',
      vote: 8.3, firstAirDate: '2024-02-22'),
  _Tv(79744, 'The Rookie', '/70kTz0OmjjZe7zHvIDrq2iKW7PJ.jpg',
      vote: 8.5, firstAirDate: '2018-10-16'),
  _Tv(76479, 'The Boys', '/in1R2dDc421JxsoRWaIIAqVI2KE.jpg',
      vote: 8.4, firstAirDate: '2019-07-25'),
  _Tv(1399, 'Game of Thrones', '/1XS1oqL89opfnbLl8WnZY1O1uJx.jpg',
      vote: 8.5, firstAirDate: '2011-04-17'),
  _Tv(456, 'The Simpsons', '/uWpG7GqfKGQqX4YMAo3nv5OrglV.jpg',
      vote: 8.0, firstAirDate: '1989-12-17'),
  _Tv(1416, "Grey's Anatomy", '/hjJkrLXhWvGHpLeLBDFznpBTY1S.jpg',
      vote: 8.2, firstAirDate: '2005-03-27'),
  _Tv(1622, 'Supernatural', '/8iixmfGx5EIFPdpNvB2JvI3VIqX.jpg',
      vote: 8.3, firstAirDate: '2005-09-13'),
  _Tv(4614, 'NCIS', '/mBcu8d6x6zB1el3MPNl7cZQEQ31.jpg',
      vote: 7.7, firstAirDate: '2003-09-23'),
  _Tv(299167, 'Dutton Ranch', '/xsiecCxd8lkcAluw0wWwbW5CwSv.jpg',
      vote: 8.1, firstAirDate: '2026-07-30'),
  _Tv(4057, 'Criminal Minds', '/hWSb4UnIjlTvnvrP98NbFSO60HA.jpg',
      vote: 8.1, firstAirDate: '2005-09-22'),
];

_Movie _movieById(int id) =>
    _movies.firstWhere((m) => m.tmdbId == id, orElse: () => _phm);
_Tv _tvById(int id) => _tv.firstWhere((t) => t.tmdbId == id, orElse: () => _hotd);

// ─── TMDB media-item / page builders (discover, search, recommendations) ─────

Map<String, dynamic> _movieItem(_Movie m, {bool withMediaType = false}) => {
      'id': m.tmdbId,
      'title': m.title,
      'poster_path': m.poster,
      if (m.backdrop != null) 'backdrop_path': m.backdrop,
      'vote_average': m.vote,
      'release_date': m.releaseDate,
      'overview': m.overview,
      if (withMediaType) 'media_type': 'movie',
    };

Map<String, dynamic> _tvItem(_Tv t, {bool withMediaType = false}) => {
      'id': t.tmdbId,
      'name': t.title,
      'poster_path': t.poster,
      if (t.backdrop != null) 'backdrop_path': t.backdrop,
      'vote_average': t.vote,
      'first_air_date': t.firstAirDate,
      'overview': t.overview,
      if (withMediaType) 'media_type': 'tv',
    };

Map<String, dynamic> _page(List<Map<String, dynamic>> results) => {
      'page': 1,
      'total_pages': 1,
      'total_results': results.length,
      'results': results,
    };

/// Popular movies row.
Map<String, dynamic> _popularMovies() => _page([
      for (final m in [
        _movies[1], // Toy Story 5
        _movies[6], // Super Mario Galaxy
        _movies[4], // Supergirl
        _movies[3], // Mortal Kombat II
        _movies[5], // Enola Holmes 3
        _movies[12], // Deep Water
        _movies[11], // Welcome to the Jungle
        _movies[10], // Michael
        _movies[8], // Obsession
        _movies[9], // Scary Movie
        _movies[7], // Backrooms
        _phm,
      ])
        _movieItem(m),
    ]);

Map<String, dynamic> _topRatedMovies() => _page([
      _movieItem(_movies[7]), // Shawshank
      _movieItem(_phm),
      _movieItem(_movies[6]), // Super Mario Galaxy
      _movieItem(_movies[11]), // Michael
      _movieItem(_movies[2]), // Devil Wears Prada 2
      _movieItem(_movies[1]), // Toy Story 5
    ]);

Map<String, dynamic> _upcomingMovies() => _page([
      _movieItem(_movies[2]), // Devil Wears Prada 2 (Jul)
      _movieItem(_movies[11]), // Michael (Aug)
      _movieItem(_movies[8]), // Obsession (Aug)
      _movieItem(_movies[10]), // Backrooms (Sep)
      _movieItem(_movies[9]), // Scary Movie (Oct)
      _movieItem(_movies[13]), // Deep Water (Jul)
    ]);

Map<String, dynamic> _popularTv() => _page([for (final t in _tv) _tvItem(t)]);

/// Trakt "anticipated" rows. The service maps each entry through
/// TraktItem.fromAnticipatedJson: {movie|show: {ids:{tmdb}, title, year,
/// images:{poster:[url]}}}. A scheme-less url is prefixed with https:// by the
/// parser, so bare image.tmdb.org paths render.
List<Map<String, dynamic>> _traktAnticipated(String type) {
  Map<String, dynamic> entry(int tmdb, String title, int year, String poster) {
    final inner = {
      'ids': {'tmdb': tmdb, 'trakt': tmdb},
      'title': title,
      'year': year,
      'images': {
        'poster': ['image.tmdb.org/t/p/w500$poster'],
      },
    };
    return {type == 'movies' ? 'movie' : 'show': inner};
  }

  if (type == 'movies') {
    return [
      entry(1314481, 'The Devil Wears Prada 2', 2026,
          '/fCAURTUx3YfsJ8k9I0UamjSILiR.jpg'),
      entry(1081003, 'Supergirl', 2026, '/niSvU02l2BONH9ivubV6K1a5QiK.jpg'),
      entry(936075, 'Michael', 2026, '/zm0KAbOjlt9eR5y7vDiL2dEOwMl.jpg'),
      entry(1202033, 'Enola Holmes 3', 2026, '/7kRYHH9H9PjBFwz1FprbHB2AAjI.jpg'),
    ];
  }
  return [
    entry(299167, 'Dutton Ranch', 2026, '/xsiecCxd8lkcAluw0wWwbW5CwSv.jpg'),
    entry(94997, 'House of the Dragon', 2022,
        '/7V0Ebks0GgpKvQ7QbLAIdX5dos4.jpg'),
    entry(124364, 'FROM', 2022, '/pRtJagIxpfODzzb0T0NAvZSzErC.jpg'),
  ];
}

// ─── Movie / TV detail (hero pages) ─────────────────────────────────────────

Map<String, dynamic> _movieDetail(int id) {
  final m = _movieById(id);
  return {
    'id': m.tmdbId,
    'title': m.title,
    'tagline': m.tmdbId == _phm.tmdbId ? 'Save the sun. Save the world.' : '',
    'overview': m.overview.isNotEmpty
        ? m.overview
        : 'A ${m.title} story from the demo catalogue.',
    'poster_path': m.poster,
    'backdrop_path': m.backdrop ?? m.poster,
    'vote_average': m.vote,
    'runtime': m.tmdbId == _phm.tmdbId ? 157 : 118,
    'release_date': m.releaseDate,
    'status': 'Released',
    'genres': m.tmdbId == _phm.tmdbId
        ? [
            {'id': 878, 'name': 'Science Fiction'},
            {'id': 12, 'name': 'Adventure'},
          ]
        : [
            {'id': 18, 'name': 'Drama'},
          ],
    'videos': {'results': const []},
    'budget': 0,
    'revenue': 0,
  };
}

/// House of the Dragon (and any other tv) detail with a real seasons list.
Map<String, dynamic> _tvDetail(int id) {
  final t = _tvById(id);
  final isHotd = t.tmdbId == _hotd.tmdbId;
  final seasons = isHotd
      ? [
          _seasonMeta(1, 'Season 1', 10, '2022-08-21',
              '/z2yahl2uefxDCl0nogcRBstwruJ.jpg'),
          _seasonMeta(2, 'Season 2', 8, '2024-06-16',
              '/7QMsOTMUswlwxJP0rTTZfmz2tX2.jpg'),
          _seasonMeta(3, 'Season 3', 8, '2026-06-28',
              '/7V0Ebks0GgpKvQ7QbLAIdX5dos4.jpg'),
        ]
      : [
          _seasonMeta(1, 'Season 1', 10, t.firstAirDate, t.poster),
        ];
  return {
    'id': t.tmdbId,
    'name': t.title,
    'tagline': isHotd ? 'Fire will reign.' : '',
    'overview': t.overview.isNotEmpty
        ? t.overview
        : 'A ${t.title} story from the demo catalogue.',
    'poster_path': t.poster,
    'backdrop_path': t.backdrop ?? t.poster,
    'vote_average': t.vote,
    'first_air_date': t.firstAirDate,
    'status': 'Returning Series',
    'number_of_seasons': seasons.length,
    'number_of_episodes': isHotd ? 26 : 10,
    'genres': isHotd
        ? [
            {'id': 10765, 'name': 'Sci-Fi & Fantasy'},
            {'id': 18, 'name': 'Drama'},
          ]
        : [
            {'id': 18, 'name': 'Drama'},
          ],
    'videos': {'results': const []},
    'seasons': seasons,
    'external_ids': {'tvdb_id': isHotd ? 371572 : 0, 'imdb_id': ''},
  };
}

Map<String, dynamic> _seasonMeta(
        int number, String name, int episodeCount, String airDate, String poster) =>
    {
      'id': 90000 + number,
      'season_number': number,
      'name': name,
      'poster_path': poster,
      'episode_count': episodeCount,
      'air_date': airDate,
    };

/// A small recommendations/similar page keyed to the media type.
Map<String, dynamic> _relatedMovies() => _page([
      _movieItem(_movies[6]),
      _movieItem(_movies[1]),
      _movieItem(_movies[4]),
      _movieItem(_movies[10]),
      _movieItem(_movies[5]),
    ]);

Map<String, dynamic> _relatedTv() => _page([
      _tvItem(_tv[5]), // Game of Thrones
      _tvItem(_tv[4]), // The Boys
      _tvItem(_tv[1]), // FROM
      _tvItem(_tv[2]), // Avatar
    ]);

// ─── Request status / options (drives the requester-facing surface) ──────────

Map<String, dynamic> _requestStatus(int tmdbId) {
  if (tmdbId == _phm.tmdbId) {
    // Movie: fully available -> "Watch Now".
    return {'status': 'available', 'seasons': const []};
  }
  if (tmdbId == _hotd.tmdbId) {
    // TV: mixed per-season availability -> overall "partial" ("Request More").
    return {
      'status': 'partial',
      'seasons': [
        {
          'season_number': 1,
          'episode_file_count': 10,
          'episode_count': 10,
          'status': 'available',
          'progress': 1.0,
        },
        {
          'season_number': 2,
          'episode_file_count': 8,
          'episode_count': 8,
          'status': 'available',
          'progress': 1.0,
        },
        {
          'season_number': 3,
          'episode_file_count': 3,
          'episode_count': 8,
          'status': 'downloading',
          'progress': 0.375,
        },
      ],
    };
  }
  return {'status': 'unavailable', 'seasons': const []};
}

Map<String, dynamic> _requestOptions() => {
      'can_choose_season': true,
      'can_choose_quality': false,
      'default_season_scope': 'all',
      'quality_profiles': const [],
    };

// ─── Radarr (verbatim v3 proxy): library, queue, calendar ────────────────────

Map<String, dynamic> _radarrMovie({
  required int id,
  required int tmdbId,
  required String title,
  required String poster,
  required int year,
  required bool hasFile,
  bool monitored = true,
  String? added,
  String? digitalRelease,
  String? inCinemas,
  String? physicalRelease,
  int sizeOnDisk = 0,
  String quality = 'Bluray-1080p',
}) =>
    {
      'id': id,
      'title': title,
      'year': year,
      'tmdbId': tmdbId,
      'monitored': monitored,
      'hasFile': hasFile,
      'isAvailable': true,
      'minimumAvailability': 'released',
      'runtime': 120,
      'status': 'released',
      'qualityProfileId': 1,
      'sizeOnDisk': sizeOnDisk,
      'images': _posterImage(poster),
      if (added != null) 'added': added,
      if (digitalRelease != null) 'digitalRelease': digitalRelease,
      if (inCinemas != null) 'inCinemas': inCinemas,
      if (physicalRelease != null) 'physicalRelease': physicalRelease,
      if (hasFile)
        'movieFile': {
          'id': id + 5000,
          'relativePath': '$title ($year) $quality.mkv',
          'size': sizeOnDisk,
          'quality': {
            'quality': {'name': quality},
          },
        },
    };

const _gb = 1024 * 1024 * 1024;

/// Radarr library (GET /movie). Backs the shell search-chip snapshot, the
/// Movies-tab library rows and the Radarr library screen.
List<Map<String, dynamic>> _radarrLibrary() => [
      _radarrMovie(
          id: 201,
          tmdbId: 687163,
          title: 'Project Hail Mary',
          poster: _phm.poster,
          year: 2026,
          hasFile: true,
          added: '2026-07-01T09:12:00Z',
          sizeOnDisk: (14.6 * _gb).round(),
          quality: 'Bluray-2160p'),
      _radarrMovie(
          id: 202,
          tmdbId: 1084244,
          title: 'Toy Story 5',
          poster: '/sfQtVlIHljToOwYjhe21KPGzZWK.jpg',
          year: 2026,
          hasFile: true,
          added: '2026-06-27T20:40:00Z',
          sizeOnDisk: (8.1 * _gb).round(),
          quality: 'WEBDL-1080p'),
      _radarrMovie(
          id: 203,
          tmdbId: 1226863,
          title: 'The Super Mario Galaxy Movie',
          poster: '/eJGWx219ZcEMVQJhAgMiqo8tYY.jpg',
          year: 2026,
          hasFile: true,
          added: '2026-06-20T18:05:00Z',
          sizeOnDisk: (9.7 * _gb).round(),
          quality: 'Bluray-1080p'),
      _radarrMovie(
          id: 204,
          tmdbId: 278,
          title: 'The Shawshank Redemption',
          poster: '/9cqNxx0GxF0bflZmeSMuL5tnGzr.jpg',
          year: 1994,
          hasFile: true,
          added: '2026-05-10T12:00:00Z',
          sizeOnDisk: (18.3 * _gb).round(),
          quality: 'Bluray-2160p'),
      _radarrMovie(
          id: 205,
          tmdbId: 1314481,
          title: 'The Devil Wears Prada 2',
          poster: '/fCAURTUx3YfsJ8k9I0UamjSILiR.jpg',
          year: 2026,
          hasFile: false,
          added: '2026-07-02T08:00:00Z'),
      _radarrMovie(
          id: 206,
          tmdbId: 931285,
          title: 'Mortal Kombat II',
          poster: '/hwRdDFIhaEmpRgoki805YvyyjZf.jpg',
          year: 2026,
          hasFile: false,
          added: '2026-07-03T08:00:00Z'),
      _radarrMovie(
          id: 207,
          tmdbId: 1081003,
          title: 'Supergirl',
          poster: '/niSvU02l2BONH9ivubV6K1a5QiK.jpg',
          year: 2026,
          hasFile: false,
          added: '2026-06-30T08:00:00Z'),
      _radarrMovie(
          id: 208,
          tmdbId: 1202033,
          title: 'Enola Holmes 3',
          poster: '/7kRYHH9H9PjBFwz1FprbHB2AAjI.jpg',
          year: 2026,
          hasFile: false,
          added: '2026-06-29T08:00:00Z'),
    ];

/// Radarr queue (GET /queue -> {records:[...]}). The Movies tab reads
/// records[].movieId to flag "Downloading"; the Radarr queue screen parses
/// each record into RadarrQueueItem.
Map<String, dynamic> _radarrQueue() => {
      'page': 1,
      'pageSize': 100,
      'totalRecords': 2,
      'records': [
        _arrQueueRecord(
          id: 9001,
          movieId: 206,
          title: 'Mortal Kombat II 2026 2160p WEB-DL DDP5.1 Atmos DV HDR-FLUX',
          quality: 'WEBDL-2160p',
          size: 21.4 * _gb,
          sizeleft: 6.2 * _gb,
          timeleft: '00:12:44',
          downloadClient: 'qBittorrent',
          protocol: 'torrent',
          indexer: 'TorrentLeech',
          extraKey: 'movie',
          extraTitle: 'Mortal Kombat II',
        ),
        _arrQueueRecord(
          id: 9002,
          movieId: 208,
          title: 'Enola.Holmes.3.2026.1080p.NF.WEB-DL.DDP5.1.H.264-NTb',
          quality: 'WEBDL-1080p',
          size: 6.8 * _gb,
          sizeleft: 2.1 * _gb,
          timeleft: '00:05:03',
          downloadClient: 'SABnzbd',
          protocol: 'usenet',
          indexer: 'NZBgeek (Prowlarr)',
          extraKey: 'movie',
          extraTitle: 'Enola Holmes 3',
        ),
      ],
    };

/// A Radarr/Sonarr queue record. [extraKey] is 'movie' or 'series' (the nested
/// object each parser reads for the display title).
Map<String, dynamic> _arrQueueRecord({
  required int id,
  int? movieId,
  int? seriesId,
  required String title,
  required String quality,
  required double size,
  required double sizeleft,
  required String timeleft,
  required String downloadClient,
  required String protocol,
  required String indexer,
  required String extraKey,
  required String extraTitle,
  Map<String, dynamic>? episode,
}) =>
    {
      'id': id,
      if (movieId != null) 'movieId': movieId,
      if (seriesId != null) 'seriesId': seriesId,
      'title': title,
      'status': 'downloading',
      'trackedDownloadState': 'downloading',
      'trackedDownloadStatus': 'ok',
      'protocol': protocol,
      'indexer': indexer,
      'downloadClient': downloadClient,
      'size': size,
      'sizeleft': sizeleft,
      'timeleft': timeleft,
      'quality': {
        'quality': {'name': quality},
      },
      extraKey: {'title': extraTitle},
      if (episode != null) 'episode': episode,
      'statusMessages': const [],
    };

/// Radarr calendar (GET /calendar) -> RadarrMovie-shaped entries with release
/// dates. Feeds the Releases-tab movie events.
List<Map<String, dynamic>> _radarrCalendar() => [
      _radarrMovie(
          id: 205,
          tmdbId: 1314481,
          title: 'The Devil Wears Prada 2',
          poster: '/fCAURTUx3YfsJ8k9I0UamjSILiR.jpg',
          year: 2026,
          hasFile: false,
          inCinemas: '2026-07-17T00:00:00Z',
          digitalRelease: '2026-08-21T00:00:00Z'),
      _radarrMovie(
          id: 207,
          tmdbId: 1081003,
          title: 'Supergirl',
          poster: '/niSvU02l2BONH9ivubV6K1a5QiK.jpg',
          year: 2026,
          hasFile: false,
          digitalRelease: '2026-07-24T00:00:00Z'),
      _radarrMovie(
          id: 210,
          tmdbId: 936075,
          title: 'Michael',
          poster: '/zm0KAbOjlt9eR5y7vDiL2dEOwMl.jpg',
          year: 2026,
          hasFile: false,
          digitalRelease: '2026-08-07T00:00:00Z'),
      _radarrMovie(
          id: 211,
          tmdbId: 1339713,
          title: 'Obsession',
          poster: '/bRwnj8WEKBCvmfeUNOukJPwB43K.jpg',
          year: 2026,
          hasFile: false,
          digitalRelease: '2026-08-14T00:00:00Z'),
      _radarrMovie(
          id: 201,
          tmdbId: 687163,
          title: 'Project Hail Mary',
          poster: _phm.poster,
          year: 2026,
          hasFile: true,
          digitalRelease: '2026-07-10T00:00:00Z'),
      _radarrMovie(
          id: 212,
          tmdbId: 1127384,
          title: 'Deep Water',
          poster: '/kjcuS7xaRyqRjVaVcH4t0qHshuX.jpg',
          year: 2026,
          hasFile: false,
          digitalRelease: '2026-07-31T00:00:00Z'),
    ];

// ─── Sonarr (verbatim v3 proxy): library, calendar ───────────────────────────

Map<String, dynamic> _sonarrSeries({
  required int id,
  required int tmdbId,
  required String title,
  required String poster,
  required int year,
  required String status,
  required int files,
  required int total,
  bool monitored = true,
  int seasons = 1,
}) {
  final stats = {
    'seasonCount': seasons,
    'episodeFileCount': files,
    'episodeCount': total,
    'totalEpisodeCount': total,
    'sizeOnDisk': files * 1400 * 1024 * 1024,
    'percentOfEpisodes': total == 0 ? 0 : (files / total) * 100,
  };
  return {
    'id': id,
    'title': title,
    'tmdbId': tmdbId,
    'year': year,
    'monitored': monitored,
    'status': status,
    'seriesType': 'standard',
    'qualityProfileId': 1,
    'images': _posterImage(poster),
    'statistics': stats,
    'seasons': [
      {
        'seasonNumber': 1,
        'monitored': monitored,
        'statistics': stats,
      },
    ],
  };
}

/// Sonarr library (GET /series). Backs the shell search-chip snapshot, the
/// TV-tab library rows, the Releases-tab series join and the Sonarr library
/// screen (airing badge + completeness bar).
List<Map<String, dynamic>> _sonarrLibrary() => [
      _sonarrSeriesHotd(),
      _sonarrSeries(
          id: 102,
          tmdbId: 76479,
          title: 'The Boys',
          poster: '/in1R2dDc421JxsoRWaIIAqVI2KE.jpg',
          year: 2019,
          status: 'continuing',
          files: 32,
          total: 40,
          seasons: 5),
      _sonarrSeries(
          id: 103,
          tmdbId: 124364,
          title: 'FROM',
          poster: '/pRtJagIxpfODzzb0T0NAvZSzErC.jpg',
          year: 2022,
          status: 'continuing',
          files: 30,
          total: 30,
          seasons: 3),
      _sonarrSeries(
          id: 104,
          tmdbId: 1399,
          title: 'Game of Thrones',
          poster: '/1XS1oqL89opfnbLl8WnZY1O1uJx.jpg',
          year: 2011,
          status: 'ended',
          files: 73,
          total: 73,
          seasons: 8),
      _sonarrSeries(
          id: 105,
          tmdbId: 82452,
          title: 'Avatar: The Last Airbender',
          poster: '/lzZpWEaqzP0qVA5nkCc5ASbNcSy.jpg',
          year: 2024,
          status: 'continuing',
          files: 8,
          total: 8,
          seasons: 1),
      _sonarrSeries(
          id: 106,
          tmdbId: 1416,
          title: "Grey's Anatomy",
          poster: '/hjJkrLXhWvGHpLeLBDFznpBTY1S.jpg',
          year: 2005,
          status: 'continuing',
          files: 400,
          total: 440,
          seasons: 21),
      _sonarrSeries(
          id: 107,
          tmdbId: 1622,
          title: 'Supernatural',
          poster: '/8iixmfGx5EIFPdpNvB2JvI3VIqX.jpg',
          year: 2005,
          status: 'ended',
          files: 327,
          total: 327,
          seasons: 15),
      _sonarrSeries(
          id: 108,
          tmdbId: 456,
          title: 'The Simpsons',
          poster: '/uWpG7GqfKGQqX4YMAo3nv5OrglV.jpg',
          year: 1989,
          status: 'continuing',
          files: 704,
          total: 750,
          seasons: 36),
      _sonarrSeries(
          id: 109,
          tmdbId: 4614,
          title: 'NCIS',
          poster: '/mBcu8d6x6zB1el3MPNl7cZQEQ31.jpg',
          year: 2003,
          status: 'continuing',
          files: 402,
          total: 450,
          monitored: false,
          seasons: 22),
      _sonarrSeries(
          id: 111,
          tmdbId: 79744,
          title: 'The Rookie',
          poster: '/70kTz0OmjjZe7zHvIDrq2iKW7PJ.jpg',
          year: 2018,
          status: 'continuing',
          files: 100,
          total: 120,
          seasons: 7),
      _sonarrSeries(
          id: 112,
          tmdbId: 4057,
          title: 'Criminal Minds',
          poster: '/hWSb4UnIjlTvnvrP98NbFSO60HA.jpg',
          year: 2005,
          status: 'ended',
          files: 300,
          total: 300,
          seasons: 16),
      _sonarrSeries(
          id: 110,
          tmdbId: 299167,
          title: 'Dutton Ranch',
          poster: '/xsiecCxd8lkcAluw0wWwbW5CwSv.jpg',
          year: 2026,
          status: 'upcoming',
          files: 0,
          total: 8,
          seasons: 1),
    ];

/// House of the Dragon with real per-season statistics: S1 10/10, S2 8/8,
/// S3 3/8 (airing) -> mixed availability the detail + library both read.
Map<String, dynamic> _sonarrSeriesHotd() {
  Map<String, dynamic> seasonStat(int files, int total) => {
        'seasonCount': 1,
        'episodeFileCount': files,
        'episodeCount': total,
        'totalEpisodeCount': total,
        'sizeOnDisk': files * 2600 * 1024 * 1024,
        'percentOfEpisodes': total == 0 ? 0 : (files / total) * 100,
      };
  const files = 21, total = 26;
  return {
    'id': 101,
    'title': 'House of the Dragon',
    'tmdbId': 94997,
    'year': 2022,
    'monitored': true,
    'status': 'continuing',
    'seriesType': 'standard',
    'qualityProfileId': 1,
    'images': _posterImage(_hotd.poster),
    'statistics': {
      'seasonCount': 3,
      'episodeFileCount': files,
      'episodeCount': total,
      'totalEpisodeCount': total,
      'sizeOnDisk': files * 2600 * 1024 * 1024,
      'percentOfEpisodes': (files / total) * 100,
    },
    'seasons': [
      {'seasonNumber': 1, 'monitored': true, 'statistics': seasonStat(10, 10)},
      {'seasonNumber': 2, 'monitored': true, 'statistics': seasonStat(8, 8)},
      {'seasonNumber': 3, 'monitored': true, 'statistics': seasonStat(3, 8)},
    ],
  };
}

/// Sonarr calendar (GET /calendar) -> episode entries with airDateUtc +
/// seriesId. Feeds the TV-tab "Airing Next" (series whose id appears here) and
/// the Releases timeline. Dates span July 2026 for a populated current month.
List<Map<String, dynamic>> _sonarrCalendar() => [
      _sonarrCalEntry(101, 'House of the Dragon', 3, 3, 'The Red Sowing',
          '2026-07-02T01:00:00Z', true),
      _sonarrCalEntry(101, 'House of the Dragon', 3, 4, 'A Dance of Dragons',
          '2026-07-06T01:00:00Z', false),
      _sonarrCalEntry(101, 'House of the Dragon', 3, 5, 'The Sowing of Seeds',
          '2026-07-13T01:00:00Z', false),
      _sonarrCalEntry(101, 'House of the Dragon', 3, 6, 'Fire and Blood',
          '2026-07-20T01:00:00Z', false),
      _sonarrCalEntry(101, 'House of the Dragon', 3, 7, 'The Green Council',
          '2026-07-27T01:00:00Z', false),
      _sonarrCalEntry(
          102, 'The Boys', 5, 1, 'Department of Dirty Tricks',
          '2026-07-08T02:00:00Z', false),
      _sonarrCalEntry(102, 'The Boys', 5, 2, 'Kill Your Heroes',
          '2026-07-15T02:00:00Z', false),
      _sonarrCalEntry(102, 'The Boys', 5, 3, 'Diabolical',
          '2026-07-22T02:00:00Z', false),
      _sonarrCalEntry(102, 'The Boys', 5, 4, 'Assembly Required',
          '2026-07-29T02:00:00Z', false),
      _sonarrCalEntry(106, "Grey's Anatomy", 21, 15, 'Wishin and Hopin',
          '2026-07-09T01:00:00Z', false),
      _sonarrCalEntry(106, "Grey's Anatomy", 21, 16, 'Under Pressure',
          '2026-07-16T01:00:00Z', false),
      _sonarrCalEntry(111, 'The Rookie', 7, 9, 'Crossfire',
          '2026-07-14T01:00:00Z', false),
      _sonarrCalEntry(111, 'The Rookie', 7, 10, 'The Squad',
          '2026-07-21T01:00:00Z', false),
      _sonarrCalEntry(108, 'The Simpsons', 36, 12, 'Treehouse of Horror',
          '2026-07-12T00:00:00Z', false),
      _sonarrCalEntry(110, 'Dutton Ranch', 1, 1, 'Homecoming',
          '2026-07-30T01:00:00Z', false),
      _sonarrCalEntry(102, 'The Boys', 5, 5, 'The Big Ride',
          '2026-08-05T02:00:00Z', false),
      _sonarrCalEntry(101, 'House of the Dragon', 3, 8, 'The Dying of the Light',
          '2026-08-03T01:00:00Z', false),
    ];

Map<String, dynamic> _sonarrCalEntry(int seriesId, String seriesTitle,
        int season, int episode, String epTitle, String airUtc, bool hasFile) =>
    {
      'id': seriesId * 1000 + season * 100 + episode,
      'seriesId': seriesId,
      'seasonNumber': season,
      'episodeNumber': episode,
      'title': epTitle,
      'airDateUtc': airUtc,
      'hasFile': hasFile,
      'monitored': true,
      'series': {'title': seriesTitle},
    };

// ─── Downloads (normalized /api/downloads/{id}/queue) ────────────────────────

/// SABnzbd (usenet) queue: percentage progress, ETA seconds, no per-item speed.
Map<String, dynamic> _sabQueue() => {
      'paused': false,
      'speed_bps': (48 * 1024 * 1024).round(),
      'items': [
        _dlItem(
            id: 'SABnzbd_nzo_a1',
            name: 'Project.Hail.Mary.2026.2160p.BluRay.x265-FLUX',
            sizeGb: 14.6,
            progress: 63.4,
            etaSeconds: 214,
            status: 'Downloading',
            category: 'movies'),
        _dlItem(
            id: 'SABnzbd_nzo_a2',
            name: 'The.Boys.S05E01.1080p.WEB.H264-SuccessfulCrab',
            sizeGb: 3.9,
            progress: 41.0,
            etaSeconds: 96,
            status: 'Downloading',
            category: 'tv'),
        _dlItem(
            id: 'SABnzbd_nzo_a3',
            name: 'Enola.Holmes.3.2026.1080p.NF.WEB-DL.DDP5.1-NTb',
            sizeGb: 6.8,
            progress: 88.2,
            etaSeconds: 47,
            status: 'Downloading',
            category: 'movies'),
        _dlItem(
            id: 'SABnzbd_nzo_a4',
            name: 'Dutton.Ranch.S01E01.1080p.WEB.H264-EDITH',
            sizeGb: 4.2,
            progress: 12.5,
            etaSeconds: 372,
            status: 'Queued',
            category: 'tv'),
        _dlItem(
            id: 'SABnzbd_nzo_a5',
            name: 'The.Devil.Wears.Prada.2.2026.1080p.WEB-DL-FLUX',
            sizeGb: 7.1,
            progress: 100.0,
            etaSeconds: 0,
            status: 'Extracting',
            category: 'movies'),
      ],
    };

/// qBittorrent (torrent) queue: per-item speeds + seeders-driven states.
Map<String, dynamic> _qbitQueue() => {
      'paused': false,
      'speed_bps': (22 * 1024 * 1024).round(),
      'items': [
        _dlItem(
            id: 'a1b2c3torrenthash01',
            name: 'Mortal.Kombat.II.2026.2160p.WEB-DL.DV.HDR.DDP5.1-FLUX',
            sizeGb: 21.4,
            progress: 71.0,
            speedMbps: 14.2,
            etaSeconds: 744,
            status: 'downloading',
            category: 'movies'),
        _dlItem(
            id: 'a1b2c3torrenthash02',
            name: 'House.of.the.Dragon.S03E03.2160p.WEB.H265-NHTFS',
            sizeGb: 6.3,
            progress: 34.5,
            speedMbps: 6.7,
            etaSeconds: 511,
            status: 'downloading',
            category: 'tv'),
        _dlItem(
            id: 'a1b2c3torrenthash03',
            name: 'Supergirl.2026.1080p.WEB-DL.DDP5.1.Atmos-FLUX',
            sizeGb: 8.9,
            progress: 100.0,
            speedMbps: 0.9,
            etaSeconds: 0,
            status: 'uploading',
            category: 'movies'),
        _dlItem(
            id: 'a1b2c3torrenthash04',
            name: 'FROM.S03E10.1080p.WEB.H264-SuccessfulCrab',
            sizeGb: 2.8,
            progress: 5.0,
            speedMbps: 0.0,
            etaSeconds: 0,
            status: 'stalledDL',
            category: 'tv'),
      ],
    };

Map<String, dynamic> _dlItem({
  required String id,
  required String name,
  required double sizeGb,
  required double progress,
  double speedMbps = 0,
  int etaSeconds = 0,
  required String status,
  required String category,
}) {
  final size = (sizeGb * _gb).round();
  final left = (size * (1 - progress / 100)).round();
  return {
    'id': id,
    'name': name,
    'size_bytes': size,
    'size_left_bytes': left,
    'progress': progress,
    'speed_bps': (speedMbps * 1024 * 1024).round(),
    'eta_seconds': etaSeconds,
    'status': status,
    'category': category,
  };
}

// ─── Search results (shell search bar) ───────────────────────────────────────

/// Multi-search results returned for ANY query. Ids/titles overlap the Radarr
/// and Sonarr libraries so the availability chips render a full mix
/// (Available / Partially Available / Requested / no chip).
Map<String, dynamic> _searchResults() => _page([
      _movieItem(_phm, withMediaType: true), // Radarr hasFile -> Available
      _movieItem(_movies[1], withMediaType: true), // Toy Story 5 -> Available
      _tvItem(_hotd, withMediaType: true), // Sonarr 21/26 -> Partially Available
      _tvItem(_tv[4], withMediaType: true), // The Boys 32/40 -> Partial
      _movieItem(_movies[2], withMediaType: true), // Devil Wears Prada 2 -> Requested
      _movieItem(_movies[3], withMediaType: true), // Mortal Kombat II -> Requested
      _tvItem(_tv[5], withMediaType: true), // Game of Thrones 73/73 -> Available
      _tvItem(_tv[11], withMediaType: true), // Criminal Minds 300/300 -> Available
      _movieItem(_movies[4], withMediaType: true), // Supergirl -> Requested
      _tvItem(_tv[10], withMediaType: true), // Dutton Ranch 0/8 monitored -> Requested
      _movieItem(_movies[13], withMediaType: true), // Deep Water -> no chip
      _movieItem(_movies[10], withMediaType: true), // Backrooms -> no chip
    ]);

// ─── Admin approval queue (GET /api/admin/requests) ──────────────────────────

/// Global request settings + arr quality profiles. The /approvals screen loads
/// this alongside the pending list (for the approve dialog's profile picker);
/// without it the screen throws on the Map cast.
Map<String, dynamic> _adminRequestSettings() => {
      'settings': {
        'require_approval': true,
        'allow_season_choice': true,
        'default_season_scope': 'all',
        'allow_quality_choice': true,
        'default_quality_radarr': 1,
        'default_quality_sonarr': 1,
      },
      'radarr_profiles': [
        {'id': 1, 'name': 'HD-1080p'},
        {'id': 2, 'name': 'Ultra-HD'},
      ],
      'sonarr_profiles': [
        {'id': 1, 'name': 'HD-1080p'},
        {'id': 2, 'name': 'Ultra-HD'},
      ],
    };

/// Three pending requests from cozy household names. Drives both the /approvals
/// screen and the drawer approvals badge (count = list length).
List<Map<String, dynamic>> _pendingRequests() => [
      {
        'id': 1,
        'user_id': 11,
        'username': 'Josie',
        'tmdb_id': 94997,
        'tvdb_id': 371572,
        'media_type': 'tv',
        'title': 'House of the Dragon',
        'book_format': 'both',
        'season_scope': 'latest',
        'quality_profile_id': 1,
        'requested_at': '2026-07-04T19:22:00Z',
      },
      {
        'id': 2,
        'user_id': 12,
        'username': 'Marcus',
        'tmdb_id': 1127384,
        'tvdb_id': 0,
        'media_type': 'movie',
        'title': 'Deep Water',
        'book_format': 'both',
        'season_scope': '',
        'quality_profile_id': 1,
        'requested_at': '2026-07-04T08:41:00Z',
      },
      {
        'id': 3,
        'user_id': 13,
        'username': 'Dad',
        'tmdb_id': 936075,
        'tvdb_id': 0,
        'media_type': 'movie',
        'title': 'Michael',
        'book_format': 'both',
        'season_scope': '',
        'quality_profile_id': 1,
        'requested_at': '2026-07-03T21:05:00Z',
      },
    ];

// ─── Path router ─────────────────────────────────────────────────────────────

/// Maps a backend request (path + query) to a populated demo body, or null to
/// let the caller fall back to an empty-but-well-shaped default.
Object? screenshotBodyFor(String rawPath, Map<String, dynamic> query) {
  // Dio keeps query params separate, but strip a query suffix defensively so
  // endsWith() checks stay robust.
  final path = rawPath.split('?').first;

  // ── Discovery rows ──
  if (path.endsWith('/api/discover/movies/popular')) return _popularMovies();
  if (path.endsWith('/api/discover/movies/top-rated')) return _topRatedMovies();
  if (path.endsWith('/api/discover/movies/upcoming')) return _upcomingMovies();
  if (path.endsWith('/api/discover/movies/now-playing')) return _popularMovies();
  if (path.endsWith('/api/discover/tv/popular')) return _popularTv();
  if (path.endsWith('/api/discover/trending')) {
    return _page([
      _movieItem(_phm, withMediaType: true),
      _tvItem(_hotd, withMediaType: true),
      _movieItem(_movies[1], withMediaType: true),
      _tvItem(_tv[4], withMediaType: true),
    ]);
  }
  if (path.endsWith('/api/trakt/anticipated')) {
    return _traktAnticipated((query['type'] as String?) ?? 'movies');
  }

  // ── Search ──
  if (path.endsWith('/api/search')) return _searchResults();

  // ── Detail: recommendations / similar before the bare detail ──
  if (path.contains('/api/media/movie/')) {
    if (path.endsWith('/recommendations') || path.endsWith('/similar')) {
      return _relatedMovies();
    }
    final id = _lastIntSegment(path);
    if (id != null) return _movieDetail(id);
  }
  if (path.contains('/api/media/tv/')) {
    if (path.endsWith('/recommendations') || path.endsWith('/similar')) {
      return _relatedTv();
    }
    final id = _lastIntSegment(path);
    if (id != null) return _tvDetail(id);
  }

  // ── Request surface ──
  if (path.endsWith('/api/requests/options')) return _requestOptions();
  if (path.contains('/api/requests/') && path.endsWith('/status')) {
    final id = _intSegmentBefore(path, 'status');
    return _requestStatus(id ?? 0);
  }

  // ── Admin approvals + boot badges ──
  if (path.endsWith('/api/admin/request-settings')) {
    return _adminRequestSettings();
  }
  if (path.endsWith('/api/admin/requests')) return _pendingRequests();
  if (path.endsWith('/api/admin/issues') || path.endsWith('/api/issues')) {
    return {'issues': const []};
  }
  if (path.endsWith('/api/admin/agent-actions')) return {'actions': const []};
  if (path.endsWith('/api/admin/setup-status')) return {'items': const []};

  // ── Radarr / Sonarr verbatim v3 proxy (any instance id) ──
  if (path.endsWith('/api/v3/movie')) return _radarrLibrary();
  if (path.endsWith('/api/v3/series')) return _sonarrLibrary();
  if (path.endsWith('/api/v3/queue')) {
    return path.contains('/sonarr') ? _sonarrQueue() : _radarrQueue();
  }
  if (path.endsWith('/api/v3/calendar')) {
    // The instance id disambiguates movie vs episode calendars.
    return path.contains('/sonarr') ? _sonarrCalendar() : _radarrCalendar();
  }

  // ── Download client queues (per instance) ──
  if (path.contains('/api/downloads/') && path.endsWith('/queue')) {
    return path.contains('qbit') ? _qbitQueue() : _sabQueue();
  }

  return null;
}

/// Sonarr queue (GET /queue -> {records:[...]}) for the Sonarr queue screen.
Map<String, dynamic> _sonarrQueue() => {
      'page': 1,
      'pageSize': 100,
      'totalRecords': 1,
      'records': [
        _arrQueueRecord(
          id: 9101,
          seriesId: 101,
          title: 'House.of.the.Dragon.S03E03.2160p.WEB.H265-NHTFS',
          quality: 'WEBDL-2160p',
          size: 6.3 * _gb,
          sizeleft: 4.1 * _gb,
          timeleft: '00:08:31',
          downloadClient: 'qBittorrent',
          protocol: 'torrent',
          indexer: 'TorrentLeech',
          extraKey: 'series',
          extraTitle: 'House of the Dragon',
          episode: {
            'id': 55003,
            'seasonNumber': 3,
            'episodeNumber': 3,
            'title': 'The Red Sowing',
          },
        ),
      ],
    };

int? _lastIntSegment(String path) {
  for (final seg in path.split('/').reversed) {
    final n = int.tryParse(seg);
    if (n != null) return n;
  }
  return null;
}

int? _intSegmentBefore(String path, String marker) {
  final parts = path.split('/');
  final idx = parts.indexOf(marker);
  if (idx <= 0) return null;
  return int.tryParse(parts[idx - 1]);
}
