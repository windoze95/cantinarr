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
}

class _NativeStorageService implements StorageService {
  final _storage = const FlutterSecureStorage(
    aOptions: AndroidOptions(encryptedSharedPreferences: true),
  );

  @override
  Future<String?> read({required String key}) => _storage.read(key: key);

  @override
  Future<void> write({required String key, required String? value}) =>
      _storage.write(key: key, value: value);

  @override
  Future<void> delete({required String key}) => _storage.delete(key: key);
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
}
