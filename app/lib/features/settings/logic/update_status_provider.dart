import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';
import '../data/update_status_service.dart';

/// Admin-only update status, null while unknown (loading, or not an admin).
///
/// Mirrors [setupStatusProvider]: there is no websocket event for a new
/// release, so the banner and Settings refresh on login and on app resume.
class UpdateStatusNotifier extends StateNotifier<UpdateStatus?> {
  UpdateStatusNotifier(this._ref) : super(null) {
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
    // Cleared before the refetch, not after: another server's status must
    // never keep showing here, even when the refetch fails (refresh keeps
    // the previous state on error by design).
    state = null;
    if (!admin) return;
    refresh();
  }

  /// Re-fetches the update status from the backend. No-op for non-admins.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    final epoch = ++_refreshEpoch;
    try {
      final status = await _ref.read(updateStatusServiceProvider).fetch();
      if (_isAdmin && epoch == _refreshEpoch) state = status;
    } catch (_) {
      // Best-effort: keep the last known status on a transient failure.
    }
  }

  /// Persists the management-portal URL and updates state. Rethrows on failure
  /// so the Settings screen can show the error.
  Future<void> setManagementUrl(String url) async {
    final status =
        await _ref.read(updateStatusServiceProvider).setManagementUrl(url);
    _refreshEpoch++;
    state = status;
  }
}

final updateStatusProvider =
    StateNotifierProvider<UpdateStatusNotifier, UpdateStatus?>(
  UpdateStatusNotifier.new,
);
