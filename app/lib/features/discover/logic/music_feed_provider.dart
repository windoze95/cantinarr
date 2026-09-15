import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import 'discovery_access.dart';
import 'discovery_page_buffer.dart';
import '../../request/data/request_service.dart';
import '../data/music_discovery_service.dart';
import '../data/music_models.dart';
import 'music_browse_query.dart';

class MusicFeedState {
  final List<MusicAlbum> items;
  final int? nextPage;
  final bool loading;
  final String? error;
  final bool unsupported;
  final bool refreshFailed;
  final String emptyMessage;
  final List<MusicAlbum> upcoming;
  const MusicFeedState({
    this.items = const [],
    this.nextPage = 1,
    this.loading = false,
    this.error,
    this.unsupported = false,
    this.refreshFailed = false,
    this.emptyMessage = '',
    this.upcoming = const [],
  });
}

class MusicFeedNotifier extends StateNotifier<MusicFeedState> {
  final MusicDiscoveryService service;
  final MusicBrowseQuery query;
  final bool allowed;
  bool _prefetchEnabled = false;
  int _generation = 0;
  late final _ahead =
      DiscoveryPageBuffer<MusicPage>((page) => service.feed(query, page));
  MusicFeedNotifier(this.service, this.query, {this.allowed = true})
      : super(allowed
            ? const MusicFeedState()
            : const MusicFeedState(
                nextPage: null,
                error: 'Music is not available for this account.')) {
    if (allowed) Future.microtask(loadMore);
  }

  Future<void> loadMore() => _load(refresh: false);
  Future<void> refresh() => _load(refresh: true);
  Future<void> retry() =>
      _load(refresh: state.refreshFailed || state.unsupported);

  void enablePrefetch() {
    if (_prefetchEnabled || !mounted || !allowed) return;
    _prefetchEnabled = true;
    unawaited(_prefetchNext());
  }

  Future<void> _prefetchNext() async {
    if (!_prefetchEnabled ||
        state.loading ||
        state.error != null ||
        state.nextPage == null) {
      return;
    }
    final generation = _generation;
    final failure = await _ahead.prefetch(state.nextPage);
    if (!mounted || generation != _generation || state.loading) return;
    if (failure is DioException &&
        {401, 403}.contains(failure.response?.statusCode)) {
      _ahead.clear();
      state = const MusicFeedState(
          nextPage: null,
          error: 'Music is no longer available for this account or library.');
      return;
    }
    state = MusicFeedState(
      items: state.items,
      nextPage: state.nextPage,
      emptyMessage: state.emptyMessage,
      upcoming: _ahead.value?.results ?? const [],
    );
  }

  @override
  void dispose() {
    _ahead.clear();
    super.dispose();
  }

  Future<void> _load({required bool refresh}) async {
    if (!mounted ||
        !allowed ||
        state.loading ||
        (!refresh && state.nextPage == null)) {
      return;
    }
    _generation++;
    if (refresh) _ahead.clear();
    final before = state;
    var next = refresh ? 1 : before.nextPage;
    state = MusicFeedState(
      items: before.items,
      nextPage: before.nextPage,
      loading: true,
      emptyMessage: before.emptyMessage,
    );
    try {
      final items = refresh ? <MusicAlbum>[] : [...before.items];
      final seen = items.map((a) => a.foreignId).toSet();
      var message = '';
      // Singles and repeated MBIDs can consume an entire provider page.
      // Follow its cursor, with a bound; offer Load more if the stretch lasts.
      for (var attempts = 0; attempts < 3 && next != null; attempts++) {
        final page = await _ahead.take(next);
        if (!mounted) return;
        if (page.page != next) {
          throw const FormatException('Unexpected music page');
        }
        next = page.nextPage;
        message = page.emptyMessage;
        final oldLength = items.length;
        items.addAll(page.results.where((a) => seen.add(a.foreignId)));
        if (items.length > oldLength) break;
      }
      state = MusicFeedState(
        items: items,
        nextPage: next,
        emptyMessage: message,
      );
      unawaited(_prefetchNext());
    } catch (error) {
      if (!mounted) return;
      final forbidden =
          error is DioException && error.response?.statusCode == 403;
      state = MusicFeedState(
        items: forbidden ? const [] : before.items,
        nextPage: before.nextPage,
        unsupported: error is MusicDiscoveryUnsupported,
        refreshFailed: refresh,
        error: error is MusicDiscoveryUnsupported
            ? musicUpdateMessage
            : forbidden
                ? 'Music is no longer available for this account or library.'
                : before.items.isNotEmpty && refresh
                    ? 'Refresh failed. Your previous results are still shown.'
                    : 'Could not load music. Please retry.',
      );
    }
  }
}

final musicFeedProvider = StateNotifierProvider.autoDispose
    .family<MusicFeedNotifier, MusicFeedState, MusicBrowseQuery>((ref, query) {
  ref.watch(catalogDiscoveryScopeProvider);
  return MusicFeedNotifier(ref.watch(musicDiscoveryServiceProvider), query,
      allowed: ref
          .watch(discoveryAccessProvider)
          .canBrowse('lidarr', query.instanceId));
});

/// Each lazily built visible card reads authoritative status for its exact
/// instance. Metadata caching never substitutes for this live status service.
final musicCardStatusProvider = FutureProvider.autoDispose
    .family<MusicRequestStatusDetail, ({String id, String instanceId})>(
        (ref, key) {
  ref.watch(catalogDiscoveryScopeProvider);
  if (!ref.watch(discoveryAccessProvider).canBrowse('lidarr', key.instanceId)) {
    throw StateError('Music is not available for this account.');
  }
  ref.watch(libraryRefreshTickProvider);
  ref.watch(libraryChangedEventsProvider);
  return RequestService(backendDio: ref.watch(backendClientProvider))
      .checkMusicStatusDetail(key.id, instanceId: key.instanceId);
});
