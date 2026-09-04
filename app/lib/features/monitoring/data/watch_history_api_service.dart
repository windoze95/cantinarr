import 'package:dio/dio.dart';
import '../../../core/models/backend_connection.dart';
import 'watch_history_models.dart';

/// Networking layer for the Monitoring module, proxied through the Cantinarr
/// backend's normalized watch-history API. Tautulli and Tracearr instances
/// answer with the same shapes.
class WatchHistoryApiService {
  final Dio _dio;
  final ServiceInstance _instance;

  WatchHistoryApiService({
    required Dio backendDio,
    required ServiceInstance instance,
  })  : _dio = backendDio,
        _instance = instance;

  /// Tautulli keeps the path it has always had, so this app works against
  /// servers that predate the neutral route; every other provider only
  /// exists on servers that have it. The server serves both from one
  /// handler.
  String get _basePath => _instance.serviceType == 'tautulli'
      ? '/api/tautulli/${_instance.id}'
      : '/api/watch-history/${_instance.id}';

  Future<ActivitySnapshot> getActivity() async {
    final resp = await _dio.get('$_basePath/activity');
    return ActivitySnapshot.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<HistorySnapshot> getHistory({int limit = 50}) async {
    final resp =
        await _dio.get('$_basePath/history', queryParameters: {'limit': limit});
    return HistorySnapshot.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<StatsSnapshot> getStats({int days = 30}) async {
    final resp =
        await _dio.get('$_basePath/stats', queryParameters: {'days': days});
    return StatsSnapshot.fromJson(resp.data as Map<String, dynamic>);
  }
}
