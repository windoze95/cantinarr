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

/// The two non-terminal issue buckets used by the navigation. Tracking stays
/// separate because it keeps the Issues entry reachable without contributing
/// to the attention badge.
class IssueQueueCounts {
  const IssueQueueCounts({
    this.needsAttention = 0,
    this.tracking = 0,
    this.hasLoaded = false,
  });

  final int needsAttention;
  final int tracking;
  final bool hasLoaded;

  int get active => needsAttention + tracking;
}

/// Tracks the number of issues needing attention or being tracked, for admins.
///
/// [openIssuesProvider] drives the actionable badge, while
/// [activeIssuesProvider] also includes passive arr recovery so a conditionally
/// visible menu entry does not disappear during tracking. Both are seeded from
/// one REST request and kept live by issue/action WebSocket events. Non-admin
/// accounts always report zero and never call the admin-only endpoint.
class IssueQueueCountsNotifier extends StateNotifier<IssueQueueCounts> {
  IssueQueueCountsNotifier(this._ref) : super(const IssueQueueCounts()) {
    _bind();
    // Re-bind on login/logout/role change without rebuilding the provider.
    _ref.listen(authProvider, (_, __) => _bind(force: true));
  }

  final Ref _ref;
  StreamSubscription<WsEvent>? _sub;
  Timer? _refreshDebounce;
  bool _isAdmin = false;
  int _refreshEpoch = 0;

  /// (Re)attaches to the live event stream when the admin status changes.
  void _bind({bool force = false}) {
    final admin = _ref.read(authProvider).valueOrNull?.user?.isAdmin ?? false;
    // Auth changes can replace both Dio and WebSocket clients even when the
    // role stays admin (server switch, token refresh, reconnect).
    if (!force && admin == _isAdmin) return;
    _refreshEpoch++;
    _isAdmin = admin;
    _sub?.cancel();
    _sub = null;
    if (!admin) {
      _set(const IssueQueueCounts());
      return;
    }
    refresh();
    _sub = _ref
        .read(realtimeEventsProvider)
        .where((e) =>
            e.type == 'issue_created' ||
            e.type == 'issue_updated' ||
            e.type == 'agent_action_pending' ||
            e.type == 'agent_action_terminal' ||
            e.type == 'agent_action_superseded')
        .listen(_onPing);
  }

  /// Refetches rather than applying the legacy `open_count` event field: that
  /// total cannot tell actionable issues from passive tracking on an older
  /// server. The REST list lets the app derive the attention badge safely.
  void _onPing(WsEvent _) {
    _refreshDebounce?.cancel();
    _refreshDebounce = Timer(const Duration(milliseconds: 300), refresh);
  }

  /// Re-reads the attention count from the backend. Passively observed/retried
  /// issues remain open but do not contribute to the drawer badge.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    final epoch = ++_refreshEpoch;
    try {
      // Resolve the service for every request. The authenticated Dio client is
      // replaced on reconnect/server switch, so caching this provider value
      // would keep calling the old server.
      final issues = await _ref.read(issuesServiceProvider).listIssues();
      if (!_isAdmin || epoch != _refreshEpoch) return;
      _set(IssueQueueCounts(
        needsAttention:
            issues.where((issue) => issue.status.needsAttention).length,
        tracking: issues.where((issue) => issue.status.isTracking).length,
        hasLoaded: true,
      ));
    } catch (_) {
      if (!_isAdmin || epoch != _refreshEpoch) return;
      // Preserve the last badge counts, but fail open for conditional menu
      // visibility because the queue's emptiness is no longer authoritative.
      _set(IssueQueueCounts(
        needsAttention: state.needsAttention,
        tracking: state.tracking,
      ));
    }
  }

  /// Sets both counts from a caller that already holds the authoritative list
  /// (the issues screen), avoiding a redundant fetch.
  void setCounts({required int needsAttention, required int tracking}) {
    _refreshEpoch++;
    _set(IssueQueueCounts(
      needsAttention: needsAttention,
      tracking: tracking,
      hasLoaded: true,
    ));
  }

  void _set(IssueQueueCounts value) {
    state = IssueQueueCounts(
      needsAttention: value.needsAttention < 0 ? 0 : value.needsAttention,
      tracking: value.tracking < 0 ? 0 : value.tracking,
      hasLoaded: value.hasLoaded,
    );
  }

  @override
  void dispose() {
    _sub?.cancel();
    _refreshDebounce?.cancel();
    super.dispose();
  }
}

