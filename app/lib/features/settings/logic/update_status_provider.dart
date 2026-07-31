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
    // Re-bind on login/logout/role change without rebuilding the provider.
    _ref.listen(authProvider, (_, __) => _bind());
  }

  final Ref _ref;
  bool _isAdmin = false;

  void _bind() {
    final admin = _ref.read(authProvider).valueOrNull?.user?.isAdmin ?? false;
    if (admin == _isAdmin) return; // no change
    _isAdmin = admin;
    if (!admin) {
      state = null;
      return;
    }
    refresh();
  }

  /// Re-fetches the update status from the backend. No-op for non-admins.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    try {
      final status = await _ref.read(updateStatusServiceProvider).fetch();
      if (_isAdmin) state = status;
    } catch (_) {
      // Best-effort: keep the last known status on a transient failure.
    }
  }

  /// Persists the management-portal URL and updates state. Rethrows on failure
  /// so the Settings screen can show the error.
  Future<void> setManagementUrl(String url) async {
    final status =
        await _ref.read(updateStatusServiceProvider).setManagementUrl(url);
    state = status;
  }
}

final updateStatusProvider =
    StateNotifierProvider<UpdateStatusNotifier, UpdateStatus?>(
  UpdateStatusNotifier.new,
);
