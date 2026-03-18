import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../features/auth/logic/auth_provider.dart';
import '../config/app_config.dart';
import 'backend_auth_interceptor.dart';

/// Provides an authenticated Dio instance configured for the backend.
final backendClientProvider = Provider<Dio>((ref) {
  final authState = ref.watch(authProvider);
  final connection = authState.valueOrNull?.connection;

  if (connection == null) {
    // Return a no-op Dio that will fail gracefully
    return Dio(BaseOptions(baseUrl: 'http://localhost'));
  }

  final dio = Dio(BaseOptions(
    baseUrl: connection.serverUrl,
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
    dio.interceptors.add(LogInterceptor(
      requestBody: true,
      responseBody: true,
      logPrint: (obj) => debugPrint(obj.toString()),
    ));
  }

  return dio;
});
