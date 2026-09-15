import 'dart:async';
import 'package:cantinarr/core/device/device_identity.dart';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/storage/secure_storage.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/data/plex_auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/auth/logic/saved_servers_provider.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'oidc_service_test.dart' show MemoryStorage;
import 'plex_auth_service_test.dart' show PlexAuthFake;

class AdoptionAuth extends PlexAuthFake {
  bool failConfig = false;
  Completer<void>? holdConfig;
  bool configEntered = false;
  @override
  Future<AuthResponse> refreshToken(
          String serverUrl, String refreshToken) async =>
      const AuthResponse(
          accessToken: 'old-access',
          refreshToken: 'old-refresh',
          deviceId: 'old-device',
          user: UserProfile(id: 1, username: 'old-user', role: 'user'));
  @override
  Future<ServerConfig> fetchConfig(String serverUrl, String accessToken) async {
    if (serverUrl == 'https://new.example') {
      configEntered = true;
      if (holdConfig != null) await holdConfig!.future;
      if (failConfig) throw StateError('Configuration temporarily unavailable');
    }
    return ServerConfig(
        serverName: serverUrl, services: const AvailableServices(radarr: true));
  }
}

class AdoptionStorage extends MemoryStorage {
  bool failNextToken = false;
  @override
  Future<void> write({required String key, required String? value}) async {
    if (failNextToken && key == StorageKeys.refreshToken) {
      failNextToken = false;
      throw StateError('Secure storage temporarily unavailable');
    }
    await super.write(key: key, value: value);
  }
}

class HeldDeviceIdentity extends DeviceIdentityService {
  final completion = Completer<DeviceIdentity>();
  HeldDeviceIdentity(super.storage);
  @override
  Future<DeviceIdentity> resolve() => completion.future;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));
  test('cancellation during device lookup never starts a new attempt',
      () async {
    final storage = MemoryStorage(), auth = AdoptionAuth();
    final identity = HeldDeviceIdentity(storage);
    final service = PlexAuthService(auth, storage, isWeb: false);
    final container = ProviderContainer(overrides: [
      authServiceProvider.overrideWithValue(auth),
      storageServiceProvider.overrideWithValue(storage),
      deviceIdentityProvider.overrideWithValue(identity),
      plexAuthServiceProvider.overrideWithValue(service),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    final start =
        container.read(authProvider.notifier).startPlex('https://new.example');
    await service.cancel();
    final cancelled = expectLater(start, throwsStateError);
    identity.completion.complete(
        const DeviceIdentity(displayName: 'Test', hardwareId: 'test'));
    await cancelled;
    expect(auth.beginData, isNull);
    expect(await service.load(), isNull);
  });
  for (final mode in [
    'success',
    'config-failure',
    'cancel-during-config',
    'storage-failure'
  ]) {
    test('Plex session adoption: $mode', () async {
      final auth = AdoptionAuth()..approved = true;
      final storage = AdoptionStorage();
      storage.values.addAll({
        StorageKeys.serverUrl: 'https://old.example',
        StorageKeys.jwt: 'old-access',
        StorageKeys.refreshToken: 'old-refresh',
        StorageKeys.deviceId: 'old-device'
      });
      final service = PlexAuthService(auth, storage, isWeb: false);
      final container = ProviderContainer(overrides: [
        authServiceProvider.overrideWithValue(auth),
        storageServiceProvider.overrideWithValue(storage),
        plexAuthServiceProvider.overrideWithValue(service)
      ]);
      addTearDown(container.dispose);
      expect(
          (await container.read(authProvider.future)).isAuthenticated, isTrue);
      await service.start('https://new.example');
      storage.failNextToken = mode == 'storage-failure';
      auth.failConfig = mode == 'config-failure';
      if (mode == 'cancel-during-config') auth.holdConfig = Completer<void>();
      final completion = container.read(authProvider.notifier).checkPlex();
      if (mode == 'cancel-during-config') {
        while (!auth.configEntered) {
          await Future<void>.delayed(Duration.zero);
        }
        await service.cancel();
        auth.holdConfig!.complete();
      }
      if (mode == 'success') {
        expect(await completion, 'login');
        expect(container.read(authProvider).valueOrNull!.connection!.serverUrl,
            'https://new.example');
        expect(storage.values[StorageKeys.jwt], 'new-access');
        expect(await service.load(), isNull);
      } else {
        await expectLater(completion, throwsStateError);
        expect(container.read(authProvider).valueOrNull!.connection!.serverUrl,
            'https://old.example');
        expect(storage.values[StorageKeys.jwt], 'old-access');
        if (mode == 'config-failure' || mode == 'storage-failure') {
          expect(auth.logouts, 0);
          expect(storage.values[StorageKeys.serverUrl], 'https://old.example');
          expect(storage.values[StorageKeys.refreshToken], 'old-refresh');
          expect(storage.values[StorageKeys.deviceId], 'old-device');
        }
      }
      expect((await container.read(savedServersProvider.future)).map((s) => s.url),
          mode == 'success'
              ? ['https://new.example', 'https://old.example']
              : ['https://old.example'],
          reason: 'only completed Plex sign-in may change the saved default');
    });
  }
}
