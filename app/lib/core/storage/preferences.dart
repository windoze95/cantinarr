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

const _approvalsMenuOnlyWhenPendingKey = 'approvals_menu_only_when_pending';
const _issuesMenuOnlyWhenActiveKey = 'issues_menu_only_when_active';
const _agentFixesMenuOnlyWhenAwaitingReviewKey =
    'agent_fixes_menu_only_when_awaiting_review';
const _profileApprovalsMenuOnlyWhenPendingKey =
    'profile_approvals_menu_only_when_pending';

/// A device-local preference that hides an admin queue from the navigation
/// menu while that queue has no active work. The default is false so existing
/// installs keep their always-visible navigation until an admin opts in.
class ConditionalMenuVisibilityNotifier extends StateNotifier<bool> {
  ConditionalMenuVisibilityNotifier(this._key) : super(false) {
    _load();
  }

  final String _key;

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    state = prefs.getBool(_key) ?? false;
  }

  Future<void> set(bool value) async {
    state = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_key, value);
  }
}

/// Whether Approvals appears in the menu only while requests are pending.
final approvalsMenuOnlyWhenPendingProvider =
    StateNotifierProvider<ConditionalMenuVisibilityNotifier, bool>(
  (ref) => ConditionalMenuVisibilityNotifier(
    _approvalsMenuOnlyWhenPendingKey,
  ),
);

/// Whether Issues appears in the menu only while an issue needs attention or
/// is being passively tracked.
final issuesMenuOnlyWhenActiveProvider =
    StateNotifierProvider<ConditionalMenuVisibilityNotifier, bool>(
  (ref) => ConditionalMenuVisibilityNotifier(_issuesMenuOnlyWhenActiveKey),
);

/// Whether Agent fixes appears only while a proposal awaits admin review.
final agentFixesMenuOnlyWhenAwaitingReviewProvider =
    StateNotifierProvider<ConditionalMenuVisibilityNotifier, bool>(
  (ref) => ConditionalMenuVisibilityNotifier(
    _agentFixesMenuOnlyWhenAwaitingReviewKey,
  ),
);

/// Whether Profile approvals appears only while an external agent's
/// quality-profile proposal awaits an admin decision.
final profileApprovalsMenuOnlyWhenPendingProvider =
    StateNotifierProvider<ConditionalMenuVisibilityNotifier, bool>(
  (ref) => ConditionalMenuVisibilityNotifier(
    _profileApprovalsMenuOnlyWhenPendingKey,
  ),
);

const _dismissedAppSkewPairKey = 'dismissed_app_skew_pair';
const _dismissedServerSkewPairKey = 'dismissed_server_skew_pair';

/// A device-local "don't show this exact notice again" slot: one string
/// holding the identity of the dismissed notice (a `version|floor` pair). The
/// notice resurfaces as soon as its identity changes, so a dismissal only ever
/// silences the thing it was for.
class DismissedNoticeNotifier extends StateNotifier<String?> {
  DismissedNoticeNotifier(this._key) : super(null) {
    _load();
  }

  final String _key;

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    state = prefs.getString(_key);
  }

  Future<void> set(String value) async {
    state = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_key, value);
  }
}

/// The `appVersion|serverFloor` pair whose "update this app" skew warning was
/// dismissed; either side changing brings the warning back.
final dismissedAppSkewPairProvider =
    StateNotifierProvider<DismissedNoticeNotifier, String?>(
  (ref) => DismissedNoticeNotifier(_dismissedAppSkewPairKey),
);

/// The `serverVersion|appFloor` pair whose "update the server" skew warning
/// was dismissed; either side changing brings the warning back.
final dismissedServerSkewPairProvider =
    StateNotifierProvider<DismissedNoticeNotifier, String?>(
  (ref) => DismissedNoticeNotifier(_dismissedServerSkewPairKey),
);
