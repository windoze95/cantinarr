import 'package:flutter/foundation.dart';

import '../../../core/logic/library_sort_controller.dart';
import '../../../core/models/library_sort.dart';
import 'lidarr_library_sort.dart';
import '../data/lidarr_api_service.dart';
import '../data/lidarr_models.dart';

/// Library filters for the Lidarr artist list. Mirrors ChaptarrLibraryFilter
/// for the artist-centric music library.
enum LidarrLibraryFilter { all, monitored, missing }

class LidarrLibraryState {
  final List<LidarrArtist> artists;
  final List<LidarrArtist> filtered;
  final bool isLoading;
  final String? error;
  final String searchQuery;
  final LidarrLibraryFilter filter;

  const LidarrLibraryState({
    this.artists = const [],
    this.filtered = const [],
    this.isLoading = false,
    this.error,
    this.searchQuery = '',
    this.filter = LidarrLibraryFilter.all,
  });

  LidarrLibraryState copyWith({
    List<LidarrArtist>? artists,
    List<LidarrArtist>? filtered,
    bool? isLoading,
    String? error,
    String? searchQuery,
    LidarrLibraryFilter? filter,
  }) =>
      LidarrLibraryState(
        artists: artists ?? this.artists,
        filtered: filtered ?? this.filtered,
        isLoading: isLoading ?? this.isLoading,
        error: error,
        searchQuery: searchQuery ?? this.searchQuery,
        filter: filter ?? this.filter,
      );

  int get monitoredCount => artists.where((a) => a.monitored).length;
  int get completeCount =>
      artists.where((a) => a.percentComplete >= 1.0).length;
  int get partialCount => artists
      .where((a) => a.percentComplete > 0 && a.percentComplete < 1.0)
      .length;
}

/// Holds the Lidarr artist library for one instance. A hand-rolled
/// ChangeNotifier (mirrors ChaptarrLibraryNotifier) instantiated per screen,
/// so a `ref.listen(activeLidarrInstanceId)` re-init swaps instances cleanly.
class LidarrLibraryNotifier extends ChangeNotifier {
  final LidarrApiService _service;
  late final LibrarySortController sorting;
  bool _disposed = false;
  int _loadGeneration = 0;

  LidarrLibraryState _state = const LidarrLibraryState();
  LidarrLibraryState get state => _state;
  set state(LidarrLibraryState value) {
    if (_disposed) return;
    _state = value;
    notifyListeners();
  }

  LidarrLibraryNotifier(this._service) {
    sorting = LibrarySortController(module: 'lidarr',
      loaders: {
        LibrarySortLookup.qualityProfiles: () async => {
          for (final profile in await _service.getQualityProfiles()) profile.id: profile.name,
        },
        LibrarySortLookup.metadataProfiles: () async => {
          for (final profile in await _service.getMetadataProfiles()) profile.id: profile.name,
        },
        LibrarySortLookup.tags: () async => {
          for (final tag in await _service.getTags()) tag.id: tag.label,
        },
      }, onChanged: _resort);
  }

  void _resort() {
    state = state.copyWith(error: state.error,
      filtered: _applyFilters(state.artists, state.searchQuery, state.filter));
  }

  @override
  void dispose() {
    _disposed = true;
    sorting.dispose();
    super.dispose();
  }

  Future<void> loadArtists() async {
    final generation = ++_loadGeneration;
    final labels = sorting.refresh();
    state = state.copyWith(isLoading: true);
    try {
      final artists = await _service.getArtists();
      if (_disposed || generation != _loadGeneration) return;
      state = state.copyWith(
        isLoading: false,
        artists: artists,
        filtered: _applyFilters(artists, state.searchQuery, state.filter),
      );
    } catch (e) {
      if (_disposed || generation != _loadGeneration) return;
      state = state.copyWith(
        isLoading: false,
        error: 'Failed to load library: $e',
      );
    } finally {
      await labels;
    }
  }

  void search(String query) {
    state = state.copyWith(
      error: state.error,
      searchQuery: query,
      filtered: _applyFilters(state.artists, query, state.filter),
    );
  }

  void setFilter(LidarrLibraryFilter filter) {
    state = state.copyWith(
      error: state.error,
      filter: filter,
      filtered: _applyFilters(state.artists, state.searchQuery, filter),
    );
  }

  Future<void> searchForArtist(int artistId) async {
    await _service.searchArtist(artistId);
  }

  List<LidarrArtist> _applyFilters(
    List<LidarrArtist> artists,
    String query,
    LidarrLibraryFilter filter,
  ) {
    var result = artists;

    if (query.isNotEmpty) {
      final q = query.toLowerCase();
      result =
          result.where((a) => a.artistName.toLowerCase().contains(q)).toList();
    }

    result = switch (filter) {
      LidarrLibraryFilter.all => result,
      LidarrLibraryFilter.monitored =>
        result.where((a) => a.monitored).toList(),
      LidarrLibraryFilter.missing => result
          .where((a) => a.monitored && a.percentComplete < 1.0)
          .toList(),
    };

    return sortLidarrLibrary(result, sorting.effectiveSelection, sorting.labels);
  }
}
