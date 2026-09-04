import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/config/app_config.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../lidarr/data/lidarr_api_service.dart';
import '../../lidarr/data/lidarr_models.dart';

/// The ways a Lidarr music search can fail to return a usable result list.
/// Distinct from the success-but-empty case, which is expressed as
/// `error == null && searched == true && results.isEmpty` rather than a
/// fourth member here.
enum MusicSearchError {
  /// No active Lidarr instance is configured for this connection.
  noInstance,

  /// The active instance rejected the request (401/403).
  forbidden,

  /// Any other failure — network error, timeout, 5xx, malformed response.
  requestFailed,
}

/// Shell-level music-search state, scoped to the Music discovery tab.
class ShellMusicSearchState {
  final String searchQuery;
  final List<LidarrAlbum> results;

  /// Artist matches for [searchQuery], rendered above [results].
  final List<LidarrArtist> artists;
  final bool isLoadingSearch;

  /// True once a lookup has completed successfully for [searchQuery] — the
  /// signal that distinguishes "no albums found" from "hasn't searched yet".
  final bool searched;
  final MusicSearchError? error;

  /// True when the album lookup succeeded but the artist lookup did not.
  ///
  /// The two are separate Lidarr calls, so an artist failure must not throw
  /// away album results the user can still use. But it must not render as an
  /// empty artist list either: per AGENTS.md an empty answer has to say
  /// whether it is absence or blindness, and "no artists matched" and
  /// "artists could not be searched" are different sentences.
  final bool artistsUnavailable;

  const ShellMusicSearchState({
    this.searchQuery = '',
    this.results = const [],
    this.artists = const [],
    this.isLoadingSearch = false,
    this.searched = false,
    this.error,
    this.artistsUnavailable = false,
  });

  /// [clearError] is explicit (not a plain nullable default) because a
  /// nullable-defaulted `copyWith` cannot set [error] back to null, and every
  /// successful result must clear a prior error.
  ShellMusicSearchState copyWith({
    String? searchQuery,
    List<LidarrAlbum>? results,
    List<LidarrArtist>? artists,
    bool? isLoadingSearch,
    bool? searched,
    MusicSearchError? error,
    bool clearError = false,
    bool? artistsUnavailable,
  }) =>
      ShellMusicSearchState(
        searchQuery: searchQuery ?? this.searchQuery,
        results: results ?? this.results,
        artists: artists ?? this.artists,
        isLoadingSearch: isLoadingSearch ?? this.isLoadingSearch,
        searched: searched ?? this.searched,
        error: clearError ? null : (error ?? this.error),
        artistsUnavailable: artistsUnavailable ?? this.artistsUnavailable,
      );

  bool get isSearching => searchQuery.trim().isNotEmpty;
}

/// Manages Lidarr album/artist-lookup search from the shell search bar,
/// scoped to the Music discovery tab. Mirrors [ShellBookSearchNotifier]'s
/// debounce and generation-guard shape, with the same typed failure taxonomy
/// and no pagination — `lookupAlbum` has no `page` parameter.
class ShellMusicSearchNotifier extends StateNotifier<ShellMusicSearchState> {
  final Ref _ref;
  Timer? _searchDebounce;
  int _searchGeneration = 0;

  ShellMusicSearchNotifier(this._ref) : super(const ShellMusicSearchState());

  void updateSearch(String query) {
    _searchDebounce?.cancel();
    final generation = ++_searchGeneration;

    if (query.trim().isEmpty) {
      state = const ShellMusicSearchState();
      return;
    }

    state = state.copyWith(
      searchQuery: query,
      results: const [],
      artists: const [],
      isLoadingSearch: true,
      searched: false,
      clearError: true,
      artistsUnavailable: false,
    );
    _searchDebounce = Timer(
      AppConfig.searchDebounce,
      () => _executeSearch(query: query, generation: generation),
    );
  }

