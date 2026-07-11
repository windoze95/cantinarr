import 'package:dio/dio.dart';
import '../../../core/models/backend_connection.dart';

/// Calls the backend instance CRUD API endpoints.
class InstanceApiService {
  final Dio _dio;

  InstanceApiService({required Dio backendDio}) : _dio = backendDio;

  Future<List<ServiceInstance>> listInstances() async {
    final resp = await _dio.get('/api/instances');
    return (resp.data as List<dynamic>)
        .map((i) => ServiceInstance.fromJson(i as Map<String, dynamic>))
        .toList();
  }

  /// Fetch full details (url, username, ...) for one instance.
  /// The list endpoint is the only read endpoint; credentials are write-only.
  Future<Map<String, dynamic>?> getInstanceDetails(String id) async {
    final resp = await _dio.get('/api/instances');
    for (final inst in (resp.data as List<dynamic>)) {
      final map = inst as Map<String, dynamic>;
      if (map['id'] == id) return map;
    }
    return null;
  }

  Future<ServiceInstance> createInstance({
    required String serviceType,
    required String name,
    required String url,
    String apiKey = '',
    String username = '',
    String password = '',
    bool isDefault = false,
  }) async {
    final resp = await _dio.post('/api/instances', data: {
      'service_type': serviceType,
      'name': name,
      'url': url,
      'api_key': apiKey,
      'username': username,
      'password': password,
      'is_default': isDefault,
    });
    return ServiceInstance.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<ServiceInstance> updateInstance({
    required String id,
    required String name,
    required String url,
    String apiKey = '',
    String username = '',
    String password = '',
    bool isDefault = false,
  }) async {
    final resp = await _dio.put('/api/instances/$id', data: {
      'name': name,
      'url': url,
      'api_key': apiKey,
      'username': username,
      'password': password,
      'is_default': isDefault,
    });
    return ServiceInstance.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<void> deleteInstance(String id) async {
    await _dio.delete('/api/instances/$id');
  }

  /// Create or refresh Cantinarr's server-managed Radarr/Sonarr Connect
  /// webhook. Its callback credential remains entirely server-side.
  Future<void> configureWebhook(String id) async {
    await _dio.post('/api/instances/$id/webhook');
  }

  /// Per-user default pins for this instance's service type, keyed by user id.
  /// The pinned id may be a sibling instance of the same type, so the edit
  /// screen can show who is currently assigned where.
  Future<Map<int, String>> getInstanceUsers(String id) async {
    final resp = await _dio.get('/api/instances/$id/users');
    final pins = <int, String>{};
    for (final row in (resp.data as List<dynamic>? ?? const [])) {
      final map = row as Map<String, dynamic>;
      pins[(map['user_id'] as num).toInt()] = map['instance_id'] as String;
    }
    return pins;
  }

  /// Pin this instance as the per-user default for exactly [userIds]: listed
  /// users are pinned here (moving off a sibling instance if needed); users
  /// previously pinned here but not listed revert to the global default (for
  /// Chaptarr, they lose access).
  Future<void> updateInstanceUsers(String id, List<int> userIds) async {
    await _dio.put('/api/instances/$id/users', data: {'user_ids': userIds});
  }

  /// Test connection to a service URL.
  Future<bool> testConnection(String url, String apiKey) async {
    try {
      final testDio = Dio(BaseOptions(
        connectTimeout: const Duration(seconds: 10),
        receiveTimeout: const Duration(seconds: 10),
      ));
      final resp = await testDio.get(
        '$url/api/v3/system/status',
        options: Options(headers: {'X-Api-Key': apiKey}),
      );
      return resp.statusCode == 200;
    } catch (_) {
      return false;
    }
  }
}
