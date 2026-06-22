import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/models/user_profile.dart';
import '../../../core/storage/secure_storage.dart';
import '../../notifications/push_service.dart';
import '../data/auth_service.dart';
import '../data/passkey_service.dart';
import '../data/server_status.dart';

/// The authentication state exposed to the rest of the app.
class AuthState {
  final BackendConnection? connection;
  final UserProfile? user;
  final bool isLoading;
  final String? error;
  final bool pendingPasskeyOffer;

  /// True when the session was restored optimistically from cache and has not
  /// yet been (re)validated with the server — i.e. we're "reconnecting". The
  /// user stays in the app; the UI shows a subtle reconnecting indicator.
  final bool isReconnecting;

  const AuthState({
    this.connection,
    this.user,
    this.isLoading = false,
    this.error,
    this.pendingPasskeyOffer = false,
    this.isReconnecting = false,
  });

  bool get isAuthenticated => connection != null && user != null;

  AuthState copyWith({
    BackendConnection? connection,
    UserProfile? user,
    bool? isLoading,
    String? error,
    bool? pendingPasskeyOffer,
    bool? isReconnecting,
    bool clearConnection = false,
    bool clearUser = false,
    bool clearError = false,
  }) =>
      AuthState(
        connection: clearConnection ? null : (connection ?? this.connection),
        user: clearUser ? null : (user ?? this.user),
        isLoading: isLoading ?? this.isLoading,
        error: clearError ? null : (error ?? this.error),
        pendingPasskeyOffer: pendingPasskeyOffer ?? this.pendingPasskeyOffer,
        isReconnecting: isReconnecting ?? this.isReconnecting,
      );
}

/// Manages authentication lifecycle: login, connect token, token refresh.
class AuthNotifier extends AsyncNotifier<AuthState> {
  late final AuthService _authService;
  late final StorageService _storage;

  /// Periodic retry while holding an optimistic (reconnecting) session.
  Timer? _reconnectTimer;

  /// Guards against overlapping background validations.
  bool _validating = false;

  @override
  Future<AuthState> build() async {
    _authService = ref.read(authServiceProvider);
    _storage = ref.read(storageServiceProvider);

    ref.onDispose(() {
      _reconnectTimer?.cancel();
      _reconnectTimer = null;
    });

    // Try to restore session from secure storage
    return _tryRestoreSession();
  }

  Future<AuthState> _tryRestoreSession() async {
    final serverUrl = await _storage.read(key: StorageKeys.serverUrl);
    final accessToken = await _storage.read(key: StorageKeys.jwt);
    final refreshToken = await _storage.read(key: StorageKeys.refreshToken);

    if (serverUrl == null || accessToken == null || refreshToken == null) {
      return const AuthState();
    }

    // If a session snapshot is cached, open straight into an optimistic,
    // authenticated session and validate it in the background — the app never
    // flashes the login screen on a slow or offline launch. The seamless path.
    final cached = await _cachedSession(serverUrl, accessToken, refreshToken);
    if (cached != null) {
      unawaited(_validateSession());
      return cached;
    }

    // No snapshot yet (first launch after install, or a pre-feature session):
    // validate inline, which also writes the snapshot for next time.
    return _restoreInline(serverUrl, accessToken, refreshToken);
  }

