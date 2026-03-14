import 'dart:async';
import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';

/// Dio interceptor that handles JWT authentication and automatic token refresh.
///
/// On 401 responses, attempts a token refresh via POST /api/auth/refresh.
/// If refresh succeeds, retries the original request with the new token.
/// If refresh fails, calls [onAuthExpired] to clear auth state.
class BackendAuthInterceptor extends Interceptor {
  final String Function() getAccessToken;
  final String Function() getRefreshToken;
  final String Function() getServerUrl;
  final Future<void> Function(String accessToken, String refreshToken)
      onTokenRefreshed;
  final VoidCallback onAuthExpired;

  /// Guards against concurrent refresh attempts.
  Completer<bool>? _refreshCompleter;

  BackendAuthInterceptor({
    required this.getAccessToken,
    required this.getRefreshToken,
    required this.getServerUrl,
    required this.onTokenRefreshed,
    required this.onAuthExpired,
  });

  @override
  void onRequest(RequestOptions options, RequestInterceptorHandler handler) {
    final token = getAccessToken();
    if (token.isNotEmpty) {
      options.headers['Authorization'] = 'Bearer $token';
    }
    handler.next(options);
  }

  @override
  void onError(DioException err, ErrorInterceptorHandler handler) async {
    if (err.response?.statusCode != 401) {
      handler.next(err);
      return;
    }

    // Avoid refreshing for the refresh endpoint itself
    if (err.requestOptions.path.contains('/api/auth/refresh')) {
      onAuthExpired();
      handler.next(err);
      return;
    }

    final refreshed = await _tryRefresh();
    if (!refreshed) {
      handler.next(err);
      return;
    }

    // Retry original request with new token
    try {
      final opts = err.requestOptions;
      opts.headers['Authorization'] = 'Bearer ${getAccessToken()}';
      final dio = Dio();
      final response = await dio.fetch(opts);
      handler.resolve(response);
    } catch (retryErr) {
      handler.next(retryErr is DioException
          ? retryErr
          : DioException(requestOptions: err.requestOptions, error: retryErr));
    }
  }

  Future<bool> _tryRefresh() async {
    // If already refreshing, wait for the result
    if (_refreshCompleter != null) {
      return _refreshCompleter!.future;
    }

    _refreshCompleter = Completer<bool>();

    try {
      final dio = Dio();
      final serverUrl = getServerUrl();
      final refreshToken = getRefreshToken();

      final resp = await dio.post(
        '$serverUrl/api/auth/refresh',
        data: {'refresh_token': refreshToken},
        options: Options(headers: {'Content-Type': 'application/json'}),
      );

      if (resp.statusCode == 200) {
        final data = resp.data as Map<String, dynamic>;
        final newAccess = data['access_token'] as String;
        final newRefresh = data['refresh_token'] as String? ?? refreshToken;
        await onTokenRefreshed(newAccess, newRefresh);
        _refreshCompleter!.complete(true);
        return true;
      }
    } catch (e) {
      debugPrint('Token refresh failed: $e');
    }

    onAuthExpired();
    _refreshCompleter!.complete(false);
    _refreshCompleter = null;
    return false;
  }
}
