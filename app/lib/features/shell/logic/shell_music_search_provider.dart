import 'dart:async';
import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/config/app_config.dart';
import '../../../core/providers/instance_provider.dart';
import '../../discover/data/music_discovery_service.dart';
import '../../discover/data/music_models.dart';
import '../../discover/logic/discovery_access.dart';

enum MusicSearchError { noInstance, forbidden, requestFailed }

class ShellMusicSearchState {
  final String searchQuery;
  final List<MusicAlbum> results;
  final List<MusicArtist> artists;
  final bool isLoadingSearch;
  final bool artistsLoading;
  final bool searched;
  final MusicSearchError? error;
  final bool artistsUnavailable;
  final int? nextPage;
  final int? nextArtistPage;
  const ShellMusicSearchState(
      {this.searchQuery = '',
      this.results = const [],
      this.artists = const [],
      this.isLoadingSearch = false,
      this.artistsLoading = false,
      this.searched = false,
      this.error,
      this.artistsUnavailable = false,
      this.nextPage,
      this.nextArtistPage});
  bool get isSearching => searchQuery.trim().isNotEmpty;
  ShellMusicSearchState copyWith(
          {String? searchQuery,
          List<MusicAlbum>? results,
          List<MusicArtist>? artists,
          bool? isLoadingSearch,
          bool? artistsLoading,
          bool? searched,
          MusicSearchError? error,
          bool clearError = false,
          bool? artistsUnavailable,
          int? nextPage,
          int? nextArtistPage,
          bool clearNextPage = false,
          bool clearNextArtistPage = false}) =>
      ShellMusicSearchState(
          searchQuery: searchQuery ?? this.searchQuery,
          results: results ?? this.results,
          artists: artists ?? this.artists,
          isLoadingSearch: isLoadingSearch ?? this.isLoadingSearch,
          artistsLoading: artistsLoading ?? this.artistsLoading,
          searched: searched ?? this.searched,
          error: clearError ? null : error ?? this.error,
          artistsUnavailable: artistsUnavailable ?? this.artistsUnavailable,
          nextPage: clearNextPage ? null : nextPage ?? this.nextPage,
          nextArtistPage: clearNextArtistPage
              ? null
              : nextArtistPage ?? this.nextArtistPage);
}

/// One catalog search with independent album/artist delivery. Cancellation
/// reaches the server; stale responses cannot change a new query or instance.
class ShellMusicSearchNotifier extends StateNotifier<ShellMusicSearchState> {
  final Ref _ref;
  Timer? _debounce;
  int _generation = 0;
  final _requests = <CancelToken>{};
  double scrollOffset = 0;
  ShellMusicSearchNotifier(this._ref) : super(const ShellMusicSearchState());
  void _cancel() {
    _debounce?.cancel();
    _generation++;
    for (final token in _requests) {
      token.cancel('Search changed');
    }
    _requests.clear();
  }

  void updateSearch(String query) {
    _cancel();
    scrollOffset = 0;
    if (query.trim().isEmpty) {
      state = const ShellMusicSearchState();
      return;
    }
    state = ShellMusicSearchState(
        searchQuery: query, isLoadingSearch: true, artistsLoading: true);
    final generation = _generation;
    _debounce = Timer(AppConfig.searchDebounce, () => _start(generation));
  }

  void _start(int generation) {
    final id = _ref.read(instanceProvider).activeLidarrInstance?.id;
    if (!_ref.read(discoveryAccessProvider).canBrowse('lidarr', id)) {
      state = state.copyWith(
          isLoadingSearch: false,
          artistsLoading: false,
          error: id == null
              ? MusicSearchError.noInstance
              : MusicSearchError.forbidden);
      return;
    }
    unawaited(_albums(generation, id, 1));
    unawaited(_artists(generation, id, 1));
  }

  Future<T> _read<T>(Future<T> Function(CancelToken) load) async {
    final token = CancelToken();
    _requests.add(token);
    try {
      return await load(token).timeout(const Duration(seconds: 10),
          onTimeout: () {
        token.cancel('Search deadline');
        throw TimeoutException('Music search timed out');
      });
    } finally {
      _requests.remove(token);
    }
  }

  bool _current(int generation) => mounted && generation == _generation;
  MusicSearchError _error(Object error) {
    if (kDebugMode) debugPrint('Music search failed with ${error.runtimeType}');
    return error is DioException &&
            {401, 403}.contains(error.response?.statusCode)
        ? MusicSearchError.forbidden
        : MusicSearchError.requestFailed;
  }

