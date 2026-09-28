import 'package:flutter/foundation.dart';

import '../../../core/logic/library_sort_controller.dart';
import '../../../core/models/library_sort.dart';
import 'chaptarr_library_sort.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';

/// Library filters for the Chaptarr author list. Mirrors SonarrFilter but for
/// the author-centric book library (continuing/ended are TV-only and dropped).
enum ChaptarrLibraryFilter { all, monitored, missing }

class ChaptarrLibraryState {
  final List<ChaptarrAuthor> authors;
  final List<ChaptarrAuthor> filtered;
  final bool isLoading;
  final String? error;
  final String searchQuery;
  final ChaptarrLibraryFilter filter;

  const ChaptarrLibraryState({
    this.authors = const [],
    this.filtered = const [],
    this.isLoading = false,
    this.error,
    this.searchQuery = '',
    this.filter = ChaptarrLibraryFilter.all,
  });

  ChaptarrLibraryState copyWith({
    List<ChaptarrAuthor>? authors,
    List<ChaptarrAuthor>? filtered,
    bool? isLoading,
    String? error,
    String? searchQuery,
    ChaptarrLibraryFilter? filter,
  }) =>
      ChaptarrLibraryState(
        authors: authors ?? this.authors,
        filtered: filtered ?? this.filtered,
        isLoading: isLoading ?? this.isLoading,
        error: error,
        searchQuery: searchQuery ?? this.searchQuery,
        filter: filter ?? this.filter,
      );

  int get monitoredCount => authors.where((a) => a.monitored).length;
  int get completeCount =>
      authors.where((a) => a.percentComplete >= 1.0).length;
  int get partialCount => authors
      .where((a) => a.percentComplete > 0 && a.percentComplete < 1.0)
      .length;
}

/// Holds the Chaptarr author library for one instance. A hand-rolled
/// ChangeNotifier (mirrors SonarrSeriesNotifier) instantiated per screen, so a
/// `ref.listen(activeChaptarrInstanceId)` re-init swaps instances cleanly.
class ChaptarrLibraryNotifier extends ChangeNotifier {
  final ChaptarrApiService _service;
  late final LibrarySortController sorting;
  bool _disposed = false;
  int _loadGeneration = 0;

  ChaptarrLibraryState _state = const ChaptarrLibraryState();
  ChaptarrLibraryState get state => _state;
  set state(ChaptarrLibraryState value) {
    if (_disposed) return;
    _state = value;
    notifyListeners();
  }

  ChaptarrLibraryNotifier(this._service) {
    sorting = LibrarySortController(module: 'chaptarr',
      loaders: {
        LibrarySortLookup.qualityProfiles: () async => {
          for (final profile in await _service.getQualityProfiles()) profile.id: profile.name,
        },
        LibrarySortLookup.metadataProfiles: () async => {
          for (final profile in await _service.getMetadataProfiles()) profile.id: profile.name,
        },
      }, onChanged: _resort);
  }

  void _resort() {
    state = state.copyWith(error: state.error,
      filtered: _applyFilters(state.authors, state.searchQuery, state.filter));
  }

  @override
  void dispose() {
    _disposed = true;
    sorting.dispose();
    super.dispose();
  }

  Future<void> loadAuthors() async {
    final generation = ++_loadGeneration;
    final labels = sorting.refresh();
    state = state.copyWith(isLoading: true);
    try {
      final authors = await _service.getAuthors();
      if (_disposed || generation != _loadGeneration) return;
      state = state.copyWith(
        isLoading: false,
        authors: authors,
        filtered: _applyFilters(authors, state.searchQuery, state.filter),
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
      filtered: _applyFilters(state.authors, query, state.filter),
    );
  }

  void setFilter(ChaptarrLibraryFilter filter) {
    state = state.copyWith(
      error: state.error,
      filter: filter,
      filtered: _applyFilters(state.authors, state.searchQuery, filter),
    );
  }

  Future<void> deleteAuthor(int id, {bool deleteFiles = false}) async {
    await _service.deleteAuthor(id, deleteFiles: deleteFiles);
    await loadAuthors();
  }

  Future<void> searchForAuthor(int authorId) async {
    await _service.searchAuthor(authorId);
  }

  List<ChaptarrAuthor> _applyFilters(
    List<ChaptarrAuthor> authors,
    String query,
    ChaptarrLibraryFilter filter,
  ) {
    var result = authors;

    if (query.isNotEmpty) {
      final q = query.toLowerCase();
      result =
          result.where((a) => a.authorName.toLowerCase().contains(q)).toList();
    }

    result = switch (filter) {
      ChaptarrLibraryFilter.all => result,
      ChaptarrLibraryFilter.monitored =>
        result.where((a) => a.monitored).toList(),
      ChaptarrLibraryFilter.missing => result
          .where((a) => a.monitored && a.percentComplete < 1.0)
          .toList(),
    };

    return sortChaptarrLibrary(result, sorting.effectiveSelection, sorting.labels);
  }
}
