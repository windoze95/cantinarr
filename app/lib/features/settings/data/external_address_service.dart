import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';

/// Admin-only external address: the origin other people's devices use to
/// reach this server. Connect invite links and passkey setup links are built
/// from it; empty means links fall back to the address the generating admin's
/// own app is connected with.
class ExternalAddressService {
  final Dio _dio;

  ExternalAddressService({required Dio backendDio}) : _dio = backendDio;

  Future<String> fetch() async {
    final resp = await _dio.get('/api/admin/external-address');
    final data = resp.data as Map<String, dynamic>;
    return data['external_url'] as String? ?? '';
  }

  /// Sets the external address (empty clears it) and returns the stored
  /// value. Throws on a non-2xx response so callers can surface the error.
  Future<String> set(String url) async {
    final resp = await _dio.put(
      '/api/admin/external-address',
      data: {'external_url': url},
    );
    final data = resp.data as Map<String, dynamic>;
    return data['external_url'] as String? ?? '';
  }
}

final externalAddressServiceProvider = Provider<ExternalAddressService>(
  (ref) => ExternalAddressService(backendDio: ref.watch(backendClientProvider)),
);