  /// Builds an optimistic [AuthState] from the cached session snapshot plus the
  /// stored tokens, or null when no snapshot is cached. The access token may be
  /// stale; [_validateSession] refreshes it. Marked reconnecting until then.
  Future<AuthState?> _cachedSession(
    String serverUrl,
    String accessToken,
    String refreshToken,
  ) async {
    final userJson = await _storage.read(key: StorageKeys.sessionUser);
    final connJson = await _storage.read(key: StorageKeys.sessionConnection);
    if (userJson == null || connJson == null) return null;
    try {
      final user =
          UserProfile.fromJson(jsonDecode(userJson) as Map<String, dynamic>);
      final meta = jsonDecode(connJson) as Map<String, dynamic>;
      final services = meta['services'];
      final connection = BackendConnection(
        serverUrl: serverUrl,
        accessToken: accessToken,
        refreshToken: refreshToken,
        serverName: meta['server_name'] as String?,
        services: services is Map<String, dynamic>
            ? AvailableServices.fromJson(services)
            : const AvailableServices(),
        instances: (meta['instances'] as List<dynamic>?)
                ?.map((e) =>
                    ServiceInstance.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
      );
      return AuthState(
          connection: connection, user: user, isReconnecting: true);
    } catch (e) {
      debugPrint('Cached session decode failed: $e');
      return null;
    }
  }

  /// Validates the restored session against the server and reconciles state:
  /// fresh data on success, login on a genuine 401, or a "reconnecting" hold
  /// (with a retry scheduled) on a transport failure. Safe to call repeatedly.
  Future<void> _validateSession() async {
    if (_validating) return;
    _validating = true;
    try {
      final serverUrl = await _storage.read(key: StorageKeys.serverUrl);
      final refreshToken = await _storage.read(key: StorageKeys.refreshToken);
      final deviceId = await _storage.read(key: StorageKeys.deviceId);
      if (serverUrl == null || refreshToken == null) return;

      // Refresh first, persist immediately (the refresh token is single-use),
      // then fetch config.
      final authResp = await _authService.refreshToken(serverUrl, refreshToken);
      await _saveTokens(serverUrl, authResp.accessToken, authResp.refreshToken,
          authResp.deviceId ?? deviceId);
      final config =
          await _authService.fetchConfig(serverUrl, authResp.accessToken);
      final connection = BackendConnection(
        serverUrl: serverUrl,
        accessToken: authResp.accessToken,
        refreshToken: authResp.refreshToken,
        serverName: config.serverName,
        services: config.services,
        instances: config.instances,
      );
      await _persistSession(connection, authResp.user);
      _stopReconnect();
      state = AsyncData(AuthState(connection: connection, user: authResp.user));
      _registerForPush();
    } on DioException catch (e) {
      if (e.response?.statusCode == 401) {
        // The server rejected the refresh token: the session is truly dead.
        _stopReconnect();
        await _clearStorage();
        state = const AsyncData(AuthState());
      } else {
        // Server unreachable: keep the user in the app and keep retrying.
        debugPrint('Session validation deferred (server unreachable): $e');
        _markReconnecting();
      }
    } catch (e) {
      debugPrint('Session validation error (staying optimistic): $e');
      _markReconnecting();
    } finally {
      _validating = false;
    }
  }

  /// No-snapshot fallback: validate the stored tokens inline and return the
  /// resulting state. Keeps tokens on a transport failure (only a real 401
  /// clears them) and writes a session snapshot on success.
  Future<AuthState> _restoreInline(
    String serverUrl,
    String accessToken,
    String refreshToken,
  ) async {
    final deviceId = await _storage.read(key: StorageKeys.deviceId);
    try {
      final authResp = await _authService.refreshToken(serverUrl, refreshToken);
      await _saveTokens(serverUrl, authResp.accessToken, authResp.refreshToken,
          authResp.deviceId ?? deviceId);
      final config =
          await _authService.fetchConfig(serverUrl, authResp.accessToken);
      final connection = BackendConnection(
        serverUrl: serverUrl,
        accessToken: authResp.accessToken,
        refreshToken: authResp.refreshToken,
        serverName: config.serverName,
        services: config.services,
        instances: config.instances,
      );
      await _persistSession(connection, authResp.user);
      _registerForPush();
      return AuthState(connection: connection, user: authResp.user);
    } on DioException catch (e) {
      // Only a genuine 401 clears the session; a transport failure keeps the
      // tokens so the next launch can restore. (Without a snapshot we can't show
      // an optimistic session yet, so this lands on login until connectivity
      // returns — the snapshot written on the first success fixes that.)
      if (e.response?.statusCode == 401) {
        debugPrint('Session restore rejected by server (401); clearing.');
        await _clearStorage();
      } else {
        debugPrint('Session restore deferred (server unreachable): $e');
      }
      return const AuthState();
    } catch (e) {
      debugPrint('Session restore error (tokens kept): $e');
      return const AuthState();
    }
  }

  /// Flags the current (optimistic) session as reconnecting and starts the
  /// retry loop. No-op once the session is gone.
  void _markReconnecting() {
    final current = state.valueOrNull;
    if (current == null || !current.isAuthenticated) return;
    if (!current.isReconnecting) {
      state = AsyncData(current.copyWith(isReconnecting: true));
    }
    _scheduleReconnect();
  }

  void _scheduleReconnect() {
    if (_reconnectTimer != null) return;
    _reconnectTimer = Timer.periodic(const Duration(seconds: 8), (_) {
      unawaited(_validateSession());
    });
  }

  void _stopReconnect() {
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
  }

  /// Retry validation now (e.g. when the app returns to the foreground) instead
  /// of waiting for the periodic timer. No-op unless we're holding a
  /// reconnecting session.
  void reconnectNow() {
    final current = state.valueOrNull;
    if (current != null && current.isAuthenticated && current.isReconnecting) {
      unawaited(_validateSession());
    }
  }

  /// Check server status (needs setup, webauthn available).
  Future<ServerStatus> checkServer(String serverUrl) async {
    final normalizedUrl = _normalizeUrl(serverUrl);
    return _authService.getServerStatus(normalizedUrl);
  }

  /// Create admin account during first-run setup.
  Future<void> setup(String serverUrl, String username, String password) async {
    state = const AsyncData(AuthState(isLoading: true));

    try {
      final normalizedUrl = _normalizeUrl(serverUrl);
      final authResp =
          await _authService.setup(normalizedUrl, username, password);
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

      await _persistSession(connection, authResp.user);
      state = AsyncData(AuthState(
        connection: connection,
        user: authResp.user,
        pendingPasskeyOffer: await _shouldOfferPasskey(normalizedUrl),
      ));
      _registerForPush();
    } catch (e) {
      state = AsyncData(AuthState(error: _parseSetupError(e)));
    }
  }

  /// Dismiss the post-setup passkey offer, allowing redirect to dashboard.
  void dismissPasskeyOffer() {
    final current = state.valueOrNull;
    if (current != null) {
      state = AsyncData(current.copyWith(pendingPasskeyOffer: false));
    }
  }

  /// Log in with server URL, username, and password (admin bootstrap).
  Future<void> login(String serverUrl, String username, String password) async {
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

      final offerPasskey =
          authResp.user.isAdmin && await _shouldOfferPasskey(normalizedUrl);

      await _persistSession(connection, authResp.user);
      state = AsyncData(AuthState(
        connection: connection,
        user: authResp.user,
        pendingPasskeyOffer: offerPasskey,
      ));
      _registerForPush();
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

      await _persistSession(connection, authResp.user);
      state = AsyncData(AuthState(connection: connection, user: authResp.user));
      _registerForPush();
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

  /// Re-fetch /api/config and update the connection state (e.g. after
  /// changing API credentials so service availability is reflected).
  Future<void> refreshConfig() async {
    final current = state.valueOrNull;
    if (current?.connection == null) return;
    final conn = current!.connection!;
    final config =
        await _authService.fetchConfig(conn.serverUrl, conn.accessToken);
    final updatedConn = conn.copyWith(
      serverName: config.serverName,
      services: config.services,
      instances: config.instances,
    );
    final user = current.user;
    if (user != null) await _persistSession(updatedConn, user);
    state = AsyncData(current.copyWith(connection: updatedConn));
  }

  /// Re-fetch the current user's profile (e.g. to learn whether a password is
  /// set) and update state.
  Future<void> refreshUser() async {
    final current = state.valueOrNull;
    final conn = current?.connection;
    if (current == null || conn == null) return;
    try {
      final user = await _authService.fetchMe(conn.serverUrl, conn.accessToken);
      await _persistSession(conn, user);
      state = AsyncData(current.copyWith(user: user));
    } catch (e) {
      debugPrint('refreshUser failed: $e');
    }
  }

  /// Create or replace the current user's password. A password enables
  /// username/password sign-in — and MCP client authorization — on servers
  /// without HTTPS, where passkeys are unavailable.
  Future<void> setPassword(String newPassword) async {
    final current = state.valueOrNull;
    final conn = current?.connection;
    if (current == null || conn == null) throw Exception('Not authenticated');
    await _authService.setPassword(
      conn.serverUrl,
      conn.accessToken,
      newPassword,
    );
    final user = current.user;
    if (user != null) {
      state = AsyncData(
        current.copyWith(user: user.copyWith(hasPassword: true)),
      );
    }
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

  /// List all user accounts (admin only).
  Future<List<UserSummary>> listUsers() async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');
    return _authService.listUsers(conn.serverUrl, conn.accessToken);
  }

  /// Change a user's role (admin only).
  Future<UserSummary> updateUserRole(int userId, String role) async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');
    return _authService.updateUserRole(
        conn.serverUrl, conn.accessToken, userId, role);
  }

  /// Delete a user account (admin only).
  Future<void> deleteUser(int userId) async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');
    await _authService.deleteUser(conn.serverUrl, conn.accessToken, userId);
  }

