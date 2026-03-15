import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import 'tmdb_models.dart';
import 'trakt_models.dart';

/// Provides the unified discover service backed by the server.
final discoverServiceProvider = Provider<DiscoverApiService>(
  (ref) => DiscoverApiService(backendDio: ref.watch(backendClientProvider)),
);

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

  Future<TmdbPage<MediaItem>> discoverMovies({
    int page = 1,
    List<int>? genreIds,
    String? sortBy,
    int? year,
    List<int>? watchProviderIds,
    String? watchRegion,
  }) async {
    final params = <String, dynamic>{'page': page};
    if (genreIds != null && genreIds.isNotEmpty) {
      params['with_genres'] = genreIds.join(',');
    }
    if (sortBy != null) params['sort_by'] = sortBy;
    if (year != null) params['primary_release_year'] = year;
    if (watchProviderIds != null && watchProviderIds.isNotEmpty) {
      params['with_watch_providers'] = watchProviderIds.join('|');
      params['watch_region'] = watchRegion ?? 'US';
    }
    final resp = await _dio.get(
      '/api/discover/movies',
      queryParameters: params,
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  Future<TmdbPage<MediaItem>> discoverTV({
    int page = 1,
    List<int>? genreIds,
    String? sortBy,
    int? year,
    List<int>? watchProviderIds,
    String? watchRegion,
  }) async {
    final params = <String, dynamic>{'page': page};
    if (genreIds != null && genreIds.isNotEmpty) {
      params['with_genres'] = genreIds.join(',');
    }
    if (sortBy != null) params['sort_by'] = sortBy;
    if (year != null) params['first_air_date_year'] = year;
    if (watchProviderIds != null && watchProviderIds.isNotEmpty) {
      params['with_watch_providers'] = watchProviderIds.join('|');
      params['watch_region'] = watchRegion ?? 'US';
    }
    final resp = await _dio.get(
      '/api/discover/tv',
      queryParameters: params,
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromTVJson);
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
      {String region = 'US'}) async {
    final resp = await _dio.get(
      '/api/providers/movie',
      queryParameters: {'region': region},
    );
    return (resp.data['results'] as List<dynamic>)
        .map((p) => WatchProvider.fromJson(p as Map<String, dynamic>))
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
