import 'passkey_service_stub.dart'
    if (dart.library.js_interop) 'passkey_service_web.dart' as platform;

/// Platform-agnostic passkey service.
/// On web: uses navigator.credentials via dart:js_interop.
/// On other platforms: returns unavailable.
class PasskeyService {
  /// Whether WebAuthn/passkeys are supported on this platform.
  static bool isAvailable() => platform.isAvailable();

  /// Call navigator.credentials.create() with the server-provided options.
  /// Returns the raw credential response as a Map to send back to the server.
  static Future<Map<String, dynamic>> create(
      Map<String, dynamic> options) async {
    return platform.create(options);
  }

  /// Call navigator.credentials.get() with the server-provided options.
  /// Returns the raw assertion response as a Map to send back to the server.
  static Future<Map<String, dynamic>> get(
      Map<String, dynamic> options) async {
    return platform.get(options);
  }
}
