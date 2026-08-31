import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/device/device_identity.dart';
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

  /// True when secure storage itself could not be read during restore (locked
  /// keychain in a prewarmed/background launch, keystore not ready yet). The
  /// session almost certainly still exists — it must never be treated as
  /// logged out; restore is retried on resume and on a short timer.
  bool _restoreBlocked = false;
  Timer? _restoreRetryTimer;

  @override
  Future<AuthState> build() async {
    _authService = ref.read(authServiceProvider);
    _storage = ref.read(storageServiceProvider);

    ref.onDispose(() {
      _reconnectTimer?.cancel();
      _reconnectTimer = null;
      _restoreRetryTimer?.cancel();
      _restoreRetryTimer = null;
    });

    // Try to restore session from secure storage
    return _tryRestoreSession();
  }

  Future<AuthState> _tryRestoreSession() async {
    final String? serverUrl;
    final String? accessToken;
    String? refreshToken;
    var usedBackupToken = false;
    try {
      serverUrl = await _storage.read(key: StorageKeys.serverUrl);
      accessToken = await _storage.read(key: StorageKeys.jwt);
      refreshToken = await _storage.read(key: StorageKeys.refreshToken);
      if (refreshToken == null) {
        refreshToken = await _storage.read(key: StorageKeys.refreshTokenBackup);
        usedBackupToken = refreshToken != null;
      }
    } catch (e) {
      // Storage is unreadable right now — NOT absent. Showing login here
      // would present a signed-out app to a user whose tokens are intact.
      debugPrint('Secure storage unavailable during restore (will retry): $e');
      _restoreBlocked = true;
      _scheduleRestoreRetry();
      return const AuthState();
    }
    _restoreBlocked = false;
    _stopRestoreRetry();

    if (serverUrl == null || accessToken == null || refreshToken == null) {
      return const AuthState();
    }

    // Storage is readable: opportunistically upgrade item protection classes
    // (one-time, marker-guarded) and heal the primary token from its backup.
    unawaited(_storage.hardenAuthKeys());
    if (usedBackupToken) {
      unawaited(
          _storage.write(key: StorageKeys.refreshToken, value: refreshToken));
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

  /// Re-runs session restore after storage was unreadable (e.g. the device
  /// has been unlocked since a prewarmed launch). Only replaces state while
  /// it is still unauthenticated so it can never clobber a live session.
  Future<void> _retryRestore() async {
    if (!_restoreBlocked) return;
    _restoreBlocked = false;
    final restored = await _tryRestoreSession();
    final current = state.valueOrNull;
    if (current == null || !current.isAuthenticated) {
      state = AsyncData(restored);
    }
  }

  void _scheduleRestoreRetry() {
    if (_restoreRetryTimer != null) return;
    _restoreRetryTimer = Timer.periodic(const Duration(seconds: 5), (_) {
      unawaited(_retryRestore());
    });
  }

  void _stopRestoreRetry() {
    _restoreRetryTimer?.cancel();
    _restoreRetryTimer = null;
  }

  /// Builds an optimistic [AuthState] from the cached session snapshot plus the
  /// stored tokens, or null when no snapshot is cached. The access token may be
  /// stale; [_validateSession] refreshes it. Marked reconnecting until then.
  Future<AuthState?> _cachedSession(
    String serverUrl,
    String accessToken,
    String refreshToken,
  ) async {
    final String? userJson;
    final String? connJson;
    try {
      userJson = await _storage.read(key: StorageKeys.sessionUser);
      connJson = await _storage.read(key: StorageKeys.sessionConnection);
    } catch (e) {
      debugPrint('Cached session unreadable (falling back to inline): $e');
      return null;
    }
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
        serverVersion: meta['version'] as String?,
        minAppVersion: meta['min_app_version'] as String?,
        services: services is Map<String, dynamic>
            ? AvailableServices.fromJson(services)
            : const AvailableServices(),
        instances: (meta['instances'] as List<dynamic>?)
                ?.map(
                    (e) => ServiceInstance.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
        issuesEnabled: meta['issues_enabled'] as bool? ?? false,
        allowReporting: meta['allow_reporting'] as bool? ?? false,
        plexAccessRequestable:
            meta['plex_access_requestable'] as bool? ?? false,
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
  ///
  /// Only the refresh call itself can end the session, and only with a 401 —
  /// the server's explicit "this token is revoked". Storage hiccups, transport
  /// failures, 5xx answers, and config-fetch failures of any kind all keep the
  /// session and retry.
  Future<void> _validateSession() async {
    if (_validating) return;
    _validating = true;
    try {
      final String? serverUrl;
      String? refreshToken;
      final String? deviceId;
      try {
        serverUrl = await _storage.read(key: StorageKeys.serverUrl);
        refreshToken = await _storage.read(key: StorageKeys.refreshToken);
        refreshToken ??=
            await _storage.read(key: StorageKeys.refreshTokenBackup);
        deviceId = await _storage.read(key: StorageKeys.deviceId);
      } catch (e) {
        debugPrint('Session validation deferred (storage unavailable): $e');
        _markReconnecting();
        return;
      }
      if (serverUrl == null || refreshToken == null) return;

      final AuthResponse authResp;
      try {
        authResp = await _authService.refreshToken(serverUrl, refreshToken);
      } on DioException catch (e) {
        if (e.response?.statusCode == 401) {
          // The server rejected the refresh token: the session is truly dead.
          _stopReconnect();
          await _clearStorage();
          state = const AsyncData(AuthState());
        } else {
          // Server unreachable or faulting: keep the user in and keep trying.
          debugPrint('Session validation deferred (server unreachable): $e');
          _markReconnecting();
        }
        return;
      }
      await _saveTokens(serverUrl, authResp.accessToken, authResp.refreshToken,
          authResp.deviceId ?? deviceId);

      // The session is confirmed alive; config is enrichment. On any config
      // failure fall back to the snapshot the optimistic session was built
      // from rather than touching the session.
      BackendConnection? connection;
      try {
        final config =
            await _authService.fetchConfig(serverUrl, authResp.accessToken);
        connection = BackendConnection(
          serverUrl: serverUrl,
          accessToken: authResp.accessToken,
          refreshToken: authResp.refreshToken,
          serverName: config.serverName,
          serverVersion: config.serverVersion,
          minAppVersion: config.minAppVersion,
          services: config.services,
          instances: config.instances,
          issuesEnabled: config.issuesEnabled,
          allowReporting: config.allowReporting,
          plexAccessRequestable: config.plexAccessRequestable,
        );
        await _persistSession(connection, authResp.user);
      } catch (e) {
        debugPrint('Config fetch failed (session kept, using cached): $e');
        final cached = state.valueOrNull?.connection;
        if (cached != null && cached.serverUrl == serverUrl) {
          connection = cached.copyWith(
            accessToken: authResp.accessToken,
            refreshToken: authResp.refreshToken,
          );
        }
      }
      if (connection == null) {
        // Refreshed fine but no config to render with (no snapshot either):
        // hold the optimistic state and let the retry loop finish the job.
        _markReconnecting();
        return;
      }
      _stopReconnect();
      state = AsyncData(AuthState(connection: connection, user: authResp.user));
      _registerForPush();
    } catch (e) {
      debugPrint('Session validation error (staying optimistic): $e');
      _markReconnecting();
    } finally {
      _validating = false;
    }
  }

  /// No-snapshot fallback: validate the stored tokens inline and return the
  /// resulting state. Keeps tokens on a transport failure (only a 401 from the
  /// refresh call itself clears them) and writes a session snapshot on success.
  Future<AuthState> _restoreInline(
    String serverUrl,
    String accessToken,
    String refreshToken,
  ) async {
    String? deviceId;
    try {
      deviceId = await _storage.read(key: StorageKeys.deviceId);
    } catch (_) {
      // Tokens were readable moments ago; treat the device id as optional.
    }

    final AuthResponse authResp;
    try {
      authResp = await _authService.refreshToken(serverUrl, refreshToken);
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

    try {
      await _saveTokens(serverUrl, authResp.accessToken, authResp.refreshToken,
          authResp.deviceId ?? deviceId);
      final config =
          await _authService.fetchConfig(serverUrl, authResp.accessToken);
      final connection = BackendConnection(
        serverUrl: serverUrl,
        accessToken: authResp.accessToken,
        refreshToken: authResp.refreshToken,
        serverName: config.serverName,
        serverVersion: config.serverVersion,
        minAppVersion: config.minAppVersion,
        services: config.services,
        instances: config.instances,
        issuesEnabled: config.issuesEnabled,
        allowReporting: config.allowReporting,
          plexAccessRequestable: config.plexAccessRequestable,
      );
      await _persistSession(connection, authResp.user);
      _registerForPush();
      return AuthState(connection: connection, user: authResp.user);
    } catch (e) {
      // The session is confirmed alive (the refresh succeeded) — a failure
      // from here on must not land on login. Enter the app with a minimal
      // connection; the reconnect loop fetches the full config shortly.
      debugPrint('Config fetch failed after restore (entering degraded): $e');
      final connection = BackendConnection(
        serverUrl: serverUrl,
        accessToken: authResp.accessToken,
        refreshToken: authResp.refreshToken,
      );
      _scheduleReconnect();
      return AuthState(
        connection: connection,
        user: authResp.user,
        isReconnecting: true,
      );
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

  /// Retry validation now (e.g. when the app returns to the foreground)
  /// instead of waiting for the periodic timers. Also retries a restore that
  /// was blocked on unreadable secure storage (locked keychain at launch) —
  /// by the time the user foregrounds the app the device is unlocked.
  void reconnectNow() {
    if (_restoreBlocked) {
      unawaited(_retryRestore());
      return;
    }
    final current = state.valueOrNull;
    if (current != null && current.isAuthenticated && current.isReconnecting) {
      unawaited(_validateSession());
    }
  }

  /// Check server status (needs setup, webauthn available).
  ///
  /// A bare address (no scheme) is probed over https first; when https is
  /// unreachable at the transport level the probe retries over http. Returns
  /// the status together with the URL that actually answered so callers keep
  /// using that exact scheme. An explicit scheme is never second-guessed.
  Future<({String serverUrl, ServerStatus status})> checkServer(
      String serverUrl) async {
    final schemeProbe = serverUrl.trim().toLowerCase();
    final hasScheme = schemeProbe.startsWith('http://') ||
        schemeProbe.startsWith('https://');
    final normalizedUrl = _normalizeUrl(serverUrl);
    try {
      final status = await _authService.getServerStatus(normalizedUrl);
      return (serverUrl: normalizedUrl, status: status);
    } on DioException catch (e) {
      if (hasScheme || !_httpsUnreachable(e)) rethrow;
      final httpUrl = 'http://${normalizedUrl.substring('https://'.length)}';
      final status = await _authService.getServerStatus(httpUrl);
      return (serverUrl: httpUrl, status: status);
    }
  }

  /// True when the https probe failed before any HTTP exchange happened —
  /// connection refused/timeout, a TLS handshake against a plaintext port, or
  /// a certificate failure. An answer with any status code means https IS
  /// available and the real error must surface instead of an http retry.
  bool _httpsUnreachable(DioException e) {
    switch (e.type) {
      case DioExceptionType.connectionError:
      case DioExceptionType.connectionTimeout:
      case DioExceptionType.badCertificate:
        return true;
      case DioExceptionType.unknown:
        // dart:io surfaces TLS/socket failures as `unknown`; match by name
        // because dart:io types can't be imported in web-compatible code.
        final err = e.error?.toString() ?? '';
        return err.contains('HandshakeException') ||
            err.contains('SocketException') ||
            err.contains('CERTIFICATE');
      default:
        return false;
    }
  }

  /// Create admin account during first-run setup.
  Future<void> setup(String serverUrl, String username, String password) async {
    state = const AsyncData(AuthState(isLoading: true));

    try {
      final normalizedUrl = _normalizeUrl(serverUrl);
      final identity = await ref.read(deviceIdentityProvider).resolve();
      final authResp = await _authService.setup(normalizedUrl, username,
          password, identity.displayName, identity.hardwareId);
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
        serverVersion: config.serverVersion,
        minAppVersion: config.minAppVersion,
        services: config.services,
        instances: config.instances,
        issuesEnabled: config.issuesEnabled,
        allowReporting: config.allowReporting,
          plexAccessRequestable: config.plexAccessRequestable,
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
      final identity = await ref.read(deviceIdentityProvider).resolve();
      final authResp = await _authService.login(normalizedUrl, username,
          password, identity.displayName, identity.hardwareId);
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
        serverVersion: config.serverVersion,
        minAppVersion: config.minAppVersion,
        services: config.services,
        instances: config.instances,
        issuesEnabled: config.issuesEnabled,
        allowReporting: config.allowReporting,
          plexAccessRequestable: config.plexAccessRequestable,
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
      final identity = await ref.read(deviceIdentityProvider).resolve();
      final authResp = await _authService.redeemConnectToken(
          normalizedUrl, token, identity.displayName, identity.hardwareId);
      final config =
          await _authService.fetchConfig(normalizedUrl, authResp.accessToken);
      await _adoptSession(normalizedUrl, authResp, config);
    } catch (e) {
      state = AsyncData(AuthState(error: _parseConnectError(e)));
    }
  }

  /// Replace this device's session with one redeemed from a connect link —
  /// the path for a link tapped while already signed in. The new server must
  /// accept the link BEFORE the current session is touched: a passwordless
  /// user may have no way back in, so an expired or superseded link has to
  /// leave them exactly where they were. Returns false on rejection (the
  /// current session is untouched); the old session's teardown mirrors
  /// [logout] and is best-effort — an unreachable old server never blocks
  /// the switch.
  Future<bool> switchServer(String serverUrl, String token) async {
    final oldConn = state.valueOrNull?.connection;

    final String normalizedUrl;
    final AuthResponse authResp;
    final ServerConfig config;
    try {
      normalizedUrl = _normalizeUrl(serverUrl);
      final identity = await ref.read(deviceIdentityProvider).resolve();
      authResp = await _authService.redeemConnectToken(
          normalizedUrl, token, identity.displayName, identity.hardwareId);
      config =
          await _authService.fetchConfig(normalizedUrl, authResp.accessToken);
    } catch (e) {
      debugPrint('Switch server: link rejected, keeping current session: $e');
      return false;
    }

    // The new server accepted us. End the old session while its access token
    // and the stored device_id are still in place (same order as logout()).
    if (oldConn != null) {
      try {
        await ref
            .read(pushServiceProvider)
            .unregister()
            .timeout(const Duration(seconds: 5));
      } catch (e) {
        debugPrint('Switch server: push unregister skipped: $e');
      }
      try {
        await _authService
            .logout(oldConn.serverUrl, oldConn.accessToken)
            .timeout(const Duration(seconds: 5));
      } catch (e) {
        debugPrint('Switch server: old-session revoke skipped: $e');
      }
    }

    await _adoptSession(normalizedUrl, authResp, config);
    return true;
  }

  /// The single session-adoption path: persist the redeemed tokens and the
  /// snapshot, swap state, register push. Both first-connect and a server
  /// switch end here, so storage is always fully overwritten (the isolation
  /// contract server_switch_isolation_test pins).
  Future<void> _adoptSession(
    String normalizedUrl,
    AuthResponse authResp,
    ServerConfig config,
  ) async {
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
      serverVersion: config.serverVersion,
      minAppVersion: config.minAppVersion,
      services: config.services,
      instances: config.instances,
      issuesEnabled: config.issuesEnabled,
      allowReporting: config.allowReporting,
          plexAccessRequestable: config.plexAccessRequestable,
    );

    await _persistSession(connection, authResp.user);
    state = AsyncData(AuthState(connection: connection, user: authResp.user));
    _registerForPush();
  }

  /// Handle a cantinarr:// deep link while signed out. Links that arrive
  /// while authenticated are the app shell's job — it prompts and calls
  /// [switchServer] — so this guard stays as a backstop for direct callers.
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
      serverVersion: config.serverVersion,
      minAppVersion: config.minAppVersion,
      services: config.services,
      instances: config.instances,
      issuesEnabled: config.issuesEnabled,
      allowReporting: config.allowReporting,
          plexAccessRequestable: config.plexAccessRequestable,
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

  /// Share or update the email this user wants their Plex invite sent to.
  /// The server notifies admins when the address is new or changed.
  Future<void> setPlexEmail(String email) async {
    final current = state.valueOrNull;
    final conn = current?.connection;
    if (current == null || conn == null) throw Exception('Not authenticated');
    final trimmed = email.trim();
    await _authService.setPlexEmail(conn.serverUrl, conn.accessToken, trimmed);
    final user = current.user;
    if (user != null) {
      // A new address resets the invited stamp locally too — any invite
      // already sent went to the old email (the server does the same).
      state = AsyncData(
        current.copyWith(
          user: user.copyWith(plexEmail: trimmed, clearPlexInvitedAt: true),
        ),
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

  /// Enable or disable this user's access to the server-provided AI account.
  /// Their own API keys and ChatGPT link are intentionally untouched.
  Future<UserSummary> updateUserAiAccess(
    int userId,
    bool sharedAiEnabled,
  ) async {
    final conn = state.valueOrNull?.connection;
    if (conn == null) throw Exception('Not authenticated');
    return _authService.updateUserAiAccess(
      conn.serverUrl,
      conn.accessToken,
      userId,
      sharedAiEnabled,
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
      final identity = await ref.read(deviceIdentityProvider).resolve();
      final authResp = await _authService.finishPasskeyLogin(
        normalizedUrl,
        beginResp.sessionId,
        assertionResponse,
        identity.displayName,
        identity.hardwareId,
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
        serverVersion: config.serverVersion,
        minAppVersion: config.minAppVersion,
        services: config.services,
        instances: config.instances,
        issuesEnabled: config.issuesEnabled,
        allowReporting: config.allowReporting,
          plexAccessRequestable: config.plexAccessRequestable,
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
    // The refresh token is stable under the current server scheme; only touch
    // its (redundant) storage when it actually changes.
    if (refreshToken != current.connection!.refreshToken) {
      await _storage.write(
          key: StorageKeys.refreshTokenBackup, value: refreshToken);
      await _storage.write(key: StorageKeys.refreshToken, value: refreshToken);
    }

    // A successful refresh means we reached the server — clear any reconnecting
    // hold and stop the retry loop.
    _stopReconnect();
    state =
        AsyncData(current.copyWith(connection: updated, isReconnecting: false));
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

  /// Explicit, deliberate sign-out — the counterpart to [onAuthExpired]'s
  /// server-initiated expiry. This is the one path where push deregistration
  /// and server-side revocation belong: the access token is still valid, so
  /// both calls can authenticate. Each is best-effort with a short timeout —
  /// the local clear always runs, so signing out works fully offline (at the
  /// cost of leaving the token alive server-side until an admin revokes it).
  Future<void> logout() async {
    final conn = state.valueOrNull?.connection;

    // Before the token clear: unregister() reads device_id from storage and
    // its DELETE must still authenticate.
    try {
      await ref
          .read(pushServiceProvider)
          .unregister()
          .timeout(const Duration(seconds: 5));
    } catch (e) {
      debugPrint('Logout: push unregister skipped: $e');
    }

    if (conn != null) {
      try {
        await _authService
            .logout(conn.serverUrl, conn.accessToken)
            .timeout(const Duration(seconds: 5));
      } catch (e) {
        debugPrint('Logout: server-side revoke skipped: $e');
      }
    }

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
    // Backup copy first: if anything interrupts between the two writes, at
    // least one intact copy of the refresh token survives.
    await _storage.write(
        key: StorageKeys.refreshTokenBackup, value: refreshToken);
    await _storage.write(key: StorageKeys.refreshToken, value: refreshToken);
    if (deviceId != null) {
      await _storage.write(key: StorageKeys.deviceId, value: deviceId);
    }
  }

  Future<void> _clearStorage() async {
    _stopReconnect();
    _stopRestoreRetry();
    _restoreBlocked = false;
    await _storage.delete(key: StorageKeys.serverUrl);
    await _storage.delete(key: StorageKeys.jwt);
    await _storage.delete(key: StorageKeys.refreshToken);
    await _storage.delete(key: StorageKeys.refreshTokenBackup);
    await _storage.delete(key: StorageKeys.deviceId);
    await _storage.delete(key: StorageKeys.sessionUser);
    await _storage.delete(key: StorageKeys.sessionConnection);
  }

  /// Cache the non-secret parts of an authenticated session (user profile +
  /// server config) so a later cold start can restore an optimistic, usable
  /// session before the server is reachable. Tokens are stored separately.
  Future<void> _persistSession(BackendConnection conn, UserProfile user) async {
    await _storage.write(
        key: StorageKeys.sessionUser, value: jsonEncode(user.toJson()));
    await _storage.write(
      key: StorageKeys.sessionConnection,
      value: jsonEncode({
        'server_name': conn.serverName,
        'version': conn.serverVersion,
        'min_app_version': conn.minAppVersion,
        'services': conn.services.toJson(),
        'instances': conn.instances.map((i) => i.toJson()).toList(),
        'issues_enabled': conn.issuesEnabled,
        'allow_reporting': conn.allowReporting,
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
    final schemeProbe = normalized.toLowerCase();
    if (schemeProbe.startsWith('http://')) {
      normalized = 'http://${normalized.substring('http://'.length)}';
    } else if (schemeProbe.startsWith('https://')) {
      normalized = 'https://${normalized.substring('https://'.length)}';
    } else {
      normalized = 'https://$normalized';
    }
    while (normalized.endsWith('/')) {
      normalized = normalized.substring(0, normalized.length - 1);
    }
    return normalized;
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
