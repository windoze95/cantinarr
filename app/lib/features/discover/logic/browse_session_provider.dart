import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/discover_api_service.dart';
import 'browse_grid_notifier.dart';
import 'browse_query.dart';
import 'discover_session.dart';

final browseSessionProvider = Provider<BrowseSession>((ref) {
  final scope = ref.watch(discoverSessionProvider);
  final cache = BrowseSession(ref.watch(discoverServiceProvider),
      isCurrent: () => ref.read(discoverSessionProvider) == scope);
  ref.onDispose(cache.dispose);
  return cache;
});

/// Eight complete queries, including filters, sort, media type and source ID.
/// Mounted grids own a lease, so evicting a cached query cannot dispose a
/// screen underneath a pushed detail route.
class BrowseSession {
  static const maxQueries = 8;
  final DiscoverApiService api;
  final _entries = <String, BrowseGridNotifier>{};
  final _leases = <BrowseGridNotifier, int>{};
  bool _disposed = false;

  final bool Function()? isCurrent;
  BrowseSession(this.api, {this.isCurrent});

  BrowseGridNotifier acquire(BrowseQuery query) {
    final key = query.toLocation();
    final entry = _entries.remove(key) ?? BrowseGridNotifier(api, query, isCurrent: isCurrent);
    _leases[entry] = (_leases[entry] ?? 0) + 1;
    touch(entry);
    return entry;
  }

  /// A retained route becoming visible again counts as recent use too.
  void touch(BrowseGridNotifier entry) {
    if (_disposed) return;
    final key = entry.query.toLocation();
    final replaced = _entries.remove(key);
    if (replaced != null && replaced != entry && !_leases.containsKey(replaced)) {
      replaced.dispose();
    }
    _entries[key] = entry;
    while (_entries.length > maxQueries) {
      final old = _entries.remove(_entries.keys.first)!;
      if (!_leases.containsKey(old)) old.dispose();
    }
  }

  void release(BrowseGridNotifier entry) {
    if (_disposed) return;
    final remaining = (_leases[entry] ?? 1) - 1;
    if (remaining > 0) {
      _leases[entry] = remaining;
    } else {
      _leases.remove(entry);
      if (!_entries.containsValue(entry)) entry.dispose();
    }
  }

  void dispose() {
    _disposed = true;
    for (final entry in {..._entries.values, ..._leases.keys}) {
      entry.dispose();
    }
    _entries.clear();
    _leases.clear();
  }
}
