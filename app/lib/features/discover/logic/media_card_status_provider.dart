import 'dart:async';
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
import 'tv_card_status_provider.dart';

/// Both media types use the server projection on scan-capable servers.
/// Reuses the bounded visible-card cache without copying its scheduling logic.
final mediaCardStatusCacheProvider = ChangeNotifierProvider.autoDispose
    .family<TVCardStatusCache, MediaType>((ref, type) {
  final auth = ref.watch(authProvider);
  final connection = auth.valueOrNull?.connection;
  final show4K = ref.watch(cover4KBadgesProvider);
  final id = type == MediaType.movie
      ? connection?.defaultRadarrInstance?.id
      : connection?.defaultSonarrInstance?.id;
  // The provider watches auth so changes to remote links, household access,
  // grants and account policy discard results and cancel unfinished reads.
  final service = RequestService(backendDio: ref.watch(backendClientProvider));
  final cache = TVCardStatusCache(
      now: ref.watch(tvCardStatusClockProvider),
      load: (tmdbId, token) => service.checkStatusDetail(tmdbId, type,
          instanceId: id,
          includeInstanceStatuses: false,
          include4K: show4K,
          cancelToken: token));
  cache.foreground = WidgetsBinding.instance.lifecycleState == null ||
      WidgetsBinding.instance.lifecycleState == AppLifecycleState.resumed;
  ref.listen(libraryRefreshTickProvider, (_, __) => cache.invalidate());
  ref.listen(libraryChangedEventsProvider, (_, next) {
    if (next.valueOrNull != null) cache.invalidate();
  });
  WidgetsBinding.instance.addObserver(cache);
  ref.onDispose(() => WidgetsBinding.instance.removeObserver(cache));
  return cache;
});

final mediaCardStatusProvider = FutureProvider.autoDispose
    .family<LibraryStatus?, (MediaType, int)>((ref, key) async {
  final cache = ref.watch(mediaCardStatusCacheProvider(key.$1));
  if (!cache.foreground) return cache.peek(key.$2);
  Timer? timer;
  var disposed = false;
  ref.onDispose(() {
    disposed = true;
    timer?.cancel();
  });
  ref.onCancel(() => timer?.cancel());
  ref.onResume(() =>
      timer = Timer(cache.remainingFreshness(key.$2), ref.invalidateSelf));
  final result = await cache.read(key.$2);
  if (!disposed)
    timer = Timer(cache.remainingFreshness(key.$2), ref.invalidateSelf);
  return result;
});
