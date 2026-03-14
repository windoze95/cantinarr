import 'package:dio/dio.dart';
import '../../../core/config/app_config.dart';
import 'tmdb_models.dart';

/// Client for TMDB API v3 – handles all discovery, search, and detail fetches.
class TmdbApiService {
  final Dio _dio;
  final String _apiKey;

  TmdbApiService({required String apiKey})
      : _apiKey = apiKey,
        _dio = Dio(BaseOptions(
          baseUrl: AppConfig.tmdbApiBase,
          connectTimeout: AppConfig.requestTimeout,
          receiveTimeout: AppConfig.requestTimeout,
        ));

  Map<String, dynamic> _params([Map<String, dynamic>? extra]) => {
        'api_key': _apiKey,
        'language': 'en-US',
        ...?extra,
      };

  // ─── Trending ───────────────────────────────────────

  /// Fetch trending movies and TV shows (day or week).
  Future<TmdbPage<MediaItem>> fetchTrending({
    String timeWindow = 'day',
    int page = 1,
  }) async {
    final resp = await _dio.get(
      '/trending/all/$timeWindow',
      queryParameters: _params({'page': page}),
    );
    return TmdbPage.fromJson(
      resp.data as Map<String, dynamic>,
      (json) => MediaItem.fromTrendingJson(json),
    );
  }

  // ─── Popular / Top Rated / Upcoming ─────────────────

  Future<TmdbPage<MediaItem>> fetchPopularMovies({int page = 1}) async {
    final resp = await _dio.get(
      '/movie/popular',
      queryParameters: _params({'page': page}),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  Future<TmdbPage<MediaItem>> fetchPopularTV({int page = 1}) async {
    final resp = await _dio.get(
      '/tv/popular',
      queryParameters: _params({'page': page}),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromTVJson);
  }

  Future<TmdbPage<MediaItem>> fetchTopRatedMovies({int page = 1}) async {
    final resp = await _dio.get(
      '/movie/top_rated',
      queryParameters: _params({'page': page}),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  Future<TmdbPage<MediaItem>> fetchUpcomingMovies({int page = 1}) async {
    final resp = await _dio.get(
      '/movie/upcoming',
      queryParameters: _params({'page': page}),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  Future<TmdbPage<MediaItem>> fetchNowPlayingMovies({int page = 1}) async {
    final resp = await _dio.get(
      '/movie/now_playing',
      queryParameters: _params({'page': page}),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  // ─── Search ─────────────────────────────────────────

  /// Multi-search: movies, TV shows, and people in one query.
  Future<TmdbPage<MediaItem>> multiSearch({
    required String query,
    int page = 1,
  }) async {
    final resp = await _dio.get(
      '/search/multi',
      queryParameters: _params({'query': query, 'page': page}),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMultiSearchJson);
  }

  // ─── Discover ───────────────────────────────────────

  /// Discover movies with filters.
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
      '/discover/movie',
      queryParameters: _params(params),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  /// Discover TV shows with filters.
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
      '/discover/tv',
      queryParameters: _params(params),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromTVJson);
  }

  // ─── Details ────────────────────────────────────────

  Future<MovieDetail> movieDetail(int id) async {
    final resp = await _dio.get(
      '/movie/$id',
      queryParameters: _params({'append_to_response': 'videos'}),
    );
    return MovieDetail.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<TVDetail> tvDetail(int id) async {
    final resp = await _dio.get(
      '/tv/$id',
      queryParameters: _params({'append_to_response': 'videos'}),
    );
    return TVDetail.fromJson(resp.data as Map<String, dynamic>);
  }

  // ─── Recommendations ───────────────────────────────

  Future<TmdbPage<MediaItem>> movieRecommendations(int id,
      {int page = 1}) async {
    final resp = await _dio.get(
      '/movie/$id/recommendations',
      queryParameters: _params({'page': page}),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  Future<TmdbPage<MediaItem>> tvRecommendations(int id,
      {int page = 1}) async {
    final resp = await _dio.get(
      '/tv/$id/recommendations',
      queryParameters: _params({'page': page}),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromTVJson);
  }

  Future<TmdbPage<MediaItem>> similarMovies(int id, {int page = 1}) async {
    final resp = await _dio.get(
      '/movie/$id/similar',
      queryParameters: _params({'page': page}),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromMovieJson);
  }

  Future<TmdbPage<MediaItem>> similarTV(int id, {int page = 1}) async {
    final resp = await _dio.get(
      '/tv/$id/similar',
      queryParameters: _params({'page': page}),
    );
    return TmdbPage.fromJson(resp.data, MediaItem.fromTVJson);
  }

  // ─── Genres ─────────────────────────────────────────

  Future<List<Genre>> movieGenres() async {
    final resp = await _dio.get(
      '/genre/movie/list',
      queryParameters: _params(),
    );
    return (resp.data['genres'] as List<dynamic>)
        .map((g) => Genre.fromJson(g as Map<String, dynamic>))
        .toList();
  }

  Future<List<Genre>> tvGenres() async {
    final resp = await _dio.get(
      '/genre/tv/list',
      queryParameters: _params(),
    );
    return (resp.data['genres'] as List<dynamic>)
        .map((g) => Genre.fromJson(g as Map<String, dynamic>))
        .toList();
  }

  // ─── Watch Providers ────────────────────────────────

  Future<List<WatchProvider>> movieWatchProviders(
      {String region = 'US'}) async {
    final resp = await _dio.get(
      '/watch/providers/movie',
      queryParameters: _params({'watch_region': region}),
    );
    return (resp.data['results'] as List<dynamic>)
        .map((p) => WatchProvider.fromJson(p as Map<String, dynamic>))
        .toList();
  }
}
