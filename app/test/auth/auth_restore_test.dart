import 'dart:convert';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/storage/secure_storage.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Exercises [AuthNotifier] session restore: the seamless optimistic restore,
/// background validation, and — critically — that a transport failure NEVER
/// destroys the stored session (only a genuine 401 does). These are the branches
/// behind the offline-logout bug, so they're worth pinning down.
void main() {
  // The real PushService (constructed by _registerForPush on success paths)
  // sets a MethodChannel handler; it needs the test binding initialised. It
  // then no-ops off-iOS, so no platform calls actually happen.
  TestWidgetsFlutterBinding.ensureInitialized();

  const user = UserProfile(id: 1, username: 'tester', role: 'user');
  const freshResp = AuthResponse(
    accessToken: 'new-access',
    refreshToken: 'new-refresh',
    user: user,
    deviceId: 'dev-1',
  );
  const config =
      ServerConfig(serverName: 'Home', services: AvailableServices(radarr: true));

  final connectionError = DioException(
    requestOptions: RequestOptions(path: '/api/auth/refresh'),
    type: DioExceptionType.connectionError,
  );
  final unauthorized = DioException(
    requestOptions: RequestOptions(path: '/api/auth/refresh'),
    type: DioExceptionType.badResponse,
    response: Response(
      requestOptions: RequestOptions(path: '/api/auth/refresh'),
      statusCode: 401,
    ),
  );

  Map<String, String?> tokensOnlyStorage() => {
        StorageKeys.serverUrl: 'http://localhost',
        StorageKeys.jwt: 'old-access',
        StorageKeys.refreshToken: 'old-refresh',
        StorageKeys.deviceId: 'dev-1',
      };

  Map<String, String?> snapshotStorage() => {
        ...tokensOnlyStorage(),
        StorageKeys.sessionUser: jsonEncode(user.toJson()),
        StorageKeys.sessionConnection: jsonEncode({
          'server_name': 'Home',
          'services': const AvailableServices(radarr: true).toJson(),
          'instances': const <Map<String, dynamic>>[],
        }),
      };

  ProviderContainer makeContainer(
    Map<String, String?> storage,
    AuthService authService,
  ) {
    final container = ProviderContainer(overrides: [
      storageServiceProvider.overrideWithValue(_FakeStorage(storage)),
      authServiceProvider.overrideWithValue(authService),
    ]);
    addTearDown(container.dispose);
    return container;
  }

  group('with a cached snapshot (optimistic restore)', () {
    test('opens authenticated + reconnecting, then upgrades to fresh on '
        'successful validation', () async {
      final storage = snapshotStorage();
      final container = makeContainer(
        storage,
        _FakeAuthService(refreshResult: freshResp, config: config),
      );

      // Build returns the optimistic session immediately, from the snapshot.
      final optimistic = await container.read(authProvider.future);
      expect(optimistic.isAuthenticated, isTrue);
      expect(optimistic.isReconnecting, isTrue);
      expect(optimistic.user?.username, 'tester');
      expect(optimistic.connection?.services.radarr, isTrue);

      // Background validation then refreshes and clears the reconnecting flag.
      await _pumpUntil(() {
        final s = container.read(authProvider).valueOrNull;
        return s != null && !s.isReconnecting;
      });
      final settled = container.read(authProvider).valueOrNull!;
      expect(settled.isAuthenticated, isTrue);
      expect(settled.isReconnecting, isFalse);
      expect(storage[StorageKeys.jwt], 'new-access');
      expect(storage[StorageKeys.refreshToken], 'new-refresh');
    });

    test('keeps the session (reconnecting) and retains tokens on a transport '
        'failure', () async {
      final storage = snapshotStorage();
      final fake = _FakeAuthService(refreshError: connectionError);
      final container = makeContainer(storage, fake);

      await container.read(authProvider.future);
      await _pumpUntil(() => fake.refreshCalls > 0);
      // Let the (synchronous) catch + reconnecting handling fully settle.
      await Future<void>.delayed(const Duration(milliseconds: 30));

      final s = container.read(authProvider).valueOrNull!;
      expect(s.isAuthenticated, isTrue, reason: 'must not log out while offline');
      expect(s.isReconnecting, isTrue);
      expect(storage[StorageKeys.refreshToken], 'old-refresh',
          reason: 'tokens must survive a transport failure');
      expect(storage[StorageKeys.jwt], 'old-access');
      expect(storage[StorageKeys.sessionUser], isNotNull);
    });

    test('clears the session on a genuine 401', () async {
      final storage = snapshotStorage();
      final container =
          makeContainer(storage, _FakeAuthService(refreshError: unauthorized));

      await container.read(authProvider.future);
      await _pumpUntil(() {
        final s = container.read(authProvider).valueOrNull;
        return s != null && !s.isAuthenticated;
      });

      expect(container.read(authProvider).valueOrNull!.isAuthenticated, isFalse);
      expect(storage[StorageKeys.refreshToken], isNull);
      expect(storage[StorageKeys.jwt], isNull);
      expect(storage[StorageKeys.sessionUser], isNull,
          reason: 'a real rejection should drop the cached snapshot too');
    });
  });

  group('without a snapshot (inline fallback)', () {
    test('authenticates and writes a snapshot on success', () async {
      final storage = tokensOnlyStorage();
      final container = makeContainer(
        storage,
        _FakeAuthService(refreshResult: freshResp, config: config),
      );

      final state = await container.read(authProvider.future);
      expect(state.isAuthenticated, isTrue);
      expect(state.isReconnecting, isFalse);
      expect(storage[StorageKeys.jwt], 'new-access');
      expect(storage[StorageKeys.sessionUser], isNotNull,
          reason: 'a snapshot should be written so the next launch is seamless');
    });

    test('stays unauthenticated but RETAINS tokens on a transport failure',
        () async {
      final storage = tokensOnlyStorage();
      final container =
          makeContainer(storage, _FakeAuthService(refreshError: connectionError));

      final state = await container.read(authProvider.future);
      expect(state.isAuthenticated, isFalse);
      expect(storage[StorageKeys.refreshToken], 'old-refresh',
          reason: 'offline restore must not wipe the credential');
    });

    test('clears tokens on a genuine 401', () async {
      final storage = tokensOnlyStorage();
      final container =
          makeContainer(storage, _FakeAuthService(refreshError: unauthorized));

      final state = await container.read(authProvider.future);
      expect(state.isAuthenticated, isFalse);
      expect(storage[StorageKeys.refreshToken], isNull);
    });
  });
}

