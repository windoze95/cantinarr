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

const _plexGuideKey = 'plex_guide_enabled';

/// Whether the "Watch on Plex" guide appears in the menu and Settings.
/// Stored locally on the device and defaults to enabled; users who are
/// already set up can hide it from the guide itself or from Settings.
class PlexGuideNotifier extends StateNotifier<bool> {
  PlexGuideNotifier() : super(true) {
    _load();
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    state = prefs.getBool(_plexGuideKey) ?? true;
  }

  Future<void> set(bool value) async {
    state = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_plexGuideKey, value);
  }
}

final plexGuideEnabledProvider =
    StateNotifierProvider<PlexGuideNotifier, bool>(
  (ref) => PlexGuideNotifier(),
);

const _setupReminderKey = 'setup_reminder_enabled';

/// Whether the drawer shows a "Setup checklist" reminder while features
/// remain unconfigured. Admins who have deliberately skipped features can
/// mute it from the wizard; the Settings tile always remains.
class SetupReminderNotifier extends StateNotifier<bool> {
  SetupReminderNotifier() : super(true) {
    _load();
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    state = prefs.getBool(_setupReminderKey) ?? true;
  }

  Future<void> set(bool value) async {
    state = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_setupReminderKey, value);
  }
}

final setupReminderEnabledProvider =
    StateNotifierProvider<SetupReminderNotifier, bool>(
  (ref) => SetupReminderNotifier(),
);
