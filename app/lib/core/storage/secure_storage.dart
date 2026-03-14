import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Provides access to encrypted key-value storage for API keys and tokens.
final secureStorageProvider = Provider<FlutterSecureStorage>(
  (_) => const FlutterSecureStorage(
    aOptions: AndroidOptions(encryptedSharedPreferences: true),
  ),
);

/// Keys used in secure storage.
class StorageKeys {
  StorageKeys._();

  // Auth tokens
  static const String jwt = 'jwt_access_token';
  static const String refreshToken = 'jwt_refresh_token';
  static const String serverUrl = 'server_url';

  // Legacy keys (kept for migration, will be removed)
  static const String tmdbApiKey = 'tmdb_api_key';
}
