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

  /// Verifies Chaptarr cover fetching through the backend: it performs the same
  /// forms login it uses for covers AND samples a real cover, so [coverOk]
  /// reflects whether covers actually load (Chaptarr's cover proxy can fail even
  /// when login works). Pass [instanceId] when editing so blank secrets fall
  /// back to the stored ones.
  Future<({bool success, String? error, bool coverOk, String? coverDetail})>
      testWebLogin({
    required String url,
    required String username,
    required String password,
    String apiKey = '',
    String? instanceId,
  }) async {
    try {
      final resp = await _dio.post('/api/instances/test-web-login', data: {
        'url': url,
        'username': username,
        'password': password,
        'api_key': apiKey,
        if (instanceId != null) 'instance_id': instanceId,
      });
      final data = resp.data as Map<String, dynamic>?;
      final err = data?['error'] as String?;
      final detail = data?['cover_detail'] as String?;
      return (
        success: data?['success'] == true,
        error: (err != null && err.isNotEmpty) ? err : null,
        coverOk: data?['cover_ok'] != false, // true unless explicitly false
        coverDetail: (detail != null && detail.isNotEmpty) ? detail : null,
      );
    } catch (_) {
      return (
        success: false,
        error: 'Could not reach the server',
        coverOk: false,
        coverDetail: null,
      );
    }
  }
}
