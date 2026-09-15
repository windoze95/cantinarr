import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/network/long_request_options.dart';

/// Admin-only outbound proxy: where the server sends its own internet
/// traffic (TMDB, Trakt, hosted AI providers, plex.tv, the update check, the
/// push relay). Arr instances, download clients, media servers, and a local
/// AI endpoint always connect directly. The password is write-only: the
/// server only ever says whether one is stored.
class OutboundProxySettings {
  /// `scheme://host:port` only, never carrying credentials; '' when unset.
  final String url;
  final String username;
  final bool hasPassword;

  const OutboundProxySettings({
    required this.url,
    required this.username,
    required this.hasPassword,
  });

  static const empty =
      OutboundProxySettings(url: '', username: '', hasPassword: false);

  /// Missing keys read as unset, so an older server (or a stub answering
  /// `{}`) means "no proxy" rather than a parse failure.
  factory OutboundProxySettings.fromJson(Map<String, dynamic> json) =>
      OutboundProxySettings(
        url: json['url'] as String? ?? '',
        username: json['username'] as String? ?? '',
        hasPassword: json['has_password'] as bool? ?? false,
      );
}

class OutboundProxyService {
  final Dio _dio;

  OutboundProxyService({required Dio backendDio}) : _dio = backendDio;

  Future<OutboundProxySettings> fetch() async {
    final resp = await _dio.get('/api/admin/outbound-proxy');
    return OutboundProxySettings.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Stores the proxy and returns what the server kept. An empty url clears
  /// everything; a blank password keeps the stored one when the username is
  /// unchanged (the server decides, the client sends what was typed). Throws
  /// on a non-2xx response so callers can surface the reason.
  Future<OutboundProxySettings> set({
    required String url,
    required String username,
    required String password,
  }) async {
    final resp = await _dio.put(
      '/api/admin/outbound-proxy',
      data: {'url': url, 'username': username, 'password': password},
    );
    return OutboundProxySettings.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Asks the server to reach TMDB through the given proxy without storing
  /// anything. Resolves on success; a non-2xx reply throws the DioException
  /// so the caller can show the server's reason. A dead proxy is found out
  /// by its dial timing out, so the wait is bounded above the base default.
  Future<void> test({
    required String url,
    required String username,
    required String password,
  }) async {
    await _dio.post(
      '/api/admin/outbound-proxy/test',
      data: {'url': url, 'username': username, 'password': password},
      options: longRequestOptions(timeout: const Duration(seconds: 30)),
    );
  }
}

final outboundProxyServiceProvider = Provider<OutboundProxyService>(
  (ref) => OutboundProxyService(backendDio: ref.watch(backendClientProvider)),
);
