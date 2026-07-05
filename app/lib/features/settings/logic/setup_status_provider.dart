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

  /// Re-derives the checklist from the backend. Cheap (one small request);
  /// call whenever a screen that can change configuration comes or goes.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    try {
      final status = await _ref.read(setupStatusServiceProvider).fetch();
      if (_isAdmin) state = status;
    } catch (_) {
      // Best-effort: keep the last known status on a transient failure.
    }
  }
}

final setupStatusProvider =
    StateNotifierProvider<SetupStatusNotifier, SetupStatus?>(
  SetupStatusNotifier.new,
);
