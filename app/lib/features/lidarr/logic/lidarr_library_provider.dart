import 'package:flutter/foundation.dart';
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

  LidarrLibraryState _state = const LidarrLibraryState();
  LidarrLibraryState get state => _state;
  set state(LidarrLibraryState value) {
    _state = value;
    notifyListeners();
  }

  LidarrLibraryNotifier(this._service);

  Future<void> loadArtists() async {
    state = state.copyWith(isLoading: true);
    try {
      final artists = await _service.getArtists();
      artists.sort((a, b) =>
          a.artistName.toLowerCase().compareTo(b.artistName.toLowerCase()));
      state = state.copyWith(
        isLoading: false,
        artists: artists,
        filtered: _applyFilters(artists, state.searchQuery, state.filter),
      );
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        error: 'Failed to load library: $e',
      );
    }
  }

  void search(String query) {
    state = state.copyWith(
      searchQuery: query,
      filtered: _applyFilters(state.artists, query, state.filter),
    );
  }

  void setFilter(LidarrLibraryFilter filter) {
    state = state.copyWith(
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

    return result;
  }
}
