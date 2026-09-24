import 'dart:async';
import 'dart:collection';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../auth/logic/auth_provider.dart';
import '../../request/data/request_service.dart';
import '../data/tmdb_models.dart';
import 'cover_4k_badges_provider.dart';
import 'search_library_status.dart';

typedef TVCardContext = ({bool supported, String? instanceId, String identity,
  bool show4K});

final tvCardStatusClockProvider = Provider<DateTime Function()>((_) => DateTime.now);

/// Capability is not admin-only: requesters need corrected badges too. Tokens
/// are deliberately excluded, but account, grants and content-policy changes
/// replace the entire cache, including reads that have not completed yet. So
/// does the 4K badges setting, which changes what each read asks for.
final tvCardContextProvider = Provider<TVCardContext>((ref) {
  final show4K = ref.watch(cover4KBadgesProvider);
  final scope = ref.watch(authProvider.select((value) {
    final auth = value.valueOrNull;
    final connection = auth?.connection;
    final user = auth?.user;
    return (
      supported: connection?.tvMatchCorrections == true,
      instanceId: user == null ? null : connection?.defaultSonarrInstance?.id,
      identity: jsonEncode([
        connection?.serverUrl, user?.id, user?.role, user?.permissions,
        user?.child, user?.contentLimits?.toJson(),
        for (final instance in connection?.sonarrInstances ?? [])
          [instance.id, instance.isDefault],
      ]),
    );
  }));
  return (supported: scope.supported, instanceId: scope.instanceId,
    identity: scope.identity, show4K: show4K);
});

final tvCardStatusCacheProvider =
    ChangeNotifierProvider<TVCardStatusCache>((ref) {
  final context = ref.watch(tvCardContextProvider);
  final service = RequestService(backendDio: ref.watch(backendClientProvider));
  final cache = TVCardStatusCache(now: ref.watch(tvCardStatusClockProvider), load: (id, cancelToken) =>
    service.checkStatusDetail(id, MediaType.tv,
      instanceId: context.instanceId, includeInstanceStatuses: false,
      include4K: context.show4K, cancelToken: cancelToken));
  final lifecycle = WidgetsBinding.instance.lifecycleState;
  cache.foreground = lifecycle == null || lifecycle == AppLifecycleState.resumed;
  Timer? debounce;
  if (context.supported && context.instanceId != null) {
    ref.listen(libraryRefreshTickProvider, (_, __) => cache.invalidate());
    ref.listen(libraryChangedEventsProvider, (_, next) {
      final event = next.valueOrNull;
      if (event == null) return;
      final instance = event.data['instance_id'];
      if (instance is String && instance.isNotEmpty && instance != context.instanceId) return;
      final type = event.data['media_type'] ?? event.data['service_type'];
      if (type != null && type != 'tv' && type != 'sonarr') return;
      debounce?.cancel();
      debounce = Timer(const Duration(seconds: 3), cache.invalidate);
    });
    WidgetsBinding.instance.addObserver(cache);
  }
  ref.onDispose(() {
    debounce?.cancel();
    WidgetsBinding.instance.removeObserver(cache);
  });
  return cache;
});

/// Only mounted catalog cards subscribe. Disposing a card stops its polling;
/// returning to it shares a fresh cached result or fetches an expired one.
final tvCardStatusProvider =
    FutureProvider.autoDispose.family<LibraryStatus?, int>((ref, tmdbId) async {
  final context = ref.watch(tvCardContextProvider);
  if (!context.supported || context.instanceId == null || tmdbId <= 0) {
    return null;
  }
  final cache = ref.watch(tvCardStatusCacheProvider);
  if (!cache.foreground) return cache.peek(tmdbId);
  var disposed = false;
  var watched = true;
  Timer? expiry;
  ref.onDispose(() { disposed = true; expiry?.cancel(); });
  ref.onCancel(() { watched = false; expiry?.cancel(); });
  ref.onResume(() {
    watched = true;
    expiry = Timer(cache.remainingFreshness(tmdbId), ref.invalidateSelf);
  });
  final result = await cache.read(tmdbId);
  if (!disposed && watched) {
    expiry?.cancel();
    expiry = Timer(cache.remainingFreshness(tmdbId), ref.invalidateSelf);
  }
  return result;
});