/// Pumps the event loop until [predicate] holds or the timeout elapses.
Future<void> _pumpUntil(
  bool Function() predicate, {
  Duration timeout = const Duration(seconds: 2),
}) async {
  final sw = Stopwatch()..start();
  while (!predicate()) {
    if (sw.elapsed > timeout) fail('Condition not met within $timeout');
    await Future<void>.delayed(const Duration(milliseconds: 5));
  }
}

/// In-memory [StorageService] backed by a caller-owned map so tests can both
/// seed it and assert on it after the notifier mutates it.
class _FakeStorage implements StorageService {
  _FakeStorage(this._data);

  final Map<String, String?> _data;

  @override
  Future<String?> read({required String key}) async => _data[key];

  @override
  Future<void> write({required String key, required String? value}) async {
    if (value == null) {
      _data.remove(key);
    } else {
      _data[key] = value;
    }
  }

  @override
  Future<void> delete({required String key}) async => _data.remove(key);
}

/// Fake [AuthService] that returns a canned refresh result or throws a chosen
/// error, so restore can be driven through every branch without the network.
class _FakeAuthService extends AuthService {
  _FakeAuthService({this.refreshResult, this.refreshError, this.config});

  final AuthResponse? refreshResult;
  final Object? refreshError;
  final ServerConfig? config;
  int refreshCalls = 0;

  @override
  Future<AuthResponse> refreshToken(String serverUrl, String refreshToken) async {
    refreshCalls++;
    final error = refreshError;
    if (error != null) throw error;
    return refreshResult!;
  }

  @override
  Future<ServerConfig> fetchConfig(String serverUrl, String accessToken) async {
    return config ??
        const ServerConfig(serverName: 'Home', services: AvailableServices());
  }
}
