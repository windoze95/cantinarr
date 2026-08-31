import 'dart:convert';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/storage/secure_storage.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/notifications/push_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Exercises [AuthNotifier.logout] — the explicit sign-out that lets a user
/// leave a server (the demo, most importantly). The contract: push unregister
/// and the server-side revoke run first, in that order, while the token can
/// still authenticate; the local clear runs unconditionally, so sign-out
/// works fully offline; the stable hardware id survives so a later reconnect
/// dedupes to the same device row.
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  const user = UserProfile(id: 1, username: 'tester', role: 'user');

  Map<String, String?> seededStorage() => {
        StorageKeys.serverUrl: 'http://localhost',
        StorageKeys.jwt: 'old-access',
        StorageKeys.refreshToken: 'old-refresh',
        StorageKeys.refreshTokenBackup: 'old-refresh',
        StorageKeys.deviceId: 'dev-1',
        StorageKeys.hardwareId: 'hw-1',
        StorageKeys.sessionUser: jsonEncode(user.toJson()),
        StorageKeys.sessionConnection: jsonEncode({
          'server_name': 'Home',
          'services': const AvailableServices(radarr: true).toJson(),
          'instances': const <Map<String, dynamic>>[],
        }),
      };

  Future<ProviderContainer> makeAuthenticatedContainer({
    required Map<String, String?> storage,
    required List<String> calls,
    Object? logoutError,
  }) async {
    final container = ProviderContainer(overrides: [
      storageServiceProvider
          .overrideWithValue(_RecordingStorage(storage, calls)),
      authServiceProvider.overrideWithValue(
        _FakeAuthService(calls, logoutError: logoutError),
      ),
      pushServiceProvider
          .overrideWith((ref) => _RecordingPushService(ref, calls)),
    ]);
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    // Let background validation settle so the connection carries the fresh
    // access token — logout must send the token that is actually live.
    await _pumpUntil(() {
      final s = container.read(authProvider).valueOrNull;
      return s != null && s.isAuthenticated && !s.isReconnecting;
    });
    calls.clear();
    return container;
  }

  test('clears every auth key except hardware_id and resets state', () async {
    final storage = seededStorage();
    final container = await makeAuthenticatedContainer(
      storage: storage,
      calls: <String>[],
    );

    await container.read(authProvider.notifier).logout();

    final s = container.read(authProvider).valueOrNull!;
    expect(s.isAuthenticated, isFalse);
    for (final key in [
      StorageKeys.serverUrl,
      StorageKeys.jwt,
      StorageKeys.refreshToken,
      StorageKeys.refreshTokenBackup,
      StorageKeys.deviceId,
      StorageKeys.sessionUser,
      StorageKeys.sessionConnection,
    ]) {
      expect(storage[key], isNull, reason: '$key must be cleared on logout');
    }
    expect(storage[StorageKeys.hardwareId], 'hw-1',
        reason: 'the stable hardware id survives so re-login dedupes '
            'to the same device row');
  });

  test('still clears locally when the server revoke fails (offline sign-out)',
      () async {
    final storage = seededStorage();
    final container = await makeAuthenticatedContainer(
      storage: storage,
      calls: <String>[],
      logoutError: DioException(
        requestOptions: RequestOptions(path: '/api/auth/logout'),
        type: DioExceptionType.connectionError,
      ),
    );

    await container.read(authProvider.notifier).logout();

    expect(container.read(authProvider).valueOrNull!.isAuthenticated, isFalse);
    expect(storage[StorageKeys.refreshToken], isNull,
        reason: 'an unreachable server must never trap the user in a session');
  });

  test('unregisters push, then revokes, then clears — in that order, with '
      'the pre-clear credentials', () async {
    final calls = <String>[];
    final container = await makeAuthenticatedContainer(
      storage: seededStorage(),
      calls: calls,
    );

    await container.read(authProvider.notifier).logout();

    final unregister = calls.indexOf('push.unregister');
    final revoke = calls.indexWhere((c) => c.startsWith('auth.logout'));
    final firstDelete = calls.indexWhere((c) => c.startsWith('storage.delete'));
    expect(unregister, isNonNegative);
    expect(revoke, isNonNegative);
    expect(firstDelete, isNonNegative);
    expect(unregister, lessThan(revoke),
        reason: 'push unregister needs the device_id still in storage');
    expect(revoke, lessThan(firstDelete),
        reason: 'both server calls need the still-valid access token');
    expect(calls[revoke], 'auth.logout http://localhost new-access',
        reason: 'the revoke must carry the live server URL and access token');
  });
}

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

/// In-memory [StorageService] that also records deletes, so ordering against
/// the network calls can be asserted.
class _RecordingStorage implements StorageService {
  _RecordingStorage(this._data, this.calls);

  final Map<String, String?> _data;
  final List<String> calls;

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
  Future<void> delete({required String key}) async {
    calls.add('storage.delete:$key');
    _data.remove(key);
  }

  @override
  Future<void> hardenAuthKeys() async {}
}

class _FakeAuthService extends AuthService {
  _FakeAuthService(this.calls, {this.logoutError});

  final List<String> calls;
  final Object? logoutError;

  @override
  Future<AuthResponse> refreshToken(
      String serverUrl, String refreshToken) async {
    return const AuthResponse(
      accessToken: 'new-access',
      refreshToken: 'new-refresh',
      user: UserProfile(id: 1, username: 'tester', role: 'user'),
      deviceId: 'dev-1',
    );
  }

  @override
  Future<ServerConfig> fetchConfig(String serverUrl, String accessToken) async {
    return const ServerConfig(
      serverName: 'Home',
      services: AvailableServices(radarr: true),
    );
  }

  @override
  Future<void> logout(String serverUrl, String accessToken) async {
    calls.add('auth.logout $serverUrl $accessToken');
    final error = logoutError;
    if (error != null) throw error;
  }
}

/// Push stays inert (platform channels) but records the unregister call.
class _RecordingPushService extends PushService {
  _RecordingPushService(super.ref, this.calls);

  final List<String> calls;

  @override
  Future<void> registerForPush() async {}

  @override
  Future<void> setBadgeCount(int count) async {}

  @override
  Future<void> unregister() async {
    calls.add('push.unregister');
  }
}
