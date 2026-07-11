import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../features/auth/logic/auth_provider.dart';
import '../config/app_config.dart';
import 'backend_auth_interceptor.dart';
import 'safe_http_log_interceptor.dart';

/// Provides an authenticated Dio instance configured for the backend.
///
/// Keyed only on the connection's [serverUrl] (via `select`) so the Dio is
/// rebuilt only when the target server actually changes — not on every
/// [authProvider] emission. The access/refresh tokens are read dynamically by
/// the interceptor below, so token refreshes and the optimistic→validated
/// session handoff on launch reuse the same Dio instead of replacing it. That
/// keeps downstream providers which `watch` this one (discovery rows, search,
/// etc.) from being torn down and silently losing their already-fetched state
/// mid-session.
final backendClientProvider = Provider<Dio>((ref) {
  final serverUrl = ref.watch(
    authProvider.select((s) => s.valueOrNull?.connection?.serverUrl),
  );

  if (serverUrl == null) {
    // Return a no-op Dio that will fail gracefully
    return Dio(BaseOptions(baseUrl: 'http://localhost'));
  }

  final dio = Dio(BaseOptions(
    baseUrl: serverUrl,
    connectTimeout: AppConfig.requestTimeout,
    receiveTimeout: AppConfig.requestTimeout,
    headers: {'Content-Type': 'application/json'},
  ));

  final authNotifier = ref.read(authProvider.notifier);

  dio.interceptors.add(BackendAuthInterceptor(
    getAccessToken: () =>
        ref.read(authProvider).valueOrNull?.connection?.accessToken ?? '',
    getRefreshToken: () =>
        ref.read(authProvider).valueOrNull?.connection?.refreshToken ?? '',
    getServerUrl: () =>
        ref.read(authProvider).valueOrNull?.connection?.serverUrl ?? '',
    onTokenRefreshed: (accessToken, refreshToken) async {
      await authNotifier.updateTokens(accessToken, refreshToken);
    },
    onAuthExpired: () {
      authNotifier.onAuthExpired();
    },
  ));

  if (kDebugMode) {
    dio.interceptors.add(SafeHttpLogInterceptor(logPrint: debugPrint));
  }

  return dio;
});
