import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import 'tmdb_models.dart';
import 'trakt_models.dart';

/// Provides the unified discover service backed by the server.
final discoverServiceProvider = Provider<DiscoverApiService>(
  (ref) => DiscoverApiService(backendDio: ref.watch(backendClientProvider)),
);

/// The headline discovery row: a page of titles plus the feed that answered.
/// The admin picks the feed server-side, so the row learns which one it got
/// from the response rather than from a setting it would have to read itself.
class FeaturedFeed {
  final String source;
  final List<MediaItem> items;

  /// Where this page sits in the feed. Page one is the row; the server
  /// continues the same source on later pages for the grid under it.
  final int page;
  final int totalPages;

  const FeaturedFeed({
    required this.source,
    required this.items,
    this.page = 1,
    this.totalPages = 1,
  });

  static const empty = FeaturedFeed(source: '', items: []);

  /// The page in the shape every other feed uses, for paging helpers.
  TmdbPage<MediaItem> get asPage => TmdbPage(
        page: page,
        totalPages: totalPages,
        totalResults: items.length,
        results: items,
      );
}

/// The row's title for a given source. A row named "Popular" over a trending
/// feed would misdescribe what it is showing, so the title follows the source.
String featuredRowTitle(String source, {required bool isTv}) =>
    switch (source) {
      'trakt_trending' => 'Trending Now',
      'tmdb_popular' => isTv ? 'Popular TV Shows' : 'Popular Movies',
      _ => 'Trending This Week',
    };

/// Unified discovery service that calls the Cantinarr backend (which proxies
/// TMDB/Trakt). API keys never leave the server.
class DiscoverApiService {
  final Dio _dio;

  DiscoverApiService({required Dio backendDio}) : _dio = backendDio;

  // ─── Trending ───────────────────────────────────────

  Future<TmdbPage<MediaItem>> fetchTrending({
    String timeWindow = 'day',
    int page = 1,
  }) async {
    final resp = await _dio.get(
      '/api/discover/trending',
      queryParameters: {'time_window': timeWindow, 'page': page},
    );
    return TmdbPage.fromJson(
      resp.data as Map<String, dynamic>,
      (json) => MediaItem.fromTrendingJson(json),
    );
  }

  // ─── Popular / Top Rated / Upcoming ─────────────────

