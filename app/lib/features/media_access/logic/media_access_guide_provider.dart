import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../core/models/backend_connection.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/storage/preferences.dart';
import '../../auth/data/server_url.dart';
import '../../auth/logic/auth_provider.dart';
import '../../settings/data/request_settings_service.dart';

/// Only the main navigation shortcut is optional. Settings and the route
/// continue to use the existing eligibility rules, independent of this value.
final mediaAccessGuideHiddenProvider =
    StateNotifierProvider<MediaAccessGuidePreference, bool>((ref) {
  final identity = ref.watch(authProvider.select((value) {
    final auth = value.valueOrNull;
    final connection = auth?.connection;
    final user = auth?.user;
    return connection == null || user == null
        ? null
        : (
            serverUrl: normalizeServerUrl(connection.serverUrl),
            userId: user.id,
            isAdmin: user.isAdmin,
          );
  }));
  final preference = MediaAccessGuidePreference(
    preferences: ref.read(sharedPreferencesProvider.future),
    storageKey: identity == null
        ? null
        : 'media_access_guide:${Uri.encodeComponent(identity.serverUrl)}:${identity.userId}',
    grantedInstanceIds: identity == null || identity.isAdmin
        ? null
        : ref
            .read(authProvider)
            .valueOrNull
            ?.connection
            ?.mediaServerInstances
            .map((instance) => instance.id)
            .toSet(),
    readGrants: identity?.isAdmin == true
        ? () async {
            final service = RequestSettingsService(
                backendDio: ref.read(backendClientProvider));
            final grants =
                await service.getUserInstanceGrants(identity!.userId);
            return {
              for (final type in mediaServerServiceTypes) ...?grants[type],
            };
          }
        : null,
  );

  // The existing config sync refreshes this connection on config_changed,
  // reconnect, and resume. Admin config includes ungranted servers, so read
  // their own grants instead. Failed reads retain the last successful set.
  ref.listen(authProvider, (previous, value) {
    final next = value.valueOrNull?.connection;
    if (next == null) return;
    final user = value.valueOrNull?.user;
    if (identity == null ||
        normalizeServerUrl(next.serverUrl) != identity.serverUrl ||
        user?.id != identity.userId ||
        user?.isAdmin != identity.isAdmin) {
      return;
    }
    if (identical(previous?.valueOrNull?.connection, next)) return;
    if (identity.isAdmin) {
      unawaited(preference.refreshGrants());
    } else {
      preference.updateGrants(
          next.mediaServerInstances.map((instance) => instance.id).toSet());
    }
  });
  return preference;
});

final mediaAccessGuideNavigationVisibleProvider = Provider<bool>((ref) {
  final eligible = ref.watch(authProvider.select((value) =>
      value.valueOrNull?.connection?.mediaAccessGuideVisible ?? false));
  final hidden = ref.watch(mediaAccessGuideHiddenProvider);
  return eligible && !hidden;
});

/// A device-local preference scoped to one Cantinarr server and user. The
/// snapshot is of instance IDs, never names, service types, or account status.
class MediaAccessGuidePreference extends StateNotifier<bool> {
  MediaAccessGuidePreference({
    required Future<SharedPreferences> preferences,
    required String? storageKey,
    Set<String>? grantedInstanceIds,
    Future<Set<String>> Function()? readGrants,
  })  : _preferences = preferences,
        _storageKey = storageKey,
        _grants = grantedInstanceIds,
        _readGrants = readGrants,
        super(false) {
    unawaited(_load());
    unawaited(refreshGrants());
  }

  final Future<SharedPreferences> _preferences;
  final String? _storageKey;
  final Future<Set<String>> Function()? _readGrants;
  Set<String>? _grants;
  Set<String>? _hiddenGrants;
  bool _loaded = false;
  bool _edited = false;
  bool _refreshPending = false;
  Future<void>? _refreshInFlight;
  Future<void> _writes = Future.value();

  Future<void> _load() async {
    try {
      final prefs = await _preferences;
      if (!mounted) return;
      final raw = _storageKey == null ? null : prefs.getString(_storageKey);
      if (!_edited && raw != null) {
        final saved = jsonDecode(raw) as Map<String, dynamic>;
        _hiddenGrants =
            (saved['instance_ids'] as List?)?.cast<String>().toSet();
        state = saved['hidden'] == true;
      }
    } catch (_) {
      // Unreadable local storage must not block opening or hiding the guide.
    }
    if (!mounted) return;
    _loaded = true;
    _reconcile();
  }

  Future<void> setHidden(bool hidden) {
    _edited = true;
    _hiddenGrants = hidden ? _grants : null;
    state = hidden;
    return _save();
  }

  void updateGrants(Set<String> grants) {
    if (!mounted) return;
    _grants = Set.unmodifiable(grants);
    if (_loaded || _edited) _reconcile();
  }

  void _reconcile() {
    final grants = _grants;
    if (!state || grants == null) return;
    if (_hiddenGrants == null) {
      // Hiding before the first successful admin grant read is allowed.
      // Unknown is not an empty grant set: establish the first baseline.
      _hiddenGrants = grants;
    } else if (grants.difference(_hiddenGrants!).isNotEmpty) {
      state = false;
      _hiddenGrants = null;
    } else {
      // Removing access does not forget IDs acknowledged when hiding.
      return;
    }
    unawaited(_save().catchError((Object error) {
      debugPrint('Could not save media guide preference: $error');
    }));
  }

  Future<void> _save() {
    final key = _storageKey;
    if (key == null) return Future.value();
    final value = jsonEncode({
      'hidden': state,
      'instance_ids':
          _hiddenGrants == null ? null : (_hiddenGrants!.toList()..sort()),
    });
    // Preserve the order of fast toggles and an automatic grant reset, even
    // when the platform's preference write has not finished yet.
    final write = _writes.then((_) async {
      final prefs = await _preferences;
      if (!await prefs.setString(key, value)) {
        throw StateError('Preference write failed');
      }
    });
    _writes = write.catchError((Object _) {});
    return write;
  }

  Future<void> refreshGrants() async {
    if (_readGrants == null || !mounted) return;
    _refreshPending = true;
    if (_refreshInFlight != null) return _refreshInFlight;
    final refresh = _drainGrantRefreshes();
    _refreshInFlight = refresh;
    try {
      await refresh;
    } finally {
      _refreshInFlight = null;
    }
  }

  Future<void> _drainGrantRefreshes() async {
    while (_refreshPending && mounted) {
      _refreshPending = false;
      try {
        updateGrants(await _readGrants!());
      } catch (_) {
        // Keep the last successful snapshot. A connection failure is not a
        // revocation or a new grant; the next config refresh retries.
      }
    }
  }
}
