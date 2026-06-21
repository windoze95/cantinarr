import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Async provider for SharedPreferences.
final sharedPreferencesProvider = FutureProvider<SharedPreferences>(
  (_) => SharedPreferences.getInstance(),
);

const _requestNotificationsKey = 'request_notifications_enabled';

/// Whether in-app notifications for request approvals/denials are shown.
/// Stored locally on the device and defaults to enabled; users can mute them
/// from Settings without affecting the passively-updated request status.
class RequestNotificationsNotifier extends StateNotifier<bool> {
  RequestNotificationsNotifier() : super(true) {
    _load();
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    state = prefs.getBool(_requestNotificationsKey) ?? true;
  }

  Future<void> set(bool value) async {
    state = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_requestNotificationsKey, value);
  }
}

final requestNotificationsEnabledProvider =
    StateNotifierProvider<RequestNotificationsNotifier, bool>(
  (ref) => RequestNotificationsNotifier(),
);
