import 'dart:async';
import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import '../config/app_config.dart';

/// Dio interceptor that handles JWT authentication and automatic token refresh.
///
/// On 401 responses, attempts a token refresh via POST /api/auth/refresh.
/// If refresh succeeds, retries the original request with the new token.
/// Only a genuine rejection (the server answers the refresh with a 401) calls
/// [onAuthExpired] to clear auth state. A transport failure (VPN down, server
/// unreachable, timeout) leaves the session intact — the token just couldn't be
/// refreshed right now.
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

    final completer = _refreshCompleter = Completer<bool>();

    try {
      // A dedicated client with explicit timeouts: a hung refresh would
      // otherwise block every queued 401 retry behind the shared completer.
      final dio = Dio(BaseOptions(
        connectTimeout: AppConfig.requestTimeout,
        receiveTimeout: AppConfig.requestTimeout,
      ));
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
        return _settle(completer, true);
      }
      // A non-200 without an exception: don't retry, but don't tear the session
      // down either.
      return _settle(completer, false);
    } on DioException catch (e) {
      // Only a genuine rejection (server reached us and refused the refresh
      // token — a 401) ends the session. Transport failures (VPN down, server
      // unreachable, timeout) must NOT log the user out: the long-lived refresh
      // token is still valid and the next request will retry.
      if (e.response?.statusCode == 401) {
        onAuthExpired();
      } else {
        debugPrint('Token refresh deferred (server unreachable): $e');
      }
      return _settle(completer, false);
    } catch (e) {
      debugPrint('Token refresh failed (session kept): $e');
      return _settle(completer, false);
    }
  }

  /// Completes the in-flight refresh guard with [result] and clears it, so a
  /// later 401 starts a fresh refresh instead of reusing this completed future.
  bool _settle(Completer<bool> completer, bool result) {
    if (!completer.isCompleted) completer.complete(result);
    _refreshCompleter = null;
    return result;
  }
}
