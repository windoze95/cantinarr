import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/network/backend_client.dart';
import '../../core/storage/secure_storage.dart';

/// Bridges native APNs registration to the Cantinarr backend.
///
/// On iOS this owns a [MethodChannel] to the native `AppDelegate`, which
/// requests notification authorization, registers with APNs, and reports the
/// device token back. The token is then sent to the backend's device registry.
///
/// This is a pure platform-channel integration (no Firebase): foreground
/// notification presentation is handled natively by the
/// `UNUserNotificationCenterDelegate`. All operations are best-effort and must
/// never block or break the auth flow, so failures are logged and swallowed.
class PushService {
  PushService(this._ref) {
    // Listen for tokens pushed from native (initial registration and APNs
    // token rotation). Re-register whenever the token changes.
    _channel.setMethodCallHandler(_handleNativeCall);
  }

  static const _channel = MethodChannel('codes.julian.cantinarr/push');

  final Ref _ref;

  /// The last APNs token successfully sent to the backend, used to avoid
  /// redundant registration calls when the token hasn't changed.
  String? _registeredToken;

  bool get _isSupported => !kIsWeb && Platform.isIOS;

  Future<dynamic> _handleNativeCall(MethodCall call) async {
    if (call.method == 'onApnsToken') {
      final token = call.arguments as String?;
      if (token != null && token.isNotEmpty && token != _registeredToken) {
        await _sendToken(token);
      }
    }
    return null;
  }

  /// Requests notification permission, obtains the APNs token, and registers
  /// the device with the backend. Safe to call repeatedly; a no-op on
  /// unsupported platforms.
  Future<void> registerForPush() async {
    if (!_isSupported) return;
    try {
      final granted =
          await _channel.invokeMethod<bool>('requestPermission') ?? false;
      if (!granted) {
        debugPrint('Push: notification permission not granted');
        return;
      }
      // The token may already be cached natively from a prior launch; the
      // native side also fires onApnsToken once registration completes.
      final token = await _channel.invokeMethod<String>('getApnsToken');
      if (token != null && token.isNotEmpty) {
        await _sendToken(token);
      }
    } catch (e) {
      debugPrint('Push: registerForPush failed: $e');
    }
  }

  /// Returns the current notification authorization status as reported by the
  /// OS: one of `authorized`, `denied`, `notDetermined`, `provisional`, or
  /// `ephemeral`. Off-iOS (or on any failure) reports `notDetermined`.
  Future<String> authorizationStatus() async {
    if (!_isSupported) return 'notDetermined';
    try {
      return await _channel.invokeMethod<String>('getAuthorizationStatus') ??
          'notDetermined';
    } catch (e) {
      debugPrint('Push: authorizationStatus failed: $e');
      return 'notDetermined';
    }
  }

  /// Opens this app's page in the system Settings so the user can change
  /// notification permissions. A no-op on unsupported platforms.
  Future<void> openSystemSettings() async {
    if (!_isSupported) return;
    try {
      await _channel.invokeMethod<bool>('openNotificationSettings');
    } catch (e) {
      debugPrint('Push: openSystemSettings failed: $e');
    }
  }

  /// Asks the backend to send a test push to this account's own devices and
  /// returns the diagnostic outcome (tokens registered + per-device results).
  /// Throws on transport/HTTP errors (including a 503 when push isn't
  /// configured) so the caller can surface the failure.
  Future<PushTestResult> sendTest() async {
    final dio = _ref.read(backendClientProvider);
    final resp = await dio.post('/api/notifications/test');
    return PushTestResult.fromJson(
        resp.data as Map<String, dynamic>? ?? const {});
  }

  /// Admin-only: send a test push to another user's devices. Mirrors [sendTest]
  /// but targets [userId] via the admin endpoint, so an admin can verify a
  /// specific account's delivery (the self-test can't reach another account).
  /// Throws on error.
  Future<PushTestResult> sendTestToUser(int userId) async {
    final dio = _ref.read(backendClientProvider);
    final resp = await dio.post('/api/admin/users/$userId/test-push');
    return PushTestResult.fromJson(
        resp.data as Map<String, dynamic>? ?? const {});
  }

  /// Removes this device's push token from the backend. Best-effort; call
  /// before clearing auth state on logout.
  Future<void> unregister() async {
    if (!_isSupported) return;
    try {
      final storage = _ref.read(storageServiceProvider);
      final deviceId = await storage.read(key: StorageKeys.deviceId);
      if (deviceId == null || deviceId.isEmpty) return;

      final dio = _ref.read(backendClientProvider);
      await dio.delete('/api/devices/push-token/$deviceId');
      _registeredToken = null;
    } catch (e) {
      debugPrint('Push: unregister failed: $e');
    }
  }

