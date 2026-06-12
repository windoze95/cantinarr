import 'package:dio/dio.dart';
import 'tautulli_models.dart';

/// Networking layer for Tautulli, proxied through the Cantinarr backend's
/// normalized /api/tautulli API.
class TautulliApiService {
  final Dio _dio;
  final String _instanceId;

  TautulliApiService({required Dio backendDio, required String instanceId})
      : _dio = backendDio,
        _instanceId = instanceId;

  String get _basePath => '/api/tautulli/$_instanceId';

  Future<TautulliActivity> getActivity() async {
    final resp = await _dio.get('$_basePath/activity');
    return TautulliActivity.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<TautulliHistoryItem>> getHistory({int limit = 50}) async {
    final resp =
        await _dio.get('$_basePath/history', queryParameters: {'limit': limit});
    final items =
        (resp.data as Map<String, dynamic>)['items'] as List<dynamic>? ?? [];
    return items
        .map((i) => TautulliHistoryItem.fromJson(i as Map<String, dynamic>))
        .toList();
  }

  Future<TautulliStats> getStats({int days = 30}) async {
    final resp =
        await _dio.get('$_basePath/stats', queryParameters: {'days': days});
    return TautulliStats.fromJson(resp.data as Map<String, dynamic>);
  }
}
