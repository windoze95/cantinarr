import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/network/websocket_client.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/issues_service.dart';

/// Shared [IssuesService] bound to the authenticated backend Dio client.
final issuesServiceProvider = Provider<IssuesService>((ref) {
  return IssuesService(backendDio: ref.watch(backendClientProvider));
});

/// Tracks the number of open issues awaiting an admin, for admins only.
///
/// Drives the drawer "Issues" entry badge (and could feed an app-bar dot the
/// same way the approvals count does). It is seeded from REST, kept live by
/// `issue_created`/`agent_action_pending` websocket events, and refreshed
/// after a dismiss. Non-admin accounts always report 0 and never call the
/// admin-only endpoint. Mirrors `PendingApprovalsNotifier`.
class OpenIssuesNotifier extends StateNotifier<int> {
  OpenIssuesNotifier(this._ref) : super(0) {
    _service = _ref.read(issuesServiceProvider);
    _bind();
    // Re-bind on login/logout/role change without rebuilding the provider.
    _ref.listen(authProvider, (_, __) => _bind());
  }

  final Ref _ref;
  late final IssuesService _service;
  StreamSubscription<WsEvent>? _sub;
  bool _isAdmin = false;

  /// (Re)attaches to the live event stream when the admin status changes.
  void _bind() {
    final admin = _ref.read(authProvider).valueOrNull?.user?.isAdmin ?? false;
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
        .where((e) =>
            e.type == 'issue_created' || e.type == 'agent_action_pending')
        .listen(_onPing);
  }

  /// Applies the authoritative count carried by the event when present,
  /// otherwise refetches (the badge must stay correct, and a ping doesn't
  /// always carry the queue depth for issues).
  void _onPing(WsEvent event) {
    final raw = event.data['open_count'];
    if (raw is num) {
      _set(raw.toInt());
    } else {
      refresh();
    }
  }

  /// Re-reads the open-issue count from the backend. Call after a dismiss so
  /// the badge reflects the resolved queue immediately.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    try {
      final issues = await _service.listIssues();
      final open = issues.where((i) => !i.status.isTerminal).length;
      _set(open);
    } catch (_) {
      // Best-effort: keep the last known count on a transient failure (covers
      // the pre-merge 404 too).
    }
  }

  /// Sets the count directly from a caller that already holds the authoritative
  /// list (the issues screen), avoiding a redundant fetch.
  void setCount(int value) => _set(value);

  void _set(int value) {
    state = value < 0 ? 0 : value;
  }

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }
}

/// Open-issue count for the signed-in admin (0 for non-admins).
final openIssuesProvider =
    StateNotifierProvider<OpenIssuesNotifier, int>(OpenIssuesNotifier.new);
