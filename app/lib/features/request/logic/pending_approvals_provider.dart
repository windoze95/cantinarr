import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/network/websocket_client.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../auth/logic/auth_provider.dart';
import '../../notifications/push_service.dart';
import '../../settings/data/request_settings_service.dart';

/// Tracks the number of media requests awaiting admin approval, for admins only.
///
/// The count drives the in-app approvals badge (drawer entry + hamburger dot)
/// and the home-screen app-icon badge. It is seeded from REST, kept live by
/// `request_pending` websocket events (which carry the authoritative
/// `pending_count`), and refreshed after an approve/deny. Non-admin accounts
/// always report 0 and never call the admin-only endpoint.
class PendingApprovalsNotifier extends StateNotifier<int> {
  PendingApprovalsNotifier(this._ref) : super(0) {
    _bind();
    // Re-bind on login/logout/role change without rebuilding the provider.
    _ref.listen(authProvider, (_, __) => _bind(force: true));
  }

  final Ref _ref;
  StreamSubscription<WsEvent>? _sub;
  bool _isAdmin = false;
  int _refreshEpoch = 0;

  /// (Re)attaches to the live event stream when the admin status changes.
  void _bind({bool force = false}) {
    final admin = _ref.read(authProvider).valueOrNull?.user?.isAdmin ?? false;
    // Auth changes can replace the backend client while the role stays admin.
    if (!force && admin == _isAdmin) return;
    _refreshEpoch++;
    _isAdmin = admin;
    _sub?.cancel();
    _sub = null;
    if (!admin) {
      _ref.read(pendingApprovalsLoadedProvider.notifier).state = false;
      _ref.read(pendingApprovalsStaleProvider.notifier).state = false;
      _set(0);
      return;
    }
    // An admin-to-admin re-bind (token refresh, resume, client swap) keeps
    // the last count authoritative while refresh() re-reads it. Resetting
    // the loaded flag here would flash every conditional menu entry
    // fail-open on each auth emission.
    refresh();
    _sub = _ref
        .read(realtimeEventsProvider)
        .where((e) => e.type == 'request_pending')
        .listen(_onPending);
  }

  /// Applies the authoritative count carried by a `request_pending` event,
  /// falling back to an optimistic +1 if the server didn't include it.
  void _onPending(WsEvent event) {
    _refreshEpoch++;
    final raw = event.data['pending_count'];
    if (raw is num) {
      _set(raw.toInt());
    } else {
      _set(state + 1);
    }
    _ref.read(pendingApprovalsLoadedProvider.notifier).state = true;
    _ref.read(pendingApprovalsStaleProvider.notifier).state = false;
  }

  /// Re-reads the queue depth from the backend. Call after an approve/deny so
  /// the badges reflect the resolved queue immediately.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    final epoch = ++_refreshEpoch;
    try {
      final service = RequestSettingsService(
        backendDio: _ref.read(backendClientProvider),
      );
      final pending = await service.listPending();
      if (!_isAdmin || epoch != _refreshEpoch) return;
      _set(pending.length);
      _ref.read(pendingApprovalsLoadedProvider.notifier).state = true;
      _ref.read(pendingApprovalsStaleProvider.notifier).state = false;
    } catch (_) {
      if (!_isAdmin || epoch != _refreshEpoch) return;
      // Preserve the last badge count, but fail open for conditional menu
      // visibility because the queue's emptiness is no longer authoritative.
      _ref.read(pendingApprovalsLoadedProvider.notifier).state = false;
      _ref.read(pendingApprovalsStaleProvider.notifier).state = true;
    }
  }

  /// Sets the count directly from a caller that already holds the authoritative
  /// queue (the approvals screen), avoiding a redundant fetch.
  void setCount(int value) {
    _refreshEpoch++;
    _set(value);
    _ref.read(pendingApprovalsLoadedProvider.notifier).state = true;
    _ref.read(pendingApprovalsStaleProvider.notifier).state = false;
  }

  void _set(int value) {
    final next = value < 0 ? 0 : value;
    state = next;
    // Mirror to the home-screen app icon so it matches the in-app badge.
    _ref.read(pushServiceProvider).setBadgeCount(next);
  }

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }
}

/// Pending-approval count for the signed-in admin (0 for non-admins).
final pendingApprovalsProvider =
    StateNotifierProvider<PendingApprovalsNotifier, int>(
  PendingApprovalsNotifier.new,
);

/// Whether the approvals count has been read successfully at least once.
final pendingApprovalsLoadedProvider = StateProvider<bool>((ref) => false);

/// Whether the approvals count is currently unknowable: the last refresh
/// failed and nothing authoritative has arrived since. The conditional menu
/// entry fails open on this — never on a load that is merely in flight,
/// which is what made cold starts flash every conditional entry.
final pendingApprovalsStaleProvider = StateProvider<bool>((ref) => false);
