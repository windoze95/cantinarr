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