typedef TVCardStatusLoader = Future<RequestStatusDetail> Function(
    int tmdbId, CancelToken cancelToken);

/// A session/library-scoped, bounded queue. No Sonarr identity or request
/// state is persisted here: the backend remains authoritative on every read.
class TVCardStatusCache extends ChangeNotifier with WidgetsBindingObserver {
  static const staleAfter = Duration(seconds: 30);
  static const maxConcurrent = 4;
  static const maxCached = 256;

  final TVCardStatusLoader load;
  final DateTime Function() _now;
  final _entries = <int, _TVCardEntry>{};
  final _queue = Queue<_TVCardEntry>();
  final _running = <_TVCardEntry>{};
  bool _disposed = false;
  bool foreground = true;

  TVCardStatusCache({required this.load, DateTime Function()? now})
      : _now = now ?? DateTime.now;

  LibraryStatus? peek(int id) => _entries[id]?.value;

  Duration remainingFreshness(int id) {
    final fetched = _entries[id]?.fetchedAt;
    if (fetched == null) return staleAfter;
    final remaining = staleAfter - _now().difference(fetched);
    return remaining > Duration.zero ? remaining : const Duration(milliseconds: 1);
  }

  Future<LibraryStatus?> read(int id) {
    if (_disposed) return Future.value(unknownTVLibraryStatus);
    final old = _entries.remove(id);
    if (old != null && (old.fetchedAt == null ||
        _now().difference(old.fetchedAt!) < staleAfter)) {
      _entries[id] = old;
      return old.result.future;
    }
    final entry = _TVCardEntry(id);
    _entries[id] = entry;
    _queue.add(entry);
    _trim();
    _pump();
    return entry.result.future;
  }

  void _trim() {
    for (final key in _entries.keys.toList()) {
      if (_entries.length <= maxCached) break;
      if (_entries[key]!.fetchedAt != null) _entries.remove(key);
    }
  }

  void _pump() {
    while (!_disposed && foreground && _running.length < maxConcurrent &&
        _queue.isNotEmpty) {
      final entry = _queue.removeFirst();
      _running.add(entry);
      unawaited(_fetch(entry));
    }
  }

  Future<void> _fetch(_TVCardEntry entry) async {
    LibraryStatus? value;
    try {
      value = tvLibraryStatus(await load(entry.id, entry.cancelToken));
    } catch (_) {
      value = unknownTVLibraryStatus;
    }
    if (!_disposed && identical(_entries[entry.id], entry)) {
      entry.value = value;
      entry.fetchedAt = _now();
      _trim();
    }
    if (!entry.result.isCompleted) entry.result.complete(value);
    _running.remove(entry);
    _pump();
  }

  /// Both mutations and read-only correction changes invalidate projections.
  /// Cancel and fence old reads before listeners can request the new mapping.
  void invalidate() {
    if (_disposed) return;
    _clear();
    notifyListeners();
  }

  void _clear() {
    for (final entry in {..._entries.values, ..._running}) {
      entry.cancelToken.cancel('TV card status invalidated');
      if (!entry.result.isCompleted) {
        entry.result.complete(unknownTVLibraryStatus);
      }
    }
    _entries.clear();
    _queue.clear();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    foreground = state == AppLifecycleState.resumed;
    invalidate();
  }

  @override
  void dispose() {
    _disposed = true;
    _clear();
    super.dispose();
  }
}

class _TVCardEntry {
  final int id;
  final cancelToken = CancelToken();
  final result = Completer<LibraryStatus?>();
  DateTime? fetchedAt;
  LibraryStatus? value;
  _TVCardEntry(this.id);
}