final issueQueueCountsProvider =
    StateNotifierProvider<IssueQueueCountsNotifier, IssueQueueCounts>(
  IssueQueueCountsNotifier.new,
);

/// Attention-needed issue count for badges (zero for non-admins).
final openIssuesProvider = Provider<int>(
  (ref) => ref.watch(
    issueQueueCountsProvider.select((counts) => counts.needsAttention),
  ),
);

/// Non-terminal issue count for conditional menu visibility. Unlike the badge,
/// this includes issues in passive Tracking states.
final activeIssuesProvider = Provider<int>(
  (ref) => ref.watch(
    issueQueueCountsProvider.select((counts) => counts.active),
  ),
);

/// Whether the issue counts have an authoritative snapshot. Conditional menu
/// visibility fails open until this is true.
final issueQueueCountsLoadedProvider = Provider<bool>(
  (ref) => ref.watch(
    issueQueueCountsProvider.select((counts) => counts.hasLoaded),
  ),
);

/// Tracks the number of agent-proposed actions awaiting an admin decision, for
/// admins only.
///
/// Drives the drawer "Agent fixes" entry badge. It is seeded from REST, kept
/// live by action and issue lifecycle events, and refreshed after an
/// approve/deny. Non-admin accounts always report 0 and never call the
/// admin-only endpoint. Mirrors `PendingApprovalsNotifier`.
class PendingAgentActionsNotifier extends StateNotifier<int> {
  PendingAgentActionsNotifier(this._ref) : super(0) {
    _bind();
    _ref.listen(authProvider, (_, __) => _bind(force: true));
  }

  final Ref _ref;
  StreamSubscription<WsEvent>? _sub;
  Timer? _refreshDebounce;
  bool _isAdmin = false;
  int _refreshEpoch = 0;

  void _bind({bool force = false}) {
    final admin = _ref.read(authProvider).valueOrNull?.user?.isAdmin ?? false;
    if (!force && admin == _isAdmin) return;
    _refreshEpoch++;
    _isAdmin = admin;
    _ref.read(pendingAgentActionsLoadedProvider.notifier).state = false;
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
            e.type == 'agent_action_pending' ||
            e.type == 'agent_action_decided' ||
            e.type == 'agent_action_terminal' ||
            e.type == 'agent_action_superseded' ||
            e.type == 'issue_updated')
        .listen(_onPing);
  }

  /// Applies the authoritative `pending_count` an `agent_action_pending` event
  /// carries; otherwise (a decided event, or a ping without the count) refetch.
  void _onPing(WsEvent event) {
    final raw = event.data['pending_count'];
    if (raw is num) {
      _refreshDebounce?.cancel();
      _refreshEpoch++;
      _set(raw.toInt());
      _ref.read(pendingAgentActionsLoadedProvider.notifier).state = true;
    } else {
      _refreshDebounce?.cancel();
      _refreshDebounce = Timer(const Duration(milliseconds: 300), refresh);
    }
  }

  /// Re-reads the proposed-action queue depth. Call after an approve/deny so
  /// the badge reflects the resolved queue immediately.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    final epoch = ++_refreshEpoch;
    try {
      final actions =
          await _ref.read(issuesServiceProvider).listPendingActions();
      if (!_isAdmin || epoch != _refreshEpoch) return;
      _set(actions.where((a) => a.canTakeAction).length);
      _ref.read(pendingAgentActionsLoadedProvider.notifier).state = true;
    } catch (_) {
      if (!_isAdmin || epoch != _refreshEpoch) return;
      // Preserve the last badge count while making a conditionally hidden row
      // visible again until an authoritative refresh succeeds.
      _ref.read(pendingAgentActionsLoadedProvider.notifier).state = false;
    }
  }

  /// Sets the count directly from a caller that already holds the authoritative
  /// queue (the agent-actions screen), avoiding a redundant fetch.
  void setCount(int value) {
    _refreshEpoch++;
    _set(value);
    _ref.read(pendingAgentActionsLoadedProvider.notifier).state = true;
  }

  void _set(int value) {
    state = value < 0 ? 0 : value;
  }

  @override
  void dispose() {
    _sub?.cancel();
    _refreshDebounce?.cancel();
    super.dispose();
  }
}

/// Proposed-action queue depth for the signed-in admin (0 for non-admins).
final pendingAgentActionsProvider =
    StateNotifierProvider<PendingAgentActionsNotifier, int>(
  PendingAgentActionsNotifier.new,
);

/// Whether the agent-action count has been read successfully at least once.
final pendingAgentActionsLoadedProvider = StateProvider<bool>((ref) => false);