  /// Enable or disable a user's password / passkey sign-in (admin only).
  Future<UserSummary> updateUserAuthMethods(
    int userId, {
    bool? passwordEnabled,
    bool? passkeyEnabled,
  }) async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');
    return _authService.updateUserAuthMethods(
      conn.serverUrl,
      conn.accessToken,
      userId,
      passwordEnabled: passwordEnabled,
      passkeyEnabled: passkeyEnabled,
    );
  }

  // ─── Passkey Methods ─────────────────────────────────

  /// Register a new passkey for the current user.
  Future<void> registerPasskey(String name) async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');

    // Step 1: Begin registration on server
    final beginResp = await _authService.beginPasskeyRegistration(
        conn.serverUrl, conn.accessToken);

    // Step 2: Call platform WebAuthn API
    final credentialResponse = await PasskeyService.create(beginResp.options);

    // Step 3: Complete registration on server
    await _authService.finishPasskeyRegistration(
      conn.serverUrl,
      conn.accessToken,
      beginResp.sessionId,
      name,
      credentialResponse,
    );
  }

  Future<String> createPasskeySetupLink() async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');
    final resp = await _authService.createPasskeySetupLink(
      conn.serverUrl,
      conn.accessToken,
    );
    return resp.link;
  }

  /// Log in with a passkey (discoverable credential).
  Future<void> loginWithPasskey(String serverUrl) async {
    state = const AsyncData(AuthState(isLoading: true));

    try {
      final normalizedUrl = _normalizeUrl(serverUrl);

      // Step 1: Begin login on server
      final beginResp = await _authService.beginPasskeyLogin(normalizedUrl);

      // Step 2: Call platform WebAuthn API
      final assertionResponse = await PasskeyService.get(beginResp.options);

      // Step 3: Complete login on server
      final authResp = await _authService.finishPasskeyLogin(
        normalizedUrl,
        beginResp.sessionId,
        assertionResponse,
      );

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

      await _persistSession(connection, authResp.user);
      state = AsyncData(AuthState(connection: connection, user: authResp.user));
      _registerForPush();
    } catch (e) {
      state = AsyncData(AuthState(error: _parsePasskeyLoginError(e)));
    }
  }

  /// List user's passkeys.
  Future<List<PasskeyInfoResponse>> listPasskeys() async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');
    return _authService.listPasskeys(conn.serverUrl, conn.accessToken);
  }

  /// Delete a passkey.
  Future<void> deletePasskey(String credentialId) async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');
    await _authService.deletePasskey(
        conn.serverUrl, conn.accessToken, credentialId);
  }

  /// Update tokens after a refresh (called by the auth interceptor).
  Future<void> updateTokens(String accessToken, String refreshToken) async {
    final current = state.valueOrNull;
    if (current?.connection == null) return;

    final updated = current!.connection!.copyWith(
      accessToken: accessToken,
      refreshToken: refreshToken,
    );

    await _storage.write(key: StorageKeys.jwt, value: accessToken);
    await _storage.write(key: StorageKeys.refreshToken, value: refreshToken);

    // A successful refresh means we reached the server — clear any reconnecting
    // hold and stop the retry loop.
    _stopReconnect();
    state = AsyncData(
        current.copyWith(connection: updated, isReconnecting: false));
  }

  /// Called when the server has *rejected* our refresh token (a genuine 401):
  /// the session is truly dead, so clear stored credentials and reset state.
  ///
  /// We deliberately do not unregister the push token here. By this point the
  /// access token is already invalid, so the server-side delete couldn't
  /// authenticate anyway; and transport failures never reach this path (the
  /// interceptor only expires on a real 401), so a dropped VPN can't wipe the
  /// device's push registration. A stale gateway token is pruned server-side the
  /// next time APNs reports it unregistered. Push deregistration belongs to an
  /// explicit, deliberate logout (token still valid) — not to session expiry.
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

  /// Check if passkey offer should be shown — requires both platform
  /// support and server-side secure context (HTTPS / localhost).
  Future<bool> _shouldOfferPasskey(String serverUrl) async {
    if (!await PasskeyService.isAvailableAsync()) return false;
    try {
      final status = await _authService.getServerStatus(serverUrl);
      return status.supportsPasskeyPlatform(PasskeyService.platformKind());
    } catch (_) {
      return false;
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
    _stopReconnect();
    await _storage.delete(key: StorageKeys.serverUrl);
    await _storage.delete(key: StorageKeys.jwt);
    await _storage.delete(key: StorageKeys.refreshToken);
    await _storage.delete(key: StorageKeys.deviceId);
    await _storage.delete(key: StorageKeys.sessionUser);
    await _storage.delete(key: StorageKeys.sessionConnection);
  }

  /// Cache the non-secret parts of an authenticated session (user profile +
  /// server config) so a later cold start can restore an optimistic, usable
  /// session before the server is reachable. Tokens are stored separately.
  Future<void> _persistSession(
      BackendConnection conn, UserProfile user) async {
    await _storage.write(
        key: StorageKeys.sessionUser, value: jsonEncode(user.toJson()));
    await _storage.write(
      key: StorageKeys.sessionConnection,
      value: jsonEncode({
        'server_name': conn.serverName,
        'services': conn.services.toJson(),
        'instances': conn.instances.map((i) => i.toJson()).toList(),
      }),
    );
  }

  /// Fire-and-forget push registration. Must never block or throw into the
  /// auth flow (the service swallows its own errors; this guards the rest).
  void _registerForPush() {
    try {
      ref.read(pushServiceProvider).registerForPush();
    } catch (e) {
      debugPrint('Push registration kickoff failed: $e');
    }
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
    debugPrint('Auth error: $e');
    if (e is DioException) {
      final statusCode = e.response?.statusCode;
      if (statusCode == 401) return 'Invalid username or password';
      if (statusCode == 404) return 'Server not found at this URL';
      if (statusCode == 409) return 'Username already taken';
      if (statusCode == 429) {
        return 'Too many attempts. Please wait a moment and try again.';
      }
      if (e.type == DioExceptionType.connectionError ||
          e.type == DioExceptionType.connectionTimeout) {
        return 'Could not connect to server';
      }
      if (e.type == DioExceptionType.receiveTimeout ||
          e.type == DioExceptionType.sendTimeout) {
        return 'Server took too long to respond';
      }
      // Extract error message from server response
      final data = e.response?.data;
      if (data is Map<String, dynamic>) {
        final error = data['error'] as String?;
        if (error != null) return error;
      }
      if (statusCode != null) {
        return 'Server error ($statusCode). Check server logs for details.';
      }
    }
    if (e is Exception) {
      final msg = e.toString();
      if (msg.startsWith('Exception: ')) {
        final message = msg.replaceFirst('Exception: ', '');
        if (message.contains('passkey') ||
            message.contains('Passkey') ||
            message.contains('credential provider') ||
            message.contains('Google account')) {
          return message;
        }
      }
      if (msg.contains('Connection refused') ||
          msg.contains('SocketException')) {
        return 'Could not connect to server';
      }
    }
    return 'Connection failed. Please check the server URL.';
  }

  String _parsePasskeyLoginError(Object e) {
    debugPrint('Passkey login error: $e');
    if (e is DioException) {
      if (e.response?.statusCode == 429) {
        return 'Too many attempts. Please wait a moment and try again.';
      }
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
    if (e is Exception) {
      final message = e.toString().replaceFirst('Exception: ', '');
      if (message.contains('passkey') ||
          message.contains('Passkey') ||
          message.contains('credential provider') ||
          message.contains('Google account')) {
        return message;
      }
    }
    return 'Passkey authentication failed. Try signing in with your password.';
  }

  String _parseSetupError(Object e) {
    debugPrint('Setup error: $e');
    if (e is DioException) {
      final statusCode = e.response?.statusCode;
      if (statusCode == 409) return 'Setup has already been completed';
      if (statusCode == 429) {
        return 'Too many attempts. Please wait a moment and try again.';
      }
      if (e.type == DioExceptionType.connectionError ||
          e.type == DioExceptionType.connectionTimeout) {
        return 'Could not connect to server';
      }
      if (e.type == DioExceptionType.receiveTimeout ||
          e.type == DioExceptionType.sendTimeout) {
        return 'Server took too long to respond';
      }
      // Extract error message from server response
      final data = e.response?.data;
      if (data is Map<String, dynamic>) {
        final error = data['error'] as String?;
        if (error != null) return error;
      }
      if (statusCode != null) {
        return 'Server error ($statusCode). Check server logs for details.';
      }
    }
    return 'Setup failed. Please try again.';
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

/// The auth service used by [AuthNotifier]. Exposed as a provider so tests can
/// inject a fake (subclass [AuthService]) without hitting the network.
final authServiceProvider = Provider<AuthService>((ref) => AuthService());

/// The main auth state provider used throughout the app.
final authProvider =
    AsyncNotifierProvider<AuthNotifier, AuthState>(AuthNotifier.new);
