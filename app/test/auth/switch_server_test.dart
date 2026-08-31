import 'dart:convert';

import 'package:cantinarr/core/device/device_identity.dart';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/storage/secure_storage.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/notifications/push_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Exercises [AuthNotifier.switchServer] — the redeem-first server switch
/// behind a connect link tapped while signed in. The contract: the new
/// server must accept the link BEFORE the current session is touched (a
/// rejected link leaves everything exactly as it was), the old session's
/// teardown mirrors logout() and is best-effort, and adoption fully
/// overwrites storage so a cold start cannot resurrect the old server.
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  const serverA = 'https://a.example.com';
  const serverB = 'https://b.example.com';
  const userA = UserProfile(id: 1, username: 'user-a', role: 'user');

  Map<String, String?> serverAStorage() => {
        StorageKeys.serverUrl: serverA,
        StorageKeys.jwt: 'a-access-old',
        StorageKeys.refreshToken: 'a-refresh',
        StorageKeys.refreshTokenBackup: 'a-refresh',
        StorageKeys.deviceId: 'a-device',
        StorageKeys.hardwareId: 'hw-1',
        StorageKeys.sessionUser: jsonEncode(userA.toJson()),
        StorageKeys.sessionConnection: jsonEncode({
          'server_name': 'Server A',
          'services': const AvailableServices(radarr: true).toJson(),
          'instances': const <Map<String, dynamic>>[],
        }),
      };

  Future<(ProviderContainer, Map<String, String?>, List<String>)>
      makeAuthenticatedOnServerA({
    Object? redeemError,
    Object? oldLogoutError,
  }) async {
    final storage = serverAStorage();
    final calls = <String>[];
    final store = _RecordingStorage(storage, calls);
    final container = ProviderContainer(overrides: [
      storageServiceProvider.overrideWithValue(store),
      authServiceProvider.overrideWithValue(_FakeAuthService(
        calls,
        redeemError: redeemError,
        oldLogoutError: oldLogoutError,
      )),
      deviceIdentityProvider.overrideWithValue(_FakeDeviceIdentityService(store)),
      pushServiceProvider
          .overrideWith((ref) => _RecordingPushService(ref, calls)),
    ]);
    addTearDown(container.dispose);

    await container.read(authProvider.future);
    await _pumpUntil(() {
      final s = container.read(authProvider).valueOrNull;
      return s != null && s.isAuthenticated && !s.isReconnecting;
    });
    calls.clear();
    return (container, storage, calls);
  }

  test('adopts the new server and fully overwrites storage', () async {
    final (container, storage, _) = await makeAuthenticatedOnServerA();

    final switched = await container
        .read(authProvider.notifier)
        .switchServer(serverB, 'token-b');

    expect(switched, isTrue);
    final s = container.read(authProvider).valueOrNull!;
    expect(s.isAuthenticated, isTrue);
    expect(s.connection!.serverUrl, serverB);
    expect(s.user!.username, 'user-b');

    expect(storage[StorageKeys.serverUrl], serverB);
    expect(storage[StorageKeys.jwt], 'b-access');
    expect(storage[StorageKeys.refreshToken], 'b-refresh');
    expect(storage[StorageKeys.refreshTokenBackup], 'b-refresh');
    expect(storage[StorageKeys.deviceId], 'b-device');
    expect(storage[StorageKeys.hardwareId], 'hw-1',
        reason: 'the stable hardware id survives a switch');
    final snapshot = jsonDecode(storage[StorageKeys.sessionConnection]!)
        as Map<String, dynamic>;
    expect(snapshot['server_name'], 'Server B',
        reason: 'no server-A state may linger in the restore snapshot');
  });

  test('redeems first, then tears down the old session, then adopts',
      () async {
    final (container, _, calls) = await makeAuthenticatedOnServerA();

    await container.read(authProvider.notifier).switchServer(serverB, 'token-b');

    final redeem = calls.indexWhere((c) => c.startsWith('redeem'));
    final unregister = calls.indexOf('push.unregister');
    final revoke = calls.indexWhere((c) => c.startsWith('auth.logout'));
    final firstWrite = calls.indexWhere((c) => c.startsWith('storage.write'));
    expect(redeem, isNonNegative);
    expect(unregister, isNonNegative);
    expect(revoke, isNonNegative);
    expect(firstWrite, isNonNegative);
    expect(redeem, lessThan(unregister),
        reason: 'the old session must be untouched until the new server '
            'has accepted the link');
    expect(unregister, lessThan(revoke),
        reason: 'push unregister needs the old device_id still in storage');
    expect(revoke, lessThan(firstWrite),
        reason: 'the old-session revoke needs the old credentials, which '
            'adoption overwrites');
    expect(calls[revoke], 'auth.logout $serverA a-access',
        reason: 'the revoke must target the old server with its live token');
  });

  test('a rejected link leaves the current session completely untouched',
      () async {
    final (container, storage, calls) = await makeAuthenticatedOnServerA(
      redeemError: DioException(
        requestOptions: RequestOptions(path: '/api/auth/connect'),
        type: DioExceptionType.badResponse,
        response: Response(
          requestOptions: RequestOptions(path: '/api/auth/connect'),
          statusCode: 400,
        ),
      ),
    );

    final switched = await container
        .read(authProvider.notifier)
        .switchServer(serverB, 'token-dead');

    expect(switched, isFalse);
    final s = container.read(authProvider).valueOrNull!;
    expect(s.isAuthenticated, isTrue,
        reason: 'a dead link must never cost the current session — a '
            'passwordless user may have no way back in');
    expect(s.connection!.serverUrl, serverA);
    expect(storage[StorageKeys.serverUrl], serverA);
    expect(storage[StorageKeys.refreshToken], 'a-refresh');
    expect(calls.contains('push.unregister'), isFalse,
        reason: 'no teardown may run when the redeem was refused');
    expect(calls.any((c) => c.startsWith('auth.logout')), isFalse);
  });

  test('an unreachable old server never blocks the switch', () async {
    final (container, storage, _) = await makeAuthenticatedOnServerA(
      oldLogoutError: DioException(
        requestOptions: RequestOptions(path: '/api/auth/logout'),
        type: DioExceptionType.connectionError,
      ),
    );

    final switched = await container
        .read(authProvider.notifier)
        .switchServer(serverB, 'token-b');

    expect(switched, isTrue);
    expect(
        container.read(authProvider).valueOrNull!.connection!.serverUrl,
        serverB);
    expect(storage[StorageKeys.serverUrl], serverB);
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

/// In-memory [StorageService] that records writes and deletes, so ordering
/// against the network calls can be asserted.
class _RecordingStorage implements StorageService {
  _RecordingStorage(this._data, this.calls);

  final Map<String, String?> _data;
  final List<String> calls;

  @override
  Future<String?> read({required String key}) async => _data[key];

  @override
  Future<void> write({required String key, required String? value}) async {
    calls.add('storage.write:$key');
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

class _FakeDeviceIdentityService extends DeviceIdentityService {
  _FakeDeviceIdentityService(super.storage);

  @override
  Future<DeviceIdentity> resolve() async =>
      const DeviceIdentity(displayName: 'Test Device', hardwareId: 'hw-1');
}

/// Serves server A for the restore path and server B for the switch; records
/// every credential-bearing call into the shared ordering list.
class _FakeAuthService extends AuthService {
  _FakeAuthService(this.calls, {this.redeemError, this.oldLogoutError});

  final List<String> calls;
  final Object? redeemError;
  final Object? oldLogoutError;

  @override
  Future<AuthResponse> refreshToken(
      String serverUrl, String refreshToken) async {
    return const AuthResponse(
      accessToken: 'a-access',
      refreshToken: 'a-refresh',
      user: UserProfile(id: 1, username: 'user-a', role: 'user'),
      deviceId: 'a-device',
    );
  }

  @override
  Future<AuthResponse> redeemConnectToken(
    String serverUrl,
    String token,
    String deviceName,
    String hardwareId,
  ) async {
    calls.add('redeem $serverUrl $token');
    final error = redeemError;
    if (error != null) throw error;
    return const AuthResponse(
      accessToken: 'b-access',
      refreshToken: 'b-refresh',
      user: UserProfile(id: 2, username: 'user-b', role: 'user'),
      deviceId: 'b-device',
    );
  }

  @override
  Future<ServerConfig> fetchConfig(String serverUrl, String accessToken) async {
    return ServerConfig(
      serverName:
          serverUrl == 'https://b.example.com' ? 'Server B' : 'Server A',
      services: const AvailableServices(radarr: true),
    );
  }

  @override
  Future<void> logout(String serverUrl, String accessToken) async {
    calls.add('auth.logout $serverUrl $accessToken');
    final error = oldLogoutError;
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