  Future<TmdbPage<MediaItem>> fetchPopularMovies({int page = 1}) async {
    final resp = await _dio.get(
      '/api/discover/movies/popular',
      queryParameters: {'page': page},
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  Future<TmdbPage<MediaItem>> fetchPopularTV({int page = 1}) async {
    final resp = await _dio.get(
      '/api/discover/tv/popular',
      queryParameters: {'page': page},
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromTVJson);
  }

  /// The configured headline TV feed (TMDB trending, Trakt trending, or TMDB
  /// popular). Every source arrives in the TMDB page shape.
  Future<FeaturedFeed> fetchFeaturedTV({int page = 1}) async {
    final resp = await _dio.get(
      '/api/discover/tv/featured',
      queryParameters: {'page': page},
    );
    return _featuredFeed(resp.data, MediaItem.fromTVJson);
  }

  /// The configured headline movie feed.
  Future<FeaturedFeed> fetchFeaturedMovies({int page = 1}) async {
    final resp = await _dio.get(
      '/api/discover/movies/featured',
      queryParameters: {'page': page},
    );
    return _featuredFeed(resp.data, MediaItem.fromMovieJson);
  }

  FeaturedFeed _featuredFeed(
    dynamic data,
    MediaItem Function(Map<String, dynamic>) fromJson,
  ) {
    final json = (data as Map).cast<String, dynamic>();
    final parsed = TmdbPage.fromJson(json, fromJson);
    return FeaturedFeed(
      source: json['source'] as String? ?? '',
      items: parsed.results,
      page: parsed.page,
      totalPages: parsed.totalPages,
    );
  }

  Future<TmdbPage<MediaItem>> fetchTopRatedMovies({int page = 1}) async {
    final resp = await _dio.get(
      '/api/discover/movies/top-rated',
      queryParameters: {'page': page},
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  Future<TmdbPage<MediaItem>> fetchUpcomingMovies({int page = 1}) async {
    final resp = await _dio.get(
      '/api/discover/movies/upcoming',
      queryParameters: {'page': page},
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  Future<TmdbPage<MediaItem>> fetchNowPlayingMovies({int page = 1}) async {
    final resp = await _dio.get(
      '/api/discover/movies/now-playing',
      queryParameters: {'page': page},
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  // ─── Search ─────────────────────────────────────────

  Future<TmdbPage<MediaItem>> multiSearch({
    required String query,
    int page = 1,
  }) async {
    final resp = await _dio.get(
      '/api/search',
      queryParameters: {'query': query, 'page': page},
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMultiSearchJson);
  }

  // ─── Discover ───────────────────────────────────────

  /// TMDB discover for movies. [releasedFrom]/[releasedTo] are inclusive
  /// `YYYY-MM-DD` bounds on the primary release date; [minVotes] is worth
  /// sending with a rating floor or a rating sort so one-vote titles do not
  /// lead (the server floors rating sorts itself when it is absent).
  Future<TmdbPage<MediaItem>> discoverMovies({
    int page = 1,
    List<int>? genreIds,
    String? sortBy,
    int? year,
    String? releasedFrom,
    String? releasedTo,
    double? minRating,
    int? minVotes,
    String? language,
    List<int>? watchProviderIds,
    String? watchRegion,
    List<int>? keywordIds,
    List<int>? companyIds,
  }) async {
    final params = _discoverParams(
      page: page,
      genreIds: genreIds,
      sortBy: sortBy,
      minRating: minRating,
      minVotes: minVotes,
      language: language,
      watchProviderIds: watchProviderIds,
      watchRegion: watchRegion,
      keywordIds: keywordIds,
      companyIds: companyIds,
    );
    if (year != null) params['primary_release_year'] = year;
    if (releasedFrom != null) params['primary_release_date.gte'] = releasedFrom;
    if (releasedTo != null) params['primary_release_date.lte'] = releasedTo;
    final resp = await _dio.get(
      '/api/discover/movies',
      queryParameters: params,
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  /// TMDB discover for TV; the date bounds apply to the first air date.
  Future<TmdbPage<MediaItem>> discoverTV({
    int page = 1,
    List<int>? genreIds,
    String? sortBy,
    int? year,
    String? airedFrom,
    String? airedTo,
    double? minRating,
    int? minVotes,
    String? language,
    List<int>? watchProviderIds,
    String? watchRegion,
    List<int>? keywordIds,
    List<int>? companyIds,
  }) async {
    final params = _discoverParams(
      page: page,
      genreIds: genreIds,
      sortBy: sortBy,
      minRating: minRating,
      minVotes: minVotes,
      language: language,
      watchProviderIds: watchProviderIds,
      watchRegion: watchRegion,
      keywordIds: keywordIds,
      companyIds: companyIds,
    );
    if (year != null) params['first_air_date_year'] = year;
    if (airedFrom != null) params['first_air_date.gte'] = airedFrom;
    if (airedTo != null) params['first_air_date.lte'] = airedTo;
    final resp = await _dio.get(
      '/api/discover/tv',
      queryParameters: params,
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromTVJson);
  }

  /// The keys shared by both media types. A named language is an explicit
  /// ask the server never English-filters; keywords must all match (comma)
  /// while any of the companies may (pipe).
  Map<String, dynamic> _discoverParams({
    required int page,
    List<int>? genreIds,
    String? sortBy,
    double? minRating,
    int? minVotes,
    String? language,
    List<int>? watchProviderIds,
    String? watchRegion,
    List<int>? keywordIds,
    List<int>? companyIds,
  }) {
    final params = <String, dynamic>{'page': page};
    if (genreIds != null && genreIds.isNotEmpty) {
      params['with_genres'] = genreIds.join(',');
    }
    if (sortBy != null) params['sort_by'] = sortBy;
    if (minRating != null) params['vote_average.gte'] = minRating;
    if (minVotes != null) params['vote_count.gte'] = minVotes;
    if (language != null && language.isNotEmpty) {
      params['with_original_language'] = language;
    }
    if (watchProviderIds != null && watchProviderIds.isNotEmpty) {
      params['with_watch_providers'] = watchProviderIds.join('|');
      params['watch_region'] = watchRegion ?? 'US';
    }
    if (keywordIds != null && keywordIds.isNotEmpty) {
      params['with_keywords'] = keywordIds.join(',');
    }
    if (companyIds != null && companyIds.isNotEmpty) {
      params['with_companies'] = companyIds.join('|');
    }
    return params;
  }

  // ─── Details ────────────────────────────────────────

  Future<MovieDetail> movieDetail(int id) async {
    final resp = await _dio.get('/api/media/movie/$id');
    return MovieDetail.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<TVDetail> tvDetail(int id) async {
    final resp = await _dio.get('/api/media/tv/$id');
    return TVDetail.fromJson(resp.data as Map<String, dynamic>);
  }

  // ─── Person ────────────────────────────────────────

  Future<PersonDetail> personDetail(int id) async {
    final resp = await _dio.get('/api/media/person/$id');
    return PersonDetail.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<PersonCredit>> personCredits(int id) async {
    final resp = await _dio.get('/api/media/person/$id/credits');
    final data = resp.data as Map<String, dynamic>;
    final cast = (data['cast'] as List<dynamic>?)
            ?.map((e) => PersonCredit.fromJson(e as Map<String, dynamic>))
            .toList() ??
        [];
    final crew = (data['crew'] as List<dynamic>?)
            ?.map((e) => PersonCredit.fromJson(e as Map<String, dynamic>))
            .toList() ??
        [];

    // Deduplicate: prefer cast over crew for same id+mediaType
    final seen = <String>{};
    final merged = <PersonCredit>[];
    for (final c in cast) {
      final key = '${c.id}:${c.mediaType}';
      if (seen.add(key)) merged.add(c);
    }
    for (final c in crew) {
      final key = '${c.id}:${c.mediaType}';
      if (seen.add(key)) merged.add(c);
    }
    return merged;
  }

  // ─── Recommendations ───────────────────────────────

  Future<TmdbPage<MediaItem>> movieRecommendations(int id,
      {int page = 1}) async {
    final resp = await _dio.get(
      '/api/media/movie/$id/recommendations',
      queryParameters: {'page': page},
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  Future<TmdbPage<MediaItem>> tvRecommendations(int id,
      {int page = 1}) async {
    final resp = await _dio.get(
      '/api/media/tv/$id/recommendations',
      queryParameters: {'page': page},
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromTVJson);
  }

  Future<TmdbPage<MediaItem>> similarMovies(int id, {int page = 1}) async {
    final resp = await _dio.get(
      '/api/media/movie/$id/similar',
      queryParameters: {'page': page},
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  Future<TmdbPage<MediaItem>> similarTV(int id, {int page = 1}) async {
    final resp = await _dio.get(
      '/api/media/tv/$id/similar',
      queryParameters: {'page': page},
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromTVJson);
  }

  // ─── Genres ─────────────────────────────────────────

  Future<List<Genre>> movieGenres() async {
    final resp = await _dio.get('/api/genres/movie');
    return (resp.data['genres'] as List<dynamic>)
        .map((g) => Genre.fromJson(g as Map<String, dynamic>))
        .toList();
  }

  Future<List<Genre>> tvGenres() async {
    final resp = await _dio.get('/api/genres/tv');
    return (resp.data['genres'] as List<dynamic>)
        .map((g) => Genre.fromJson(g as Map<String, dynamic>))
        .toList();
  }

  // ─── Watch Providers ────────────────────────────────

  Future<List<WatchProvider>> movieWatchProviders(
      {String region = 'US'}) =>
      _watchProviders('/api/providers/movie', region);

  Future<List<WatchProvider>> tvWatchProviders({String region = 'US'}) =>
      _watchProviders('/api/providers/tv', region);

  /// The services TMDB knows for one region, in TMDB's display order.
  Future<List<WatchProvider>> _watchProviders(String path, String region) async {
    final resp = await _dio.get(path, queryParameters: {'region': region});
    final providers = (resp.data['results'] as List<dynamic>)
        .map((p) => WatchProvider.fromJson(p as Map<String, dynamic>))
        .toList();
    providers.sort((a, b) => a.displayPriority.compareTo(b.displayPriority));
    return providers;
  }

  /// The countries TMDB tracks streaming availability for, by English name.
  Future<List<WatchRegion>> watchRegions() async {
    final resp = await _dio.get('/api/providers/regions');
    final regions = (resp.data['results'] as List<dynamic>)
        .map((r) => WatchRegion.fromJson(r as Map<String, dynamic>))
        .toList();
    regions.sort((a, b) => a.name.compareTo(b.name));
    return regions;
  }

  // ─── Languages ─────────────────────────────────────

  /// Every language TMDB can filter on, by English name.
  Future<List<TmdbLanguage>> languages() async {
    final resp = await _dio.get('/api/languages');
    final languages = (resp.data as List<dynamic>)
        .map((l) => TmdbLanguage.fromJson(l as Map<String, dynamic>))
        .toList();
    languages.sort((a, b) =>
        a.englishName.toLowerCase().compareTo(b.englishName.toLowerCase()));
    return languages;
  }

  // ─── Keyword and studio lookups ────────────────────

  Future<List<TaggedId>> searchKeywords(String query) =>
      _taggedSearch('/api/search/keyword', query);

  Future<List<TaggedId>> searchCompanies(String query) =>
      _taggedSearch('/api/search/company', query);

  Future<List<TaggedId>> _taggedSearch(String path, String query) async {
    final resp = await _dio.get(path, queryParameters: {'query': query});
    return (resp.data['results'] as List<dynamic>)
        .map((r) => TaggedId.fromJson(r as Map<String, dynamic>))
        .toList();
  }

  // ─── Trakt ──────────────────────────────────────────

  Future<List<TraktItem>> getTraktTrending(String type,
      {int page = 1}) async {
    final resp = await _dio.get(
      '/api/trakt/trending',
      queryParameters: {'type': type, 'page': page},
    );
    return (resp.data as List<dynamic>)
        .map((j) =>
            TraktItem.fromTrendingJson(j as Map<String, dynamic>, type))
        .toList();
  }

  Future<List<TraktItem>> getTraktPopular(String type,
      {int page = 1}) async {
    final resp = await _dio.get(
      '/api/trakt/popular',
      queryParameters: {'type': type, 'page': page},
    );
    return (resp.data as List<dynamic>)
        .map((j) =>
            TraktItem.fromPopularJson(j as Map<String, dynamic>, type))
        .toList();
  }

  Future<List<TraktList>> getTraktPopularLists({int page = 1}) async {
    final resp = await _dio.get(
      '/api/trakt/lists',
      queryParameters: {'page': page},
    );
    return (resp.data as List<dynamic>)
        .map((j) => TraktList.fromJson(j as Map<String, dynamic>))
        .toList();
  }

  Future<List<TraktItem>> getTraktListItems(String user, String slug) async {
    final resp = await _dio.get(
      '/api/trakt/lists/$user/$slug/items',
    );
    return (resp.data as List<dynamic>).map((j) {
      final json = j as Map<String, dynamic>;
      final type = json['type'] as String? ?? 'movie';
      final inner = json[type] as Map<String, dynamic>? ?? {};
      final ids =
          TraktIds.fromJson(inner['ids'] as Map<String, dynamic>? ?? {});
      return TraktItem(
        tmdbId: ids.tmdb,
        title: (inner['title'] ?? 'Untitled') as String,
        year: inner['year'] as int?,
        overview: inner['overview'] as String?,
        ids: ids,
        mediaType: type,
      );
    }).toList();
  }

  Future<List<TraktCalendarItem>> getTraktCalendar({int days = 14}) async {
    final resp = await _dio.get(
      '/api/trakt/calendar',
      queryParameters: {'days': days},
    );
    return (resp.data as List<dynamic>)
        .map((j) => TraktCalendarItem.fromJson(j as Map<String, dynamic>))
        .toList();
  }

  Future<List<TraktItem>> getTraktAnticipated(String type,
      {int page = 1}) async {
    final resp = await _dio.get(
      '/api/trakt/anticipated',
      queryParameters: {'type': type, 'page': page},
    );
    return (resp.data as List<dynamic>)
        .map((j) =>
            TraktItem.fromAnticipatedJson(j as Map<String, dynamic>, type))
        .toList();
  }

  Future<List<TraktItem>> getTraktRecommendations(String type) async {
    final resp = await _dio.get(
      '/api/trakt/recommendations',
      queryParameters: {'type': type},
    );
    return (resp.data as List<dynamic>)
        .map((j) =>
            TraktItem.fromPopularJson(j as Map<String, dynamic>, type))
        .toList();
  }
}
