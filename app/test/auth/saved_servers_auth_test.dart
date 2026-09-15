import 'dart:convert';

import 'package:cantinarr/core/device/device_identity.dart';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/storage/preferences.dart';
import 'package:cantinarr/core/storage/secure_storage.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/data/oidc_service.dart';
import 'package:cantinarr/features/auth/data/plex_auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/auth/logic/saved_servers_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:passkeys_platform_interface/passkeys_platform_interface.dart';
import 'package:passkeys_platform_interface/types/types.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'oidc_service_test.dart' show MemoryStorage;
import 'plex_auth_service_test.dart' show PlexAuthFake;

const _old = 'http://old.test:8585';
const _new = 'https://new.test/library';
const _response = AuthResponse(
  accessToken: 'access-secret',
  refreshToken: 'refresh-secret',
  deviceId: 'device',
  user: UserProfile(id: 1, username: 'viewer', role: 'user'),
);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  Future<ProviderContainer> harness({
    MemoryStorage? storage,
    _Auth? auth,
    bool preferencesUnavailable = false,
  }) async {
    storage ??= MemoryStorage();
    auth ??= _Auth();
    final container = ProviderContainer(overrides: [
      storageServiceProvider.overrideWithValue(storage),
      authServiceProvider.overrideWithValue(auth),
      deviceIdentityProvider.overrideWithValue(_Device(storage)),
      oidcServiceProvider.overrideWithValue(_OIDC(auth, storage)),
      plexAuthServiceProvider
          .overrideWithValue(PlexAuthService(auth, storage, isWeb: false)),
      if (preferencesUnavailable)
        sharedPreferencesProvider.overrideWith(
            (_) async => throw StateError('Preferences unavailable')),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    return container;
  }

  for (final method in [
    'password',
    'setup',
    'connect',
    'passkey',
    'sso',
    'plex'
  ]) {
    test('$method success saves its server and sign-out keeps it', () async {
      final originalPlatform = PasskeysPlatform.instance;
      PasskeysPlatform.instance = _Passkeys();
      addTearDown(() => PasskeysPlatform.instance = originalPlatform);
      final storage = MemoryStorage();
      final container = await harness(storage: storage);
      final notifier = container.read(authProvider.notifier);
      switch (method) {
        case 'password':
          await notifier.login(_new, 'viewer', 'password');
        case 'setup':
          await notifier.setup(_new, 'viewer', 'password');
        case 'connect':
          await notifier.connectWithToken(_new, 'invitation');
        case 'passkey':
          await notifier.loginWithPasskey(_new);
        case 'sso':
          await notifier.finishSSO(Uri.parse('cantinarr://oidc?code=return'));
        case 'plex':
          await container.read(plexAuthServiceProvider).start(_new);
          await notifier.checkPlex();
      }
      expect(container.read(authProvider).valueOrNull!.isAuthenticated, isTrue);
      final saved = await container.read(savedServersProvider.future);
      expect(saved.single.url, _new);
      expect(saved.single.name, 'New server');
      await notifier.logout();
      expect(storage.values[StorageKeys.serverUrl], isNull);
      expect(storage.values[StorageKeys.jwt], isNull);
      expect(
          (await container.read(savedServersProvider.future)).single.url, _new);
    });
  }

  test('upgrade saves legacy server before a revoked token is cleared',
      () async {
    final storage = MemoryStorage()
      ..values.addAll({
        StorageKeys.serverUrl: _old,
        StorageKeys.jwt: 'old-access',
        StorageKeys.refreshToken: 'old-refresh',
        StorageKeys.sessionConnection:
            jsonEncode({'server_name': 'Old server'}),
      });
    final container =
        await harness(storage: storage, auth: _Auth()..revoked = true);
    expect(container.read(authProvider).valueOrNull!.isAuthenticated, isFalse);
    expect(storage.values[StorageKeys.serverUrl], isNull);
    final saved = await container.read(savedServersProvider.future);
    expect(saved.single.url, _old);
    expect(saved.single.name, 'Old server');
  });

  test('failed login and config failure do not change the saved default',
      () async {
    final auth = _Auth();
    final container = await harness(auth: auth);
    final notifier = container.read(authProvider.notifier);
    await notifier.login(_old, 'viewer', 'password');
    await notifier.logout();
    auth.failLogin = true;
    await notifier.login(_new, 'viewer', 'wrong');
    auth.failLogin = false;
    auth.failConfig = true;
    await notifier.connectWithToken(_new, 'invitation');
    await notifier.checkServer(_new);
    expect(
        (await container.read(savedServersProvider.future)).single.url, _old);
  });

  test('failed switch preserves history; successful switch becomes the default',
      () async {
    final auth = _Auth();
    final container = await harness(auth: auth);
    final notifier = container.read(authProvider.notifier);
    await notifier.login(_old, 'viewer', 'password');
    auth.failConfig = true;
    expect(await notifier.switchServer(_new, 'invitation'),
        ServerSwitchResult.rejected);
    expect(
        (await container.read(savedServersProvider.future)).single.url, _old);
    auth.failConfig = false;
    expect(await notifier.switchServer(_new, 'invitation'),
        ServerSwitchResult.switched);
    await notifier.onAuthExpired();
    expect(
        (await container.read(savedServersProvider.future)).map((s) => s.url),
        [_new, _old]);
  });

  test('preference failures never block successful sign-in or sign-out',
      () async {
    final container = await harness(preferencesUnavailable: true);
    final notifier = container.read(authProvider.notifier);
    await notifier.login(_new, 'viewer', 'password');
    expect(container.read(authProvider).valueOrNull!.isAuthenticated, isTrue);
    await notifier.logout();
    expect(container.read(authProvider).valueOrNull!.isAuthenticated, isFalse);
  });

  test('forgotten server is not re-added by restoration or token refresh',
      () async {
    final storage = MemoryStorage();
    final first = await harness(storage: storage);
    await first.read(authProvider.notifier).login(_old, 'viewer', 'password');
    await first.read(savedServersProvider.notifier).forget(_old);
    final restarted = await harness(storage: storage);
    restarted.read(authProvider.notifier).reconnectNow();
    for (var i = 0;
        i < 100 && restarted.read(authProvider).valueOrNull!.isReconnecting;
        i++) {
      await Future<void>.delayed(const Duration(milliseconds: 5));
    }
    expect(restarted.read(authProvider).valueOrNull!.isReconnecting, isFalse);
    expect(await restarted.read(savedServersProvider.future), isEmpty);
  });
}

class _Auth extends PlexAuthFake {
  bool failLogin = false, failConfig = false, revoked = false;
  _Auth() {
    approved = true;
  }

  @override
  Future<AuthResponse> login(String serverUrl, String username, String password,
      String deviceName, String hardwareId) async {
    if (failLogin) throw StateError('Sign-in refused');
    return _response;
  }

  @override
  Future<AuthResponse> setup(String serverUrl, String username, String password,
          String deviceName, String hardwareId) async =>
      _response;

  @override
  Future<AuthResponse> redeemConnectToken(String serverUrl, String token,
          String deviceName, String hardwareId) async =>
      _response;

  @override
  Future<AuthResponse> refreshToken(
      String serverUrl, String refreshToken) async {
    if (revoked) {
      final request = RequestOptions(path: '/api/auth/refresh');
      throw DioException(
          requestOptions: request,
          response: Response(requestOptions: request, statusCode: 401));
    }
    return _response;
  }

  @override
  Future<ServerConfig> fetchConfig(String serverUrl, String accessToken) async {
    if (failConfig) throw StateError('Configuration unavailable');
    return ServerConfig(
        services: const AvailableServices(),
        serverName: serverUrl == _new ? 'New server' : 'Old server');
  }

  @override
  Future<BeginLoginResponse> beginPasskeyLogin(String serverUrl) async =>
      const BeginLoginResponse(sessionId: 'flow', options: {
        'challenge': 'Y2hhbGxlbmdl',
        'rpId': 'new.test',
        'allowCredentials': <Map<String, dynamic>>[],
        'userVerification': 'preferred',
      });

  @override
  Future<AuthResponse> finishPasskeyLogin(
          String serverUrl,
          String sessionId,
          Map<String, dynamic> response,
          String deviceName,
          String hardwareId) async =>
      _response;
}

class _Device extends DeviceIdentityService {
  _Device(super.storage);
  @override
  Future<DeviceIdentity> resolve() async => const DeviceIdentity(
      displayName: 'Test device', hardwareId: 'test-device');
}

class _OIDC extends OIDCService {
  _OIDC(super.auth, super.storage);
  @override
  Future<OIDCResult> finish(Uri uri) async => OIDCResult(_new, 'login', {
        'access_token': _response.accessToken,
        'refresh_token': _response.refreshToken,
        'device_id': _response.deviceId,
        'user': _response.user.toJson(),
      });
}

class _Passkeys extends PasskeysPlatform {
  @override
  Future<AvailabilityType> getAvailability() async => AvailabilityTypeIOS(
      hasPasskeySupport: true, isNative: true, hasBiometrics: true);

  @override
  Future<RegisterResponseType> register(RegisterRequestType request) async =>
      throw UnimplementedError();

  @override
  Future<void> cancelCurrentAuthenticatorOperation() async {}
  @override
  Future<AuthenticateResponseType> authenticate(
          AuthenticateRequestType request) async =>
      const AuthenticateResponseType(
        id: 'credential',
        rawId: 'credential',
        clientDataJSON: 'e30',
        authenticatorData: 'e30',
        signature: 'c2ln',
        userHandle: 'dXNlcg',
      );
}
