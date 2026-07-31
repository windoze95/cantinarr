import 'dart:io' show Platform;

import 'package:device_info_plus/device_info_plus.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:uuid/uuid.dart';

import '../storage/secure_storage.dart';

/// A device's self-reported identity, sourced from the hardware/OS — never the
/// account username or a user-entered field.
class DeviceIdentity {
  const DeviceIdentity({required this.displayName, required this.hardwareId});

  /// Human-friendly label, e.g. "Apple iPhone 16 Pro Max", "Google Pixel 8",
  /// "Yana's MacBook Pro" (desktop computer name), or "Chrome on macOS" (web).
  final String displayName;

  /// Stable per-device identifier used to dedupe reconnects of the same
  /// physical device. Stable across logout. Empty only when nothing usable
  /// could be derived or persisted.
  final String hardwareId;
}

/// Resolves the current [DeviceIdentity] via `device_info_plus`.
///
/// The display name is always read from the device/OS. For the stable id we
/// prefer a native per-device identifier (iOS `identifierForVendor`, desktop
/// machine ids) and fall back to a UUID persisted in secure storage — kept out
/// of the logout purge — for platforms that expose none (web, Android).
class DeviceIdentityService {
  DeviceIdentityService(this._storage);

  final StorageService _storage;
  final DeviceInfoPlugin _plugin = DeviceInfoPlugin();

  DeviceIdentity? _cached;

  /// Resolves (and memoizes) this device's identity. Best-effort: any failure
  /// degrades to a generic platform name rather than throwing into the caller.
  Future<DeviceIdentity> resolve() async {
    final cached = _cached;
    if (cached != null) return cached;

    DeviceIdentity raw;
    try {
      raw = await readDeviceInfo();
    } catch (e) {
      debugPrint('DeviceIdentity: read failed: $e');
      raw = DeviceIdentity(displayName: _fallbackName(), hardwareId: '');
    }

    final hardwareId =
        raw.hardwareId.isNotEmpty ? raw.hardwareId : await persistedId();
    final identity =
        DeviceIdentity(displayName: raw.displayName, hardwareId: hardwareId);
    _cached = identity;
    return identity;
  }

  /// Reads the raw platform identity via `device_info_plus`. Overridable in
  /// tests (the platform branch taken depends on the host OS, so unit tests
  /// stub this seam instead of the plugin); behavior-identical to the previous
  /// private helper.
  @protected
  @visibleForTesting
  Future<DeviceIdentity> readDeviceInfo() async {
    if (kIsWeb) {
      final info = await _plugin.webBrowserInfo;
      final label = webDisplayName(
        info.browserName.name,
        info.platform ?? info.userAgent ?? '',
      );
      // Browsers expose no stable hardware id; the persisted UUID fallback
      // makes web dedup best-effort (per browser profile).
      return DeviceIdentity(displayName: label, hardwareId: '');
    }
    if (Platform.isIOS) {
      final info = await _plugin.iosInfo;
      return DeviceIdentity(
        displayName: appleModelName(info.utsname.machine),
        hardwareId: info.identifierForVendor ?? '',
      );
    }
    if (Platform.isAndroid) {
      final info = await _plugin.androidInfo;
      final label = '${_capitalize(info.manufacturer)} ${info.model}'.trim();
      return DeviceIdentity(displayName: label, hardwareId: '');
    }
    if (Platform.isMacOS) {
      final info = await _plugin.macOsInfo;
      return DeviceIdentity(
        displayName: info.computerName,
        hardwareId: info.systemGUID ?? '',
      );
    }
    if (Platform.isWindows) {
      final info = await _plugin.windowsInfo;
      return DeviceIdentity(
        displayName: info.computerName,
        hardwareId: info.deviceId,
      );
    }
    if (Platform.isLinux) {
      final info = await _plugin.linuxInfo;
      return DeviceIdentity(
        displayName: info.prettyName,
        hardwareId: info.machineId ?? '',
      );
    }
    return DeviceIdentity(displayName: _fallbackName(), hardwareId: '');
  }