  Future<void> _executeSearch({
    required String query,
    required int generation,
  }) async {
    bool superseded() =>
        !mounted ||
        generation != _searchGeneration ||
        state.searchQuery != query;

    // The no-instance check runs first and short-circuits before any
    // LidarrApiService is constructed or request issued, so no 401/403 or
    // generic failure can ever supersede it.
    final instance = _ref.read(instanceProvider).activeLidarrInstance;
    if (instance == null) {
      if (superseded()) return;
      state = state.copyWith(
        results: const [],
        artists: const [],
        isLoadingSearch: false,
        searched: false,
        artistsUnavailable: false,
        error: MusicSearchError.noInstance,
      );
      return;
    }

    final service = LidarrApiService(
      backendDio: _ref.read(backendClientProvider),
      instanceId: instance.id,
    );

    // Two independent Lidarr calls, issued together rather than in sequence
    // so adding artists costs no extra round-trip of latency. Their failures
    // are NOT shared: the album lookup alone owns the error taxonomy below,
    // and a failed artist lookup only sets `artistsUnavailable` so the view
    // can say "couldn't search artists" instead of showing an empty artist
    // list that reads as "this artist doesn't exist".
    final term = query.trim();
    final artistsFuture = service.lookupArtist(term);
    // Claim the error now — an unawaited future that completes with an error
    // while the album call is still in flight would otherwise reach the zone
    // handler as an unhandled async error.
    var artistsFailed = false;
    final artistsGuarded = artistsFuture.catchError((Object error) {
      if (kDebugMode) {
        debugPrint(
          'ShellMusicSearchNotifier: artist lookup failed with '
          '${error.runtimeType}',
        );
      }
      artistsFailed = true;
      return <LidarrArtist>[];
    });

    try {
      final albums = await service.lookupAlbum(term);
      final artists = await artistsGuarded;
      if (superseded()) return;
      state = state.copyWith(
        results: albums,
        artists: artists,
        isLoadingSearch: false,
        searched: true,
        clearError: true,
        artistsUnavailable: artistsFailed,
      );
    } on DioException catch (e) {
      if (kDebugMode) {
        debugPrint(
          'ShellMusicSearchNotifier: album lookup failed with '
          '${e.runtimeType}, status ${e.response?.statusCode}',
        );
      }
      if (superseded()) return;
      final code = e.response?.statusCode;
      state = state.copyWith(
        // A failed search owns the whole overlay, so the previous query's
        // artists must go with its albums.
        results: const [],
        artists: const [],
        isLoadingSearch: false,
        searched: false,
        artistsUnavailable: false,
        error: code == 401 || code == 403
            ? MusicSearchError.forbidden
            : MusicSearchError.requestFailed,
      );
    } catch (error) {
      if (kDebugMode) {
        debugPrint(
          'ShellMusicSearchNotifier: album lookup failed with '
          '${error.runtimeType}',
        );
      }
      if (superseded()) return;
      state = state.copyWith(
        results: const [],
        artists: const [],
        isLoadingSearch: false,
        searched: false,
        artistsUnavailable: false,
        error: MusicSearchError.requestFailed,
      );
    }
  }

  void reset() {
    _searchDebounce?.cancel();
    _searchGeneration++;
    state = const ShellMusicSearchState();
  }

  /// The active Lidarr instance changed. Discard whatever the previous
  /// instance's results were and re-run the currently typed query against
  /// the new instance — an instance switch is a deliberate act, not a
  /// keystroke, so this issues the request immediately rather than
  /// re-debouncing. Bumping the generation before touching state means any
  /// in-flight response from the *old* instance lands on a stale generation
  /// and is dropped by [_executeSearch]'s existing guard.
  void rerunForInstance() {
    _searchDebounce?.cancel();
    final generation = ++_searchGeneration;
    final query = state.searchQuery;

    if (query.trim().isEmpty) {
      state = const ShellMusicSearchState();
      return;
    }

    state = state.copyWith(
      results: const [],
      isLoadingSearch: true,
      searched: false,
      clearError: true,
    );
    _executeSearch(query: query, generation: generation);
  }

  @override
  void dispose() {
    _searchDebounce?.cancel();
    super.dispose();
  }
}

final shellMusicSearchProvider =
    StateNotifierProvider<ShellMusicSearchNotifier, ShellMusicSearchState>(
  (ref) => ShellMusicSearchNotifier(ref),
);
