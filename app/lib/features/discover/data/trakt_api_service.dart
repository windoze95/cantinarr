import 'package:dio/dio.dart';
import '../../../core/config/app_config.dart';
import 'trakt_models.dart';

/// Client for the Trakt API v2 – trending, popular, lists, calendar.
class TraktApiService {
  final Dio _dio;
  final String clientId;

  TraktApiService({required this.clientId})
      : _dio = Dio(BaseOptions(
          baseUrl: 'https://api.trakt.tv',
          connectTimeout: AppConfig.requestTimeout,
          receiveTimeout: AppConfig.requestTimeout,
          headers: {
            'Content-Type': 'application/json',
            'trakt-api-version': '2',
            'trakt-api-key': clientId,
          },
        ));

  /// Get trending movies or shows.
  Future<List<TraktItem>> getTrending(String type, {int page = 1}) async {
    final resp = await _dio.get(
      '/$type/trending',
      queryParameters: {'page': page, 'limit': 20, 'extended': 'full'},
    );
    return (resp.data as List<dynamic>)
        .map((j) =>
            TraktItem.fromTrendingJson(j as Map<String, dynamic>, type))
        .toList();
  }

  /// Get popular movies or shows (most watched of all time).
  Future<List<TraktItem>> getPopular(String type, {int page = 1}) async {
    final resp = await _dio.get(
      '/$type/popular',
      queryParameters: {'page': page, 'limit': 20, 'extended': 'full'},
    );
    return (resp.data as List<dynamic>)
        .map((j) =>
            TraktItem.fromPopularJson(j as Map<String, dynamic>, type))
        .toList();
  }

  /// Get popular user-curated lists.
  Future<List<TraktList>> getPopularLists({int page = 1}) async {
    final resp = await _dio.get(
      '/lists/popular',
      queryParameters: {'page': page, 'limit': 20},
    );
    return (resp.data as List<dynamic>)
        .map((j) => TraktList.fromJson(j as Map<String, dynamic>))
        .toList();
  }

  /// Get items from a specific list.
  Future<List<TraktItem>> getListItems(String listId) async {
    final resp = await _dio.get(
      '/users/$listId/items',
      queryParameters: {'extended': 'full'},
    );
    return (resp.data as List<dynamic>).map((j) {
      final json = j as Map<String, dynamic>;
      final type = json['type'] as String? ?? 'movie';
      final inner = json[type] as Map<String, dynamic>? ?? {};
      final ids = TraktIds.fromJson(inner['ids'] as Map<String, dynamic>? ?? {});
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

  /// Get upcoming calendar items for the next N days.
  Future<List<TraktCalendarItem>> getCalendar({int days = 14}) async {
    final today = DateTime.now().toIso8601String().split('T').first;
    final resp = await _dio.get(
      '/calendars/all/shows/$today/$days',
    );
    return (resp.data as List<dynamic>)
        .map(
            (j) => TraktCalendarItem.fromJson(j as Map<String, dynamic>))
        .toList();
  }

  /// Get recommended movies or shows (requires Trakt user auth — optional).
  Future<List<TraktItem>> getRecommendations(String type) async {
    final resp = await _dio.get(
      '/recommendations/$type',
      queryParameters: {'limit': 20, 'extended': 'full'},
    );
    return (resp.data as List<dynamic>)
        .map((j) =>
            TraktItem.fromPopularJson(j as Map<String, dynamic>, type))
        .toList();
  }
}
