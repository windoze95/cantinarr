import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import 'config_change_models.dart';

/// Admin-only REST client for Cantinarr's durable connected-app change log.
class ConfigChangesService {
  final Dio _dio;

  ConfigChangesService({required Dio backendDio}) : _dio = backendDio;

  Future<List<ConfigChange>> listChanges({
    int limit = 100,
    int? beforeId,
  }) async {
    final response = await _dio.get(
      '/api/admin/external-settings-changes',
      queryParameters: {
        'limit': limit,
        if (beforeId != null) 'before_id': beforeId,
      },
    );
    final data = response.data as Map<String, dynamic>? ?? const {};
    return ((data['changes'] as List?) ?? const [])
        .whereType<Map>()
        .map((item) => ConfigChange.fromJson(
              item.map((key, value) => MapEntry(key.toString(), value)),
            ))
        .toList(growable: false);
  }

  Future<ConfigChange> getChange(int id) async {
    final response =
        await _dio.get('/api/admin/external-settings-changes/$id');
    return ConfigChange.fromJson(_unwrapChange(response.data));
  }

  /// Requests a server-owned restore. No stored before/after values are sent
  /// back from the device; the backend re-reads live state and owns the replay.
  Future<ConfigChange> revertChange(int id) async {
    final response =
        await _dio.post('/api/admin/external-settings-changes/$id/revert');
    return ConfigChange.fromJson(_unwrapChange(response.data));
  }

  Map<String, dynamic> _unwrapChange(Object? raw) {
    final data = raw as Map<String, dynamic>? ?? const {};
    final nested = data['change'];
    return nested is Map
        ? nested.map((key, value) => MapEntry(key.toString(), value))
        : data;
  }
}

final configChangesServiceProvider = Provider<ConfigChangesService>(
  (ref) => ConfigChangesService(backendDio: ref.watch(backendClientProvider)),
);
