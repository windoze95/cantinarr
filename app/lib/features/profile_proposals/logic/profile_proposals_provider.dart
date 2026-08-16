import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/websocket_client.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/profile_proposals_service.dart';

/// Pending external profile-change proposals for the signed-in admin — the
/// attention-menu badge source. Mirrors [PendingAgentActionsNotifier]: seeds
/// from the REST list at construction (the shell watching the provider is
/// what instantiates it, so the count exists without the screen ever being
/// opened), applies the authoritative `pending_count` a
/// `profile_change_pending`/`profile_change_decided` event carries, and
/// otherwise refetches on ping.
class PendingProfileProposalsNotifier extends StateNotifier<int> {
  PendingProfileProposalsNotifier(this._ref) : super(0) {
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
    _sub?.cancel();
    _sub = null;
    if (!admin) {
      _ref.read(pendingProfileProposalsLoadedProvider.notifier).state = false;
      _ref.read(pendingProfileProposalsStaleProvider.notifier).state = false;
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
        .where((e) =>
            e.type == 'profile_change_pending' ||
            e.type == 'profile_change_decided')
        .listen(_onPing);
  }

  /// Applies the event's authoritative `pending_count`; without one, refetch.
  void _onPing(WsEvent event) {
    final raw = event.data['pending_count'];
    if (raw is num) {
      _refreshDebounce?.cancel();
      _refreshEpoch++;
      _set(raw.toInt());
      _ref.read(pendingProfileProposalsLoadedProvider.notifier).state = true;
      _ref.read(pendingProfileProposalsStaleProvider.notifier).state = false;
    } else {
      _refreshDebounce?.cancel();
      _refreshDebounce = Timer(const Duration(milliseconds: 300), refresh);
    }
  }

  /// Re-reads the pending queue depth (the empty status means pending-only
  /// server-side). Call after a decision so the badge drains immediately.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    final epoch = ++_refreshEpoch;
    try {
      final proposals = await _ref
          .read(profileProposalsServiceProvider)
          .listProposals(status: '');
      if (!_isAdmin || epoch != _refreshEpoch) return;
      _set(proposals.where((p) => p.isPending).length);
      _ref.read(pendingProfileProposalsLoadedProvider.notifier).state = true;
      _ref.read(pendingProfileProposalsStaleProvider.notifier).state = false;
    } catch (_) {
      if (!_isAdmin || epoch != _refreshEpoch) return;
      // Preserve the last badge count while making a conditionally hidden row
      // visible again until an authoritative refresh succeeds.
      _ref.read(pendingProfileProposalsLoadedProvider.notifier).state = false;
      _ref.read(pendingProfileProposalsStaleProvider.notifier).state = true;
    }
  }

  /// Sets the count directly from a caller that already holds the
  /// authoritative list (the approvals screen), avoiding a redundant fetch.
  void setCount(int value) {
    _refreshEpoch++;
    _set(value);
    _ref.read(pendingProfileProposalsLoadedProvider.notifier).state = true;
    _ref.read(pendingProfileProposalsStaleProvider.notifier).state = false;
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

/// Pending proposal count for the signed-in admin (0 for non-admins).
final pendingProfileProposalsProvider =
    StateNotifierProvider<PendingProfileProposalsNotifier, int>(
  PendingProfileProposalsNotifier.new,
);

/// Whether the proposal count has been read successfully at least once.
final pendingProfileProposalsLoadedProvider = StateProvider<bool>((ref) => false);

/// Whether the proposals count is currently unknowable (last refresh failed,
/// nothing authoritative since). Fail-open signal for the conditional menu
/// entry; loading alone never sets it.
final pendingProfileProposalsStaleProvider =
    StateProvider<bool>((ref) => false);
