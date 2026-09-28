import 'package:flutter/foundation.dart';

import '../../../core/logic/library_sort_controller.dart';
import '../../../core/models/library_sort.dart';
import 'sonarr_library_sort.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';

class SonarrSeriesState {
  final List<SonarrSeries> series;
  final List<SonarrSeries> filtered;
  final bool isLoading;
  final String? error;
  final String searchQuery;
  final SonarrFilter filter;

  const SonarrSeriesState({
    this.series = const [],
    this.filtered = const [],
    this.isLoading = false,
    this.error,
    this.searchQuery = '',
    this.filter = SonarrFilter.all,
  });

  SonarrSeriesState copyWith({
    List<SonarrSeries>? series,
    List<SonarrSeries>? filtered,
    bool? isLoading,
    String? error,
    String? searchQuery,
    SonarrFilter? filter,
  }) =>
      SonarrSeriesState(
        series: series ?? this.series,
        filtered: filtered ?? this.filtered,
        isLoading: isLoading ?? this.isLoading,
        error: error,
        searchQuery: searchQuery ?? this.searchQuery,
        filter: filter ?? this.filter,
      );

  int get monitoredCount => series.where((s) => s.monitored).length;
  int get completeCount =>
      series.where((s) => s.percentComplete >= 1.0).length;
  int get partialCount => series
      .where((s) => s.percentComplete > 0 && s.percentComplete < 1.0)
      .length;
}

enum SonarrFilter { all, monitored, continuing, ended, missing }

class SonarrSeriesNotifier extends ChangeNotifier {
  final SonarrApiService _service;
  late final LibrarySortController sorting;
  bool _disposed = false;
  int _loadGeneration = 0;

  SonarrSeriesState _state = const SonarrSeriesState();
  SonarrSeriesState get state => _state;
  set state(SonarrSeriesState value) {
    if (_disposed) return;
    _state = value;
    notifyListeners();
  }

  SonarrSeriesNotifier(this._service) {
    sorting = LibrarySortController(module: 'sonarr',
      loaders: {
        LibrarySortLookup.qualityProfiles: () async => {
          for (final profile in await _service.getQualityProfiles()) profile.id: profile.name,
        },
        LibrarySortLookup.tags: () async => {
          for (final tag in await _service.getTags()) tag.id: tag.label,
        },
      }, onChanged: _resort);
  }

  void _resort() {
    state = state.copyWith(error: state.error,
      filtered: _applyFilters(state.series, state.searchQuery, state.filter));
  }

  @override
  void dispose() {
    _disposed = true;
    sorting.dispose();
    super.dispose();
  }

  Future<void> loadSeries() async {
    final generation = ++_loadGeneration;
    final labels = sorting.refresh();
    state = state.copyWith(isLoading: true);
    try {
      final series = await _service.getSeries();
      if (_disposed || generation != _loadGeneration) return;
      state = state.copyWith(
        isLoading: false,
        series: series,
        filtered: _applyFilters(series, state.searchQuery, state.filter),
      );
    } catch (e) {
      if (_disposed || generation != _loadGeneration) return;
      state = state.copyWith(
        isLoading: false,
        error: 'Failed to load series: $e',
      );
    } finally {
      await labels;
    }
  }

  void search(String query) {
    state = state.copyWith(
      error: state.error,
      searchQuery: query,
      filtered: _applyFilters(state.series, query, state.filter),
    );
  }

  void setFilter(SonarrFilter filter) {
    state = state.copyWith(
      error: state.error,
      filter: filter,
      filtered: _applyFilters(state.series, state.searchQuery, filter),
    );
  }

  Future<void> deleteSeries(int id, {bool deleteFiles = false}) async {
    await _service.deleteSeries(id, deleteFiles: deleteFiles);
    await loadSeries();
  }

  Future<void> searchForSeries(int seriesId) async {
    await _service.searchSeries(seriesId);
  }

  List<SonarrSeries> _applyFilters(
    List<SonarrSeries> series,
    String query,
    SonarrFilter filter,
  ) {
    var result = series;

    if (query.isNotEmpty) {
      final q = query.toLowerCase();
      result = result.where((s) => s.title.toLowerCase().contains(q)).toList();
    }

    result = switch (filter) {
      SonarrFilter.all => result,
      SonarrFilter.monitored => result.where((s) => s.monitored).toList(),
      SonarrFilter.continuing =>
        result.where((s) => s.status == 'continuing').toList(),
      SonarrFilter.ended => result.where((s) => s.status == 'ended').toList(),
      SonarrFilter.missing => result
          .where((s) => s.monitored && s.percentComplete < 1.0)
          .toList(),
    };

    return sortSonarrLibrary(result, sorting.effectiveSelection, sorting.labels);
  }
}