  void _deny() {
    _cancel();
    state = ShellMusicSearchState(
        searchQuery: state.searchQuery, error: MusicSearchError.forbidden);
  }

  Future<void> _albums(int generation, String? id, int page) async {
    try {
      final result = await _read((token) => _ref
          .read(musicDiscoveryServiceProvider)
          .search(state.searchQuery.trim(), id,
              page: page, includeSingles: true, cancelToken: token));
      if (!_current(generation)) return;
      state = state.copyWith(
          results: page == 1
              ? result.results
              : [...state.results, ...result.results],
          isLoadingSearch: false,
          searched: true,
          clearError: true,
          nextPage: result.nextPage,
          clearNextPage: result.nextPage == null);
    } catch (e) {
      if (_current(generation)) {
        final error = _error(e);
        if (error == MusicSearchError.forbidden) {
          _deny();
          return;
        }
        state = state.copyWith(
            results: error == MusicSearchError.forbidden ? const [] : null,
            isLoadingSearch: false,
            error: error);
      }
    }
  }

  Future<void> _artists(int generation, String? id, int page) async {
    try {
      final result = await _read((token) => _ref
          .read(musicDiscoveryServiceProvider)
          .searchArtists(state.searchQuery.trim(), id,
              page: page, cancelToken: token));
      if (!_current(generation)) return;
      state = state.copyWith(
          artists: page == 1
              ? result.results
              : [...state.artists, ...result.results],
          artistsLoading: false,
          artistsUnavailable: false,
          nextArtistPage: result.nextPage,
          clearNextArtistPage: result.nextPage == null);
    } catch (e) {
      if (_current(generation)) {
        if (_error(e) == MusicSearchError.forbidden) {
          _deny();
          return;
        }
        state = state.copyWith(artistsLoading: false, artistsUnavailable: true);
      }
    }
  }

  void loadMore() {
    if (state.isLoadingSearch || state.nextPage == null) return;
    state = state.copyWith(isLoadingSearch: true, clearError: true);
    unawaited(_albums(_generation,
        _ref.read(instanceProvider).activeLidarrInstance?.id, state.nextPage!));
  }

  void loadMoreArtists() {
    if (state.artistsLoading || state.nextArtistPage == null) return;
    state = state.copyWith(artistsLoading: true, artistsUnavailable: false);
    unawaited(_artists(
        _generation,
        _ref.read(instanceProvider).activeLidarrInstance?.id,
        state.nextArtistPage!));
  }

  void retryAlbums() {
    if (state.isLoadingSearch) return;
    state = state.copyWith(isLoadingSearch: true, clearError: true);
    unawaited(_albums(
        _generation,
        _ref.read(instanceProvider).activeLidarrInstance?.id,
        state.results.isEmpty ? 1 : state.nextPage ?? 1));
  }

  void retryArtists() {
    if (state.artistsLoading) return;
    state = state.copyWith(artistsLoading: true, artistsUnavailable: false);
    unawaited(_artists(
        _generation,
        _ref.read(instanceProvider).activeLidarrInstance?.id,
        state.artists.isEmpty ? 1 : state.nextArtistPage ?? 1));
  }

  void rerunForInstance() {
    final query = state.searchQuery;
    _cancel();
    scrollOffset = 0;
    if (query.trim().isEmpty) {
      state = const ShellMusicSearchState();
      return;
    }
    state = ShellMusicSearchState(
        searchQuery: query, isLoadingSearch: true, artistsLoading: true);
    _start(_generation);
  }

  void reset() {
    _cancel();
    scrollOffset = 0;
    state = const ShellMusicSearchState();
  }

  @override
  void dispose() {
    _cancel();
    super.dispose();
  }
}

final shellMusicSearchProvider =
    StateNotifierProvider<ShellMusicSearchNotifier, ShellMusicSearchState>(
        (ref) {
  final notifier = ShellMusicSearchNotifier(ref);
  ref.listen(catalogDiscoveryScopeProvider, (previous, next) {
    if (previous != next) notifier.rerunForInstance();
  });
  ref.listen(instanceProvider.select((s) => s.activeLidarrInstance?.id),
      (previous, next) {
    if (previous != next) notifier.rerunForInstance();
  });
  return notifier;
});
