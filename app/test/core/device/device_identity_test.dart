import 'package:cantinarr/core/device/device_identity.dart';
import 'package:cantinarr/core/storage/secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';

/// Exercises [DeviceIdentityService]: the Apple model-name table, web
/// browser/OS labeling, and — critically — the persisted-UUID fallback used on
/// platforms without a native hardware id (web, Android), which must survive
/// logout so reconnects of the same physical device dedupe.
void main() {
  DeviceIdentityService service([Map<String, String?>? data]) =>
      DeviceIdentityService(_FakeStorage(data ?? {}));

  group('appleModelName', () {
    test('maps known identifiers to marketing names', () {
      const expected = <String, String>{
        'iPhone12,1': 'Apple iPhone 11',
        'iPhone14,6': 'Apple iPhone SE (3rd gen)',
        'iPhone16,2': 'Apple iPhone 15 Pro Max',
        'iPhone17,2': 'Apple iPhone 16 Pro Max',
        'iPhone17,5': 'Apple iPhone 16e',
      };
      final svc = service();
      expected.forEach((machine, name) {
        expect(svc.appleModelName(machine), name, reason: machine);
      });
    });

    test('falls back to the device class for unmapped models', () {
      final svc = service();
      expect(svc.appleModelName('iPhone99,9'), 'Apple iPhone');
      expect(svc.appleModelName('iPad13,1'), 'Apple iPad');
      expect(svc.appleModelName('iPod9,1'), 'Apple iPod touch');
    });

    test('recognizes simulators and passes anything else through raw', () {
      final svc = service();
      expect(svc.appleModelName('arm64'), 'iOS Simulator');
      expect(svc.appleModelName('x86_64'), 'iOS Simulator');
      expect(svc.appleModelName('i386'), 'iOS Simulator');
      expect(svc.appleModelName('RealityDevice14,1'),
          'Apple RealityDevice14,1',
          reason: 'unknown ids keep the raw identifier as a usable fallback');
    });
  });

  group('webDisplayName', () {
    test('labels browser and OS from a platform hint', () {
      final svc = service();
      expect(svc.webDisplayName('chrome', 'MacIntel'), 'Chrome on macOS');
      expect(svc.webDisplayName('firefox', 'Win32'), 'Firefox on Windows');
      expect(svc.webDisplayName('edge', 'Linux x86_64'), 'Edge on Linux');
    });

    test('detects mobile platforms from navigator.platform-style hints', () {
      final svc = service();
      expect(svc.webDisplayName('safari', 'iPhone'), 'Safari on iOS');
      expect(svc.webDisplayName('safari', 'iPad'), 'Safari on iOS');
      expect(
        svc.webDisplayName('chrome', 'Mozilla/5.0 (Android 15; Mobile)'),
        'Chrome on Android',
      );
    });

    test('omits the OS when the hint matches nothing', () {
      expect(service().webDisplayName('chrome', ''), 'Chrome');
      expect(service().webDisplayName('opera', 'FreeBSD'), 'Opera');
    });
  });

  group('persistedId', () {
    test('mints a UUID once and returns the same value afterwards', () async {
      final data = <String, String?>{};
      final svc = service(data);

      final first = await svc.persistedId();
      expect(first, isNotEmpty);
      expect(data[StorageKeys.hardwareId], first,
          reason: 'the generated id must be persisted');
      expect(await svc.persistedId(), first);
    });

    test('returns an already-persisted id without overwriting it', () async {
      final data = <String, String?>{StorageKeys.hardwareId: 'stable-id'};
      expect(await service(data).persistedId(), 'stable-id');
      expect(data[StorageKeys.hardwareId], 'stable-id');
    });

    test('survives a logout purge (which deliberately skips hardware_id)',
        () async {
      final data = <String, String?>{};
      final original = await service(data).persistedId();

      // Mirror AuthNotifier._clearStorage: every auth key is deleted on
      // logout, but StorageKeys.hardwareId is deliberately not among them.
      for (final key in [
        StorageKeys.serverUrl,
        StorageKeys.jwt,
        StorageKeys.refreshToken,
        StorageKeys.refreshTokenBackup,
        StorageKeys.deviceId,
        StorageKeys.sessionUser,
        StorageKeys.sessionConnection,
      ]) {
        data.remove(key);
      }

      // A fresh service (new session after re-login) sees the same id.
      expect(await service(data).persistedId(), original);
    });
  });

  group('resolve', () {
    test('uses the native hardware id when the platform provides one',
        () async {
      final data = <String, String?>{};
      final svc = _StubbedIdentityService(
        _FakeStorage(data),
        identity: const DeviceIdentity(
          displayName: 'Apple iPhone 16 Pro Max',
          hardwareId: 'vendor-id-1',
        ),
      );

      final identity = await svc.resolve();
      expect(identity.displayName, 'Apple iPhone 16 Pro Max');
      expect(identity.hardwareId, 'vendor-id-1');
      expect(data.containsKey(StorageKeys.hardwareId), isFalse,
          reason: 'no fallback UUID is minted when a native id exists');
    });

    test('falls back to the persisted UUID when the platform has no id '
        '(web/Android) and keeps it across sessions', () async {
      final data = <String, String?>{};
      const identity =
          DeviceIdentity(displayName: 'Google Pixel 8', hardwareId: '');

      final first =
          await _StubbedIdentityService(_FakeStorage(data), identity: identity)
              .resolve();
      expect(first.displayName, 'Google Pixel 8');
      expect(first.hardwareId, isNotEmpty);
      expect(first.hardwareId, data[StorageKeys.hardwareId]);

      // Logout: auth keys purged, hardware_id retained.
      data
        ..remove(StorageKeys.jwt)
        ..remove(StorageKeys.refreshToken)
        ..remove(StorageKeys.deviceId);

      final second =
          await _StubbedIdentityService(_FakeStorage(data), identity: identity)
              .resolve();
      expect(second.hardwareId, first.hardwareId,
          reason: 'the same physical device must dedupe across sessions');
    });

    test('degrades to a generic name plus the persisted UUID when the '
        'platform read fails', () async {
      final data = <String, String?>{};
      final svc = _StubbedIdentityService(
        _FakeStorage(data),
        error: Exception('platform unavailable'),
      );

      final identity = await svc.resolve();
      expect(identity.displayName, isNotEmpty,
          reason: 'a read failure must never surface an empty name');
      expect(identity.hardwareId, data[StorageKeys.hardwareId]);
    });

    test('memoizes the resolved identity per service instance', () async {
      final storage = _FakeStorage({});
      final svc = _StubbedIdentityService(
        storage,
        identity: const DeviceIdentity(displayName: 'Pixel', hardwareId: ''),
      );

      final first = await svc.resolve();
      final readsAfterFirst = storage.reads;
      final second = await svc.resolve();
      expect(identical(first, second), isTrue);
      expect(storage.reads, readsAfterFirst,
          reason: 'a memoized resolve must not hit storage again');
    });
  });
}

/// [DeviceIdentityService] with the platform read stubbed out: the branch the
/// real [DeviceIdentityService.readDeviceInfo] takes depends on the host OS,
/// so tests inject the raw identity (or a failure) at that seam instead.
class _StubbedIdentityService extends DeviceIdentityService {
  _StubbedIdentityService(super.storage, {this.identity, this.error});

  final DeviceIdentity? identity;
  final Object? error;

  @override
  Future<DeviceIdentity> readDeviceInfo() async {
    final failure = error;
    if (failure != null) throw failure;
    return identity!;
  }
}

/// In-memory [StorageService] over a caller-owned map, with a read counter so
/// memoization can be asserted.
class _FakeStorage implements StorageService {
  _FakeStorage(this._data);

  final Map<String, String?> _data;
  int reads = 0;

  @override
  Future<String?> read({required String key}) async {
    reads++;
    return _data[key];
  }

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

  @override
  Future<void> hardenAuthKeys() async {}
}
