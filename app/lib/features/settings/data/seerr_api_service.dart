import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';

/// The Seerr-compatible API key: the credential other apps (Maintainerr,
/// Dashbrr, Homepage, anything built against the Seerr API) present to read
/// Cantinarr's request ledger under `/api/v1`. Admin-only. The key acts as
/// the administrator who issued it.
class SeerrApiKey {
  final bool configured;

  /// The key itself, '' until one is issued. Shown to administrators so it
  /// can be pasted into the integrating app, as Radarr and Seerr show theirs.
  final String apiKey;
  final String issuedBy;
  final DateTime? createdAt;

  const SeerrApiKey({
    required this.configured,
    required this.apiKey,
    required this.issuedBy,
    required this.createdAt,
  });

  static const none =
      SeerrApiKey(configured: false, apiKey: '', issuedBy: '', createdAt: null);

  factory SeerrApiKey.fromJson(Map<String, dynamic> json) => SeerrApiKey(
        configured: json['configured'] as bool? ?? false,
        apiKey: json['api_key'] as String? ?? '',
        issuedBy: json['issued_by'] as String? ?? '',
        createdAt: DateTime.tryParse(json['created_at'] as String? ?? ''),
      );
}

class SeerrApiService {
  final Dio _dio;

  SeerrApiService(this._dio);

  Future<SeerrApiKey> get() async {
    final resp = await _dio.get('/api/admin/seerr-api');
    return SeerrApiKey.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Issues a key, replacing any earlier one at once: apps still holding the
  /// old key stop working on their next call.
  Future<SeerrApiKey> issue() async {
    final resp = await _dio.post('/api/admin/seerr-api');
    return SeerrApiKey.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Revokes the key; `/api/v1` answers 401 until a new one is issued.
  Future<SeerrApiKey> revoke() async {
    final resp = await _dio.delete('/api/admin/seerr-api');
    return SeerrApiKey.fromJson(resp.data as Map<String, dynamic>);
  }
}

final seerrApiServiceProvider = Provider<SeerrApiService>(
  (ref) => SeerrApiService(ref.watch(backendClientProvider)),
);
