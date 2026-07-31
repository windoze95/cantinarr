import 'package:dio/dio.dart';
import 'downloads_models.dart';

/// Networking layer for download clients (SABnzbd / qBittorrent), proxied
/// through the Cantinarr backend's normalized downloads API.
class DownloadsApiService {
  final Dio _dio;
  final String _instanceId;

  DownloadsApiService({required Dio backendDio, required String instanceId})
      : _dio = backendDio,
        _instanceId = instanceId;

  String get _basePath => '/api/downloads/$_instanceId';

  Future<DownloadsQueue> getQueue() async {
    final resp = await _dio.get('$_basePath/queue');
    return DownloadsQueue.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<void> pauseItem(String itemId) async {
    await _dio.post('$_basePath/queue/${Uri.encodeComponent(itemId)}/pause');
  }

  Future<void> resumeItem(String itemId) async {
    await _dio.post('$_basePath/queue/${Uri.encodeComponent(itemId)}/resume');
  }

  Future<void> deleteItem(String itemId, {bool deleteData = false}) async {
    await _dio.delete(
      '$_basePath/queue/${Uri.encodeComponent(itemId)}',
      queryParameters: {'deleteData': deleteData},
    );
  }

  Future<void> pauseAll() async {
    await _dio.post('$_basePath/pause');
  }

  Future<void> resumeAll() async {
    await _dio.post('$_basePath/resume');
  }

  Future<List<DownloadHistoryItem>> getHistory({int limit = 50}) async {
    final resp =
        await _dio.get('$_basePath/history', queryParameters: {'limit': limit});
    final items =
        (resp.data as Map<String, dynamic>)['items'] as List<dynamic>? ?? [];
    return items
        .map((i) => DownloadHistoryItem.fromJson(i as Map<String, dynamic>))
        .toList();
  }
}