  /// Reads (or mints and persists) the fallback UUID used as [hardwareId] on
  /// platforms without a native device identifier (web, Android). Stored under
  /// [StorageKeys.hardwareId], which the logout purge deliberately skips, so
  /// the same physical device dedupes across sessions.
  @visibleForTesting
  Future<String> persistedId() async {
    final existing = await _storage.read(key: StorageKeys.hardwareId);
    if (existing != null && existing.isNotEmpty) return existing;
    final generated = const Uuid().v4();
    await _storage.write(key: StorageKeys.hardwareId, value: generated);
    return generated;
  }

  String _fallbackName() {
    if (kIsWeb) return 'Web Browser';
    try {
      if (Platform.isIOS) return 'iPhone';
      if (Platform.isAndroid) return 'Android';
      if (Platform.isMacOS) return 'Mac';
      if (Platform.isWindows) return 'Windows PC';
      if (Platform.isLinux) return 'Linux';
    } catch (_) {}
    return 'Unknown Device';
  }

  /// Builds the web display label ("Chrome on macOS") from the reported
  /// browser name and a platform/user-agent hint. Exposed for tests; the OS
  /// part is omitted when the hint matches nothing.
  @visibleForTesting
  String webDisplayName(String browserName, String platformHint) {
    final browser = _capitalize(browserName);
    final os = _webOs(platformHint);
    return os.isEmpty ? browser : '$browser on $os';
  }

  String _webOs(String raw) {
    final s = raw.toLowerCase();
    if (s.contains('mac')) return 'macOS';
    if (s.contains('win')) return 'Windows';
    if (s.contains('iphone') || s.contains('ipad') || s.contains('ios')) {
      return 'iOS';
    }
    if (s.contains('android')) return 'Android';
    if (s.contains('linux')) return 'Linux';
    return '';
  }

  String _capitalize(String s) =>
      s.isEmpty ? s : '${s[0].toUpperCase()}${s.substring(1)}';

  /// Maps an iOS hardware identifier (`utsname.machine`, e.g. "iPhone17,2") to a
  /// marketing name ("Apple iPhone 16 Pro Max"). Unmapped models fall back to
  /// the device class; extend this table as new devices ship.
  @visibleForTesting
  String appleModelName(String machine) {
    const models = <String, String>{
      'iPhone12,1': 'iPhone 11',
      'iPhone12,3': 'iPhone 11 Pro',
      'iPhone12,5': 'iPhone 11 Pro Max',
      'iPhone12,8': 'iPhone SE (2nd gen)',
      'iPhone13,1': 'iPhone 12 mini',
      'iPhone13,2': 'iPhone 12',
      'iPhone13,3': 'iPhone 12 Pro',
      'iPhone13,4': 'iPhone 12 Pro Max',
      'iPhone14,2': 'iPhone 13 Pro',
      'iPhone14,3': 'iPhone 13 Pro Max',
      'iPhone14,4': 'iPhone 13 mini',
      'iPhone14,5': 'iPhone 13',
      'iPhone14,6': 'iPhone SE (3rd gen)',
      'iPhone14,7': 'iPhone 14',
      'iPhone14,8': 'iPhone 14 Plus',
      'iPhone15,2': 'iPhone 14 Pro',
      'iPhone15,3': 'iPhone 14 Pro Max',
      'iPhone15,4': 'iPhone 15',
      'iPhone15,5': 'iPhone 15 Plus',
      'iPhone16,1': 'iPhone 15 Pro',
      'iPhone16,2': 'iPhone 15 Pro Max',
      'iPhone17,1': 'iPhone 16 Pro',
      'iPhone17,2': 'iPhone 16 Pro Max',
      'iPhone17,3': 'iPhone 16',
      'iPhone17,4': 'iPhone 16 Plus',
      'iPhone17,5': 'iPhone 16e',
    };
    final mapped = models[machine];
    if (mapped != null) return 'Apple $mapped';
    if (machine.startsWith('iPhone')) return 'Apple iPhone';
    if (machine.startsWith('iPad')) return 'Apple iPad';
    if (machine.startsWith('iPod')) return 'Apple iPod touch';
    if (machine == 'x86_64' || machine == 'arm64' || machine == 'i386') {
      return 'iOS Simulator';
    }
    return 'Apple $machine';
  }
}

/// App-wide [DeviceIdentityService].
final deviceIdentityProvider = Provider<DeviceIdentityService>(
  (ref) => DeviceIdentityService(ref.read(storageServiceProvider)),
);
