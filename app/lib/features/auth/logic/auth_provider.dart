import 'dart:io';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/models/user_profile.dart';
import '../../../core/storage/secure_storage.dart';
import '../data/auth_service.dart';

/// The authentication state exposed to the rest of the app.
class AuthState {
  final BackendConnection? connection;
  final UserProfile? user;
  final bool isLoading;
  final String? error;

  const AuthState({
    this.connection,
    this.user,
    this.isLoading = false,
    this.error,
  });

  bool get isAuthenticated => connection != null && user != null;

  AuthState copyWith({
    BackendConnection? connection,
    UserProfile? user,
    bool? isLoading,
    String? error,
    bool clearConnection = false,
    bool clearUser = false,
    bool clearError = false,
  }) =>
      AuthState(
        connection: clearConnection ? null : (connection ?? this.connection),
        user: clearUser ? null : (user ?? this.user),
        isLoading: isLoading ?? this.isLoading,
        error: clearError ? null : (error ?? this.error),
      );
}

/// Manages authentication lifecycle: login, connect token, token refresh.
class AuthNotifier extends AsyncNotifier<AuthState> {
  late final AuthService _authService;
  late final FlutterSecureStorage _storage;

  @override
  Future<AuthState> build() async {
    _authService = AuthService();
    _storage = ref.read(secureStorageProvider);

    // Try to restore session from secure storage
    return _tryRestoreSession();
  }

  Future<AuthState> _tryRestoreSession() async {
    final serverUrl = await _storage.read(key: StorageKeys.serverUrl);
    final accessToken = await _storage.read(key: StorageKeys.jwt);
    final refreshToken = await _storage.read(key: StorageKeys.refreshToken);
    final deviceId = await _storage.read(key: StorageKeys.deviceId);

    if (serverUrl == null || accessToken == null || refreshToken == null) {
      return const AuthState();
    }

    try {
      // Try refreshing the token
      final authResp = await _authService.refreshToken(serverUrl, refreshToken);
      final config =
          await _authService.fetchConfig(serverUrl, authResp.accessToken);

      // Persist new tokens
      await _saveTokens(
        serverUrl,
        authResp.accessToken,
        authResp.refreshToken,
        authResp.deviceId ?? deviceId,
      );

      final connection = BackendConnection(
        serverUrl: serverUrl,
        accessToken: authResp.accessToken,
        refreshToken: authResp.refreshToken,
        serverName: config.serverName,
        services: config.services,
        instances: config.instances,
      );

      return AuthState(connection: connection, user: authResp.user);
    } catch (e) {
      debugPrint('Session restore failed: $e');
      await _clearStorage();
      return const AuthState();
    }
  }

  /// Log in with server URL, username, and password (admin bootstrap).
  Future<void> login(
      String serverUrl, String username, String password) async {
    state = const AsyncData(AuthState(isLoading: true));

    try {
      final normalizedUrl = _normalizeUrl(serverUrl);
      final authResp =
          await _authService.login(normalizedUrl, username, password);
      final config =
          await _authService.fetchConfig(normalizedUrl, authResp.accessToken);

      await _saveTokens(
        normalizedUrl,
        authResp.accessToken,
        authResp.refreshToken,
        authResp.deviceId,
      );

      final connection = BackendConnection(
        serverUrl: normalizedUrl,
        accessToken: authResp.accessToken,
        refreshToken: authResp.refreshToken,
        serverName: config.serverName,
        services: config.services,
        instances: config.instances,
      );

      state = AsyncData(
          AuthState(connection: connection, user: authResp.user));
    } catch (e) {
      state = AsyncData(AuthState(error: _parseError(e)));
    }
  }

  /// Connect using a connect token (from deep link or paste).
  Future<void> connectWithToken(String serverUrl, String token) async {
    state = const AsyncData(AuthState(isLoading: true));

    try {
      final normalizedUrl = _normalizeUrl(serverUrl);
      final deviceName = _getDeviceName();
      final authResp = await _authService.redeemConnectToken(
          normalizedUrl, token, deviceName);
      final config =
          await _authService.fetchConfig(normalizedUrl, authResp.accessToken);

      await _saveTokens(
        normalizedUrl,
        authResp.accessToken,
        authResp.refreshToken,
        authResp.deviceId,
      );

      final connection = BackendConnection(
        serverUrl: normalizedUrl,
        accessToken: authResp.accessToken,
        refreshToken: authResp.refreshToken,
        serverName: config.serverName,
        services: config.services,
        instances: config.instances,
      );

      state = AsyncData(
          AuthState(connection: connection, user: authResp.user));
    } catch (e) {
      state = AsyncData(AuthState(error: _parseConnectError(e)));
    }
  }

