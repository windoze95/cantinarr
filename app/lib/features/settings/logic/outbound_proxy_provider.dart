import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';
import '../data/outbound_proxy_service.dart';

/// Admin-only outbound proxy settings, null while unknown (loading, or not
/// an admin), [OutboundProxySettings.empty] when the admin has not
/// configured one.
///
/// Mirrors [ExternalAddressNotifier]: nothing pushes changes for this
/// setting, so it refreshes on login and whenever Settings saves it.
class OutboundProxyNotifier extends StateNotifier<OutboundProxySettings?> {
  OutboundProxyNotifier(this._ref) : super(null) {
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

  /// Re-fetches the proxy settings from the backend. No-op for non-admins.
  Future<void> refresh() async {
    if (!_isAdmin) return;
    final epoch = ++_refreshEpoch;
    try {
      final value = await _ref.read(outboundProxyServiceProvider).fetch();
      if (_isAdmin && epoch == _refreshEpoch) state = value;
    } catch (_) {
      // Best-effort: keep the last known value on a transient failure.
    }
  }

  /// Persists the proxy settings and updates state with what the server
  /// kept. Rethrows on failure so the Settings screen can show the error.
  Future<void> set({
    required String url,
    required String username,
    required String password,
  }) async {
    final value = await _ref
        .read(outboundProxyServiceProvider)
        .set(url: url, username: username, password: password);
    _refreshEpoch++;
    state = value;
  }
}

final outboundProxyProvider =
    StateNotifierProvider<OutboundProxyNotifier, OutboundProxySettings?>(
  OutboundProxyNotifier.new,
);
