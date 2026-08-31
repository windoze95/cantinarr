import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';
import '../data/external_address_service.dart';

/// Admin-only external address, null while unknown (loading, or not an
/// admin), '' when the admin has not configured one.
///
/// Mirrors [UpdateStatusNotifier]: nothing pushes changes for this setting,
/// so it refreshes on login and whenever Settings saves it.
class ExternalAddressNotifier extends StateNotifier<String?> {
  ExternalAddressNotifier(this._ref) : super(null) {
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
    // Cleared before the refetch, not after: another server's value must
    // never keep showing here, even when the refetch fails.
    state = null;
    if (!admin) return;
    refresh();
  }

  /// Re-fetches the external address from the backend. No-op for non-admins.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    final epoch = ++_refreshEpoch;
    try {
      final value = await _ref.read(externalAddressServiceProvider).fetch();
      if (_isAdmin && epoch == _refreshEpoch) state = value;
    } catch (_) {
      // Best-effort: keep the last known value on a transient failure.
    }
  }

  /// Persists the external address and updates state. Rethrows on failure so
  /// the Settings screen can show the error.
  Future<void> set(String url) async {
    final value = await _ref.read(externalAddressServiceProvider).set(url);
    _refreshEpoch++;
    state = value;
  }
}

final externalAddressProvider =
    StateNotifierProvider<ExternalAddressNotifier, String?>(
  ExternalAddressNotifier.new,
);
