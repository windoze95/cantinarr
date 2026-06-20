import 'passkey_service_stub.dart'
    if (dart.library.js_interop) 'passkey_service_web.dart'
    if (dart.library.io) 'passkey_service_native.dart' as platform;

/// Platform-agnostic passkey service.
/// On web: uses navigator.credentials via dart:js_interop.
/// On other platforms: returns unavailable.
class PasskeyService {
  /// Whether WebAuthn/passkeys are supported on this platform.
  static bool isAvailable() => platform.isAvailable();

  /// Platform kind used to match server-side native passkey readiness.
  static String platformKind() => platform.platformKind();

  /// Whether WebAuthn/passkeys are supported on this platform.
  ///
  /// Native platforms need to ask the OS / credential manager, so callers that
  /// control UI availability should prefer this async check.
  static Future<bool> isAvailableAsync() => platform.isAvailableAsync();

  /// Call navigator.credentials.create() with the server-provided options.
  /// Returns the raw credential response as a Map to send back to the server.
  static Future<Map<String, dynamic>> create(
      Map<String, dynamic> options) async {
    return platform.create(options);
  }

  /// Call navigator.credentials.get() with the server-provided options.
  /// Returns the raw assertion response as a Map to send back to the server.
  static Future<Map<String, dynamic>> get(Map<String, dynamic> options) async {
    return platform.get(options);
  }
}
