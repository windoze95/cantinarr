import 'package:flutter/foundation.dart';
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

  SonarrSeriesState _state = const SonarrSeriesState();
  SonarrSeriesState get state => _state;
  set state(SonarrSeriesState value) {
    _state = value;
    notifyListeners();
  }

  SonarrSeriesNotifier(this._service);

  Future<void> loadSeries() async {
    state = state.copyWith(isLoading: true);
    try {
      final series = await _service.getSeries();
      series.sort(
          (a, b) => a.title.toLowerCase().compareTo(b.title.toLowerCase()));
      state = state.copyWith(
        isLoading: false,
        series: series,
        filtered: _applyFilters(series, state.searchQuery, state.filter),
      );
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        error: 'Failed to load series: $e',
      );
    }
  }

  void search(String query) {
    state = state.copyWith(
      searchQuery: query,
      filtered: _applyFilters(state.series, query, state.filter),
    );
  }

  void setFilter(SonarrFilter filter) {
    state = state.copyWith(
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

    return result;
  }
}
