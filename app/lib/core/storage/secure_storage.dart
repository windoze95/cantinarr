import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Platform-aware key-value storage.
///
/// On native platforms, uses FlutterSecureStorage (Keychain / EncryptedSharedPreferences).
/// On web, uses SharedPreferences (localStorage) because FlutterSecureStorage's
/// Web Crypto API requires HTTPS, which self-hosted LAN deployments often lack.
abstract class StorageService {
  Future<String?> read({required String key});
  Future<void> write({required String key, required String? value});
  Future<void> delete({required String key});

  /// Best-effort, one-time upgrade of already-stored auth items to the
  /// current protection class. Call only after a successful [read] of the
  /// auth keys (i.e. when the store is known to be readable).
  Future<void> hardenAuthKeys();
}

class _NativeStorageService implements StorageService {
  /// `first_unlock` instead of the plugin default (`unlocked`): iOS prewarms
  /// and background-launches the app while the device is locked, where
  /// `unlocked`-class keychain items are unreadable — which used to surface
  /// as users being "logged out" at launch with their tokens still present.
  /// `first_unlock` items are readable any time after the first unlock since
  /// boot, and are included in encrypted backups so a session survives
  /// migration to a new phone.
  final _storage = const FlutterSecureStorage(
    aOptions: AndroidOptions(encryptedSharedPreferences: true),
    iOptions: IOSOptions(accessibility: KeychainAccessibility.first_unlock),
    mOptions: MacOsOptions(accessibility: KeychainAccessibility.first_unlock),
  );

  /// Non-secure marker (readable even while the keychain is locked) recording
  /// that existing items were rewritten under the current protection class.
  static const _hardenedMarker = 'cantinarr_secure_storage_first_unlock';

  @override
  Future<String?> read({required String key}) => _storage.read(key: key);

  @override
  Future<void> write({required String key, required String? value}) =>
      _storage.write(key: key, value: value);

  @override
  Future<void> delete({required String key}) => _storage.delete(key: key);

  @override
  Future<void> hardenAuthKeys() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      if (prefs.getBool(_hardenedMarker) == true) return;

      // The plugin migrates an existing item's protection class by deleting
      // and re-adding it, so copy the refresh token to its backup key FIRST:
      // a brand-new item is a single add (no delete window), guaranteeing the
      // one value that cannot be re-minted client-side survives a crash at
      // any point during the rewrite below.
      final refreshToken = await read(key: StorageKeys.refreshToken);
      if (refreshToken != null) {
        await write(key: StorageKeys.refreshTokenBackup, value: refreshToken);
      }

      for (final key in [
        StorageKeys.serverUrl,
        StorageKeys.jwt,
        StorageKeys.refreshToken,
        StorageKeys.deviceId,
        StorageKeys.hardwareId,
        StorageKeys.sessionUser,
        StorageKeys.sessionConnection,
      ]) {
        final value = await read(key: key);
        if (value != null) {
          await write(key: key, value: value);
        }
      }

      await prefs.setBool(_hardenedMarker, true);
    } catch (e) {
      // Never let hardening interfere with the session; retry next launch.
      debugPrint('Secure storage hardening deferred: $e');
    }
  }
}

class _WebStorageService implements StorageService {
  static const _prefix = 'cantinarr_';

  @override
  Future<String?> read({required String key}) async {
    final prefs = await SharedPreferences.getInstance();
    return prefs.getString('$_prefix$key');
  }

  @override
  Future<void> write({required String key, required String? value}) async {
    final prefs = await SharedPreferences.getInstance();
    if (value == null) {
      await prefs.remove('$_prefix$key');
    } else {
      await prefs.setString('$_prefix$key', value);
    }
  }

  @override
  Future<void> delete({required String key}) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove('$_prefix$key');
  }

  @override
  Future<void> hardenAuthKeys() async {
    // localStorage has no protection classes.
  }
}

/// Provides access to token/credential storage.
final storageServiceProvider = Provider<StorageService>(
  (_) => kIsWeb ? _WebStorageService() : _NativeStorageService(),
);

/// Keys used in secure storage.
class StorageKeys {
  StorageKeys._();

  // Auth tokens
  static const String jwt = 'jwt_access_token';
  static const String refreshToken = 'jwt_refresh_token';
  static const String serverUrl = 'server_url';
  static const String deviceId = 'device_id';

  // Redundant copy of the refresh token — the single value whose loss ends a
  // passwordless session. Written alongside every refresh-token write and
  // consulted when the primary key comes back empty, so one corrupted or
  // dropped keychain item can never log a user out.
  static const String refreshTokenBackup = 'jwt_refresh_token_backup';

  // Stable per-device id (persisted; deliberately NOT cleared on logout) used
  // to dedupe reconnects of the same physical device when the platform exposes
  // no native hardware identifier (e.g. web, Android).
  static const String hardwareId = 'hardware_id';

  // Cached session snapshot (user profile + server config, no secrets) so a
  // cold launch can restore an optimistic, usable session and validate it in
  // the background instead of flashing the login screen while offline.
  static const String sessionUser = 'session_user';
  static const String sessionConnection = 'session_connection';
}