  /// Handle a cantinarr:// deep link. If already authenticated, ignores it.
  Future<void> connectWithLink(String link) async {
    final current = state.valueOrNull;
    if (current?.isAuthenticated == true) return;

    final uri = Uri.tryParse(link);
    if (uri == null) return;

    final token = uri.queryParameters['token'];
    final server = uri.queryParameters['server'];
    if (token == null || server == null) return;

    await connectWithToken(server, token);
  }

  /// Generate a connect link for a new user (admin only).
  Future<ConnectTokenResponse> generateConnectToken(String name) async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');
    return _authService.generateConnectToken(
      conn.serverUrl,
      conn.accessToken,
      name,
      conn.serverUrl,
    );
  }

  /// List all connected devices (admin only).
  Future<List<DeviceInfo>> listDevices() async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');
    return _authService.listDevices(conn.serverUrl, conn.accessToken);
  }

  /// Revoke a device (admin only).
  Future<void> revokeDevice(String deviceId) async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');
    await _authService.revokeDevice(conn.serverUrl, conn.accessToken, deviceId);
  }

  /// Update tokens after a refresh (called by the auth interceptor).
  Future<void> updateTokens(
      String accessToken, String refreshToken) async {
    final current = state.valueOrNull;
    if (current?.connection == null) return;

    final updated = current!.connection!.copyWith(
      accessToken: accessToken,
      refreshToken: refreshToken,
    );

    await _storage.write(key: StorageKeys.jwt, value: accessToken);
    await _storage.write(key: StorageKeys.refreshToken, value: refreshToken);

    state = AsyncData(current.copyWith(connection: updated));
  }

  /// Called when auth has expired (refresh rejected). Clears state.
  Future<void> onAuthExpired() async {
    await _clearStorage();
    state = const AsyncData(AuthState());
  }

  void clearError() {
    final current = state.valueOrNull;
    if (current != null) {
      state = AsyncData(current.copyWith(clearError: true));
    }
  }

  // ─── Helpers ─────────────────────────────────────────

  Future<void> _saveTokens(
    String serverUrl,
    String accessToken,
    String refreshToken,
    String? deviceId,
  ) async {
    await _storage.write(key: StorageKeys.serverUrl, value: serverUrl);
    await _storage.write(key: StorageKeys.jwt, value: accessToken);
    await _storage.write(key: StorageKeys.refreshToken, value: refreshToken);
    if (deviceId != null) {
      await _storage.write(key: StorageKeys.deviceId, value: deviceId);
    }
  }

  Future<void> _clearStorage() async {
    await _storage.delete(key: StorageKeys.serverUrl);
    await _storage.delete(key: StorageKeys.jwt);
    await _storage.delete(key: StorageKeys.refreshToken);
    await _storage.delete(key: StorageKeys.deviceId);
  }

  String _normalizeUrl(String url) {
    var normalized = url.trim();
    if (!normalized.startsWith('http://') &&
        !normalized.startsWith('https://')) {
      normalized = 'https://$normalized';
    }
    if (normalized.endsWith('/')) {
      normalized = normalized.substring(0, normalized.length - 1);
    }
    return normalized;
  }

  String _getDeviceName() {
    try {
      if (Platform.isIOS) return 'iPhone';
      if (Platform.isAndroid) return 'Android';
      if (Platform.isMacOS) return 'Mac';
      if (Platform.isWindows) return 'Windows';
      if (Platform.isLinux) return 'Linux';
    } catch (_) {}
    return 'Unknown Device';
  }

  String _parseError(Object e) {
    if (e is DioException) {
      final statusCode = e.response?.statusCode;
      if (statusCode == 401) return 'Invalid username or password';
      if (statusCode == 404) return 'Server not found at this URL';
      if (e.type == DioExceptionType.connectionError ||
          e.type == DioExceptionType.connectionTimeout) {
        return 'Could not connect to server';
      }
      if (statusCode == 409) return 'Username already taken';
      if (statusCode == 400) return 'Invalid invite code';
    }
    if (e is Exception) {
      final msg = e.toString();
      if (msg.contains('401')) return 'Invalid username or password';
      if (msg.contains('404')) return 'Server not found at this URL';
      if (msg.contains('Connection refused') ||
          msg.contains('SocketException')) {
        return 'Could not connect to server';
      }
    }
    return 'Connection failed. Please check the server URL.';
  }

  String _parseConnectError(Object e) {
    if (e is DioException) {
      final data = e.response?.data;
      if (data is Map<String, dynamic>) {
        final error = data['error'] as String?;
        if (error != null) return error;
      }
      if (e.type == DioExceptionType.connectionError ||
          e.type == DioExceptionType.connectionTimeout) {
        return 'Could not connect to server';
      }
    }
    return 'Connection failed. The link may be invalid or expired.';
  }
}

/// The main auth state provider used throughout the app.
final authProvider =
    AsyncNotifierProvider<AuthNotifier, AuthState>(AuthNotifier.new);
