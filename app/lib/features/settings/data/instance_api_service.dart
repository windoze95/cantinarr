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

  Future<ServiceInstance> createInstance({
    required String serviceType,
    required String name,
    required String url,
    required String apiKey,
    bool isDefault = false,
  }) async {
    final resp = await _dio.post('/api/instances', data: {
      'service_type': serviceType,
      'name': name,
      'url': url,
      'api_key': apiKey,
      'is_default': isDefault,
    });
    return ServiceInstance.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<ServiceInstance> updateInstance({
    required String id,
    required String name,
    required String url,
    required String apiKey,
    bool isDefault = false,
  }) async {
    final resp = await _dio.put('/api/instances/$id', data: {
      'name': name,
      'url': url,
      'api_key': apiKey,
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
}
