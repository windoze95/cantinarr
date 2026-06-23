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
    _service =
        RequestSettingsService(backendDio: _ref.read(backendClientProvider));
    _bind();
    // Re-bind on login/logout/role change without rebuilding the provider.
    _ref.listen(authProvider, (_, __) => _bind());
  }

  final Ref _ref;
  late final RequestSettingsService _service;
  StreamSubscription<WsEvent>? _sub;
  bool _isAdmin = false;

  /// (Re)attaches to the live event stream when the admin status changes.
  void _bind() {
    final admin =
        _ref.read(authProvider).valueOrNull?.user?.isAdmin ?? false;
    if (admin == _isAdmin) return; // no change
    _isAdmin = admin;
    _sub?.cancel();
    _sub = null;
    if (!admin) {
      _set(0);
      return;
    }
    refresh();
    _sub = _ref
        .read(realtimeEventsProvider)
        .where((e) => e.type == 'request_pending')
        .listen(_onPending);
  }

  /// Applies the authoritative count carried by a `request_pending` event,
  /// falling back to an optimistic +1 if the server didn't include it.
  void _onPending(WsEvent event) {
    final raw = event.data['pending_count'];
    if (raw is num) {
      _set(raw.toInt());
    } else {
      _set(state + 1);
    }
  }

  /// Re-reads the queue depth from the backend. Call after an approve/deny so
  /// the badges reflect the resolved queue immediately.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    try {
      final pending = await _service.listPending();
      _set(pending.length);
    } catch (_) {
      // Best-effort: keep the last known count on a transient failure.
    }
  }

  /// Sets the count directly from a caller that already holds the authoritative
  /// queue (the approvals screen), avoiding a redundant fetch.
  void setCount(int value) => _set(value);

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
