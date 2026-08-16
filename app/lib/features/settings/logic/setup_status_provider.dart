import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';
import '../data/setup_status_service.dart';

/// The admin setup checklist, null while unknown (loading, or not an admin).
///
/// Drives the settings "Setup Checklist" tile subtitle, the wizard screen,
/// and the drawer reminder entry. There is no websocket event for config
/// changes; instead the wizard and the settings screen call [refresh] on
/// load/return, which covers every in-app path that changes configuration.
class SetupStatusNotifier extends StateNotifier<SetupStatus?> {
  SetupStatusNotifier(this._ref) : super(null) {
    _bind();
    // Re-bind on login/logout/role change/server switch without rebuilding
    // the provider.
    _ref.listen(authProvider, (_, __) => _bind());
  }

  final Ref _ref;
  bool _isAdmin = false;
  String? _serverUrl;
  int _refreshEpoch = 0;

  void _bind() {
    final auth = _ref.read(authProvider).valueOrNull;
    final admin = auth?.user?.isAdmin ?? false;
    final server = auth?.connection?.serverUrl;
    if (admin == _isAdmin && server == _serverUrl) return; // no change
    _refreshEpoch++;
    _isAdmin = admin;
    _serverUrl = server;
    // Cleared before the refetch, not after: another server's checklist must
    // never keep showing here, even when the refetch fails (refresh keeps
    // the previous state on error by design).
    state = null;
    if (!admin) return;
    refresh();
  }

  /// Re-derives the checklist from the backend. Cheap (one small request);
  /// call whenever a screen that can change configuration comes or goes.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    final epoch = ++_refreshEpoch;
    try {
      final status = await _ref.read(setupStatusServiceProvider).fetch();
      if (_isAdmin && epoch == _refreshEpoch) state = status;
    } catch (_) {
      // Best-effort: keep the last known status on a transient failure.
    }
  }
}

final setupStatusProvider =
    StateNotifierProvider<SetupStatusNotifier, SetupStatus?>(
  SetupStatusNotifier.new,
);