  /// POSTs the APNs token to the backend device registry.
  Future<void> _sendToken(String token) async {
    try {
      final storage = _ref.read(storageServiceProvider);
      final deviceId = await storage.read(key: StorageKeys.deviceId);
      if (deviceId == null || deviceId.isEmpty) {
        debugPrint('Push: no device id; skipping token registration');
        return;
      }

      final dio = _ref.read(backendClientProvider);
      await dio.post('/api/devices/push-token', data: {
        'device_id': deviceId,
        'apns_token': token,
        'platform': 'ios',
      });
      _registeredToken = token;
    } catch (e) {
      debugPrint('Push: failed to send token: $e');
    }
  }
}

/// Provides the app-wide [PushService].
final pushServiceProvider = Provider<PushService>(PushService.new);

/// The outcome of a test-push request: how many tokens the target has
/// registered, plus the gateway's per-device delivery results.
class PushTestResult {
  const PushTestResult({
    required this.tokens,
    required this.sent,
    required this.failed,
    required this.results,
  });

  /// Number of push tokens registered for the target user. Zero is the headline
  /// diagnostic — the device never registered, so nothing could be delivered.
  final int tokens;
  final int sent;
  final int failed;
  final List<PushTestDeviceResult> results;

  factory PushTestResult.fromJson(Map<String, dynamic> json) => PushTestResult(
        tokens: (json['tokens'] as num?)?.toInt() ?? 0,
        sent: (json['sent'] as num?)?.toInt() ?? 0,
        failed: (json['failed'] as num?)?.toInt() ?? 0,
        results: (json['results'] as List<dynamic>?)
                ?.map((e) =>
                    PushTestDeviceResult.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
      );

  /// The first non-empty per-device error reason, if any (e.g. a rejected
  /// token's `BadDeviceToken`).
  String? get firstError {
    for (final r in results) {
      if (r.error.isNotEmpty) return r.error;
    }
    return null;
  }
}

/// One device's delivery outcome within a [PushTestResult].
class PushTestDeviceResult {
  const PushTestDeviceResult({
    required this.ok,
    required this.pruned,
    required this.error,
  });

  final bool ok;
  final bool pruned;
  final String error;

  factory PushTestDeviceResult.fromJson(Map<String, dynamic> json) =>
      PushTestDeviceResult(
        ok: json['ok'] as bool? ?? false,
        pruned: json['pruned'] as bool? ?? false,
        error: json['error'] as String? ?? '',
      );
}

/// Builds a human-readable summary of a [PushTestResult] for a snackbar. Pass
/// [username] for an admin test of another account; omit it for the caller's
/// own self-test.
String describePushTest(PushTestResult r, {String? username}) {
  if (r.tokens == 0) {
    final subject = username == null ? 'You have' : '$username has';
    return '$subject no registered push devices yet. Open the app on the '
        'device (while connected) and allow notifications so it can register.';
  }
  if (r.sent > 0 && r.failed == 0) {
    final n = r.sent == 1 ? '1 device' : '${r.sent} devices';
    return username == null ? 'Test sent to $n.' : 'Test sent to $username ($n).';
  }
  if (r.sent == 0 && r.failed == 0) {
    // Tokens exist locally but the gateway accepted none — usually a desync
    // where the gateway already pruned them.
    return 'No devices were reached — the push gateway has no active token for '
        '${username ?? 'this account'}. Have them reopen the app to re-register.';
  }
  final hint = _apnsHint(r.firstError);
  if (r.sent > 0) {
    return 'Sent to ${r.sent}, but ${r.failed} failed$hint.';
  }
  final n = r.failed == 1 ? '1 device' : '${r.failed} devices';
  return username == null
      ? 'Delivery failed for $n$hint.'
      : '$username: delivery failed for $n$hint.';
}

/// Maps the common APNs rejection reasons to a short hint; otherwise echoes the
/// raw reason in parentheses (or empty when there is none).
String _apnsHint(String? error) {
  if (error == null || error.isEmpty) return '';
  if (error.contains('BadDeviceToken')) {
    return ' (Apple rejected the token — usually a dev build’s sandbox '
        'token sent to production APNs, or a stale token)';
  }
  if (error.contains('Unregistered')) {
    return ' (Apple says the token is no longer valid — the app was removed or '
        'reinstalled; it re-registers on next launch)';
  }
  return ' ($error)';
}
