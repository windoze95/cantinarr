import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/websocket_client.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../auth/logic/auth_provider.dart';

/// Tracks how many users shared a Plex email but have no invite sent yet,
/// for admins only.
///
/// This is the persistent surface behind the miss-able `plex_access_request`
/// push: the count drives a drawer "Plex invites" entry (shown only while
/// someone is waiting) and the hamburger dot. Seeded from the admin users
/// list and refreshed by `plex_access_request` websocket events — the server
/// emits one for every state change (waiting, auto-sent, auto-failed), and
/// stamps `plex_invited_at` before emitting, so a refetch always sees the
/// settled state. Non-admin accounts always report 0 and never call the
/// admin-only endpoint.
class PlexInvitesWaitingNotifier extends StateNotifier<int> {
  PlexInvitesWaitingNotifier(this._ref) : super(0) {
    _bind();
    // Re-bind on login/logout/role change without rebuilding the provider.
    _ref.listen(authProvider, (_, __) => _bind());
  }

  final Ref _ref;
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
      state = 0;
      return;
    }
    refresh();
    _sub = _ref
        .read(realtimeEventsProvider)
        .where((e) => e.type == 'plex_access_request')
        .listen((_) => refresh());
  }

  /// Re-counts waiting users from the backend. Call after sending an invite
  /// so the badge clears immediately.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    try {
      final users = await _ref.read(authProvider.notifier).listUsers();
      if (!_isAdmin) return;
      state = users
          .where((u) => u.plexEmail.isNotEmpty && u.plexInvitedAt == null)
          .length;
    } catch (_) {
      // Best-effort: keep the last known count on a transient failure.
    }
  }

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }
}

/// Users waiting for a Plex invite, for the signed-in admin (0 otherwise).
final plexInvitesWaitingProvider =
    StateNotifierProvider<PlexInvitesWaitingNotifier, int>(
  PlexInvitesWaitingNotifier.new,
);
