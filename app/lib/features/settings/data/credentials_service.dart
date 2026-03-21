import 'package:dio/dio.dart';

/// API service for admin credential management (write-only).
class CredentialsService {
  final Dio _dio;

  CredentialsService({required Dio backendDio}) : _dio = backendDio;

  /// Returns which credentials are configured (booleans, never values).
  Future<Map<String, bool>> getStatus() async {
    final resp = await _dio.get('/api/admin/credentials');
    return (resp.data as Map<String, dynamic>).map(
      (k, v) => MapEntry(k, v as bool),
    );
  }

  /// Updates one or more credentials. Only non-empty values are written.
  Future<void> update(Map<String, String> credentials) async {
    await _dio.put('/api/admin/credentials', data: credentials);
  }

  /// Removes a single credential.
  Future<void> delete(String key) async {
    await _dio.delete('/api/admin/credentials/$key');
  }
}
