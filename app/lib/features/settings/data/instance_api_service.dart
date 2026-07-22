import 'package:dio/dio.dart';
import '../../../core/models/backend_connection.dart';

/// Maps the path reported by one arr instance to the corresponding read-only
/// path mounted inside the Cantinarr server.
class MediaPathMapping {
  final String arrPath;
  final String cantinarrPath;

  const MediaPathMapping({
    required this.arrPath,
    required this.cantinarrPath,
  });

  factory MediaPathMapping.fromJson(Map<String, dynamic> json) =>
      MediaPathMapping(
        arrPath: json['arr_path'] as String? ?? '',
        cantinarrPath: json['cantinarr_path'] as String? ?? '',
      );

  Map<String, dynamic> toJson() => {
        'arr_path': arrPath,
        'cantinarr_path': cantinarrPath,
      };
}

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

  /// Absolute filesystem roots the server operator has explicitly allowed for
  /// completed-media delivery. This endpoint is admin-only. Unsupported/404
  /// responses intentionally propagate so older servers remain write-safe.
  Future<List<String>> listMediaRoots() async {
    final resp = await _dio.get('/api/instances/media-roots');
    return (resp.data as List<dynamic>)
        .map((root) => root as String)
        .toList(growable: false);
  }

  Future<ServiceInstance> createInstance({
    required String serviceType,
    required String name,
    required String url,
    String apiKey = '',
    String username = '',
    String password = '',
    bool isDefault = false,
    List<MediaPathMapping>? mediaPathMappings,
  }) async {
    final resp = await _dio.post('/api/instances', data: {
      'service_type': serviceType,
      'name': name,
      'url': url,
      'api_key': apiKey,
      'username': username,
      'password': password,
      'is_default': isDefault,
      if (mediaPathMappings != null)
        'media_path_mappings':
            mediaPathMappings.map((mapping) => mapping.toJson()).toList(),
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
    List<MediaPathMapping>? mediaPathMappings,
  }) async {
    final resp = await _dio.put('/api/instances/$id', data: {
      'name': name,
      'url': url,
      'api_key': apiKey,
      'username': username,
      'password': password,
      'is_default': isDefault,
      if (mediaPathMappings != null)
        'media_path_mappings':
            mediaPathMappings.map((mapping) => mapping.toJson()).toList(),
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

  /// Ask the server to test a candidate instance configuration. The server is
  /// what dials instance URLs in production, so cluster-internal names this
  /// device cannot resolve (e.g. http://radarr:7878) still test truthfully —
  /// and credentials never leave the backend boundary. When [id] is set,
  /// blank credentials fall back to the stored write-only ones, matching
  /// save's semantics. Throws on failure with the server's reason.
  Future<void> testConnection({
    String? id,
    required String serviceType,
    required String url,
    String apiKey = '',
    String username = '',
    String password = '',
  }) async {
    await _dio.post('/api/instances/test', data: {
      if (id != null) 'id': id,
      'service_type': serviceType,
      'url': url,
      'api_key': apiKey,
      'username': username,
      'password': password,
    });
  }
}
