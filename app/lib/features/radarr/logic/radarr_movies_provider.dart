import 'package:flutter/foundation.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_models.dart';

/// State for the Radarr movies list.
class RadarrMoviesState {
  final List<RadarrMovie> movies;
  final List<RadarrMovie> filtered;
  final bool isLoading;
  final String? error;
  final String searchQuery;
  final RadarrFilter filter;

  const RadarrMoviesState({
    this.movies = const [],
    this.filtered = const [],
    this.isLoading = false,
    this.error,
    this.searchQuery = '',
    this.filter = RadarrFilter.all,
  });

  RadarrMoviesState copyWith({
    List<RadarrMovie>? movies,
    List<RadarrMovie>? filtered,
    bool? isLoading,
    String? error,
    String? searchQuery,
    RadarrFilter? filter,
  }) =>
      RadarrMoviesState(
        movies: movies ?? this.movies,
        filtered: filtered ?? this.filtered,
        isLoading: isLoading ?? this.isLoading,
        error: error,
        searchQuery: searchQuery ?? this.searchQuery,
        filter: filter ?? this.filter,
      );

  int get monitoredCount => movies.where((m) => m.monitored).length;
  int get downloadedCount => movies.where((m) => m.hasFile).length;
  int get missingCount =>
      movies.where((m) => m.monitored && !m.hasFile).length;
}

enum RadarrFilter { all, monitored, missing, downloaded }

class RadarrMoviesNotifier extends ChangeNotifier {
  final RadarrApiService _service;

  RadarrMoviesState _state = const RadarrMoviesState();
  RadarrMoviesState get state => _state;
  set state(RadarrMoviesState value) {
    _state = value;
    notifyListeners();
  }

  RadarrMoviesNotifier(this._service);

  Future<void> loadMovies() async {
    state = state.copyWith(isLoading: true);
    try {
      final movies = await _service.getMovies();
      movies.sort(
          (a, b) => a.title.toLowerCase().compareTo(b.title.toLowerCase()));
      state = state.copyWith(
        isLoading: false,
        movies: movies,
        filtered: _applyFilters(movies, state.searchQuery, state.filter),
      );
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        error: 'Failed to load movies: $e',
      );
    }
  }

  void search(String query) {
    state = state.copyWith(
      searchQuery: query,
      filtered: _applyFilters(state.movies, query, state.filter),
    );
  }

  void setFilter(RadarrFilter filter) {
    state = state.copyWith(
      filter: filter,
      filtered: _applyFilters(state.movies, state.searchQuery, filter),
    );
  }

  Future<void> deleteMovie(int id,
      {bool deleteFiles = false}) async {
    await _service.deleteMovie(id, deleteFiles: deleteFiles);
    await loadMovies();
  }

  Future<void> searchForMovie(int movieId) async {
    await _service.searchMovie(movieId);
  }

  List<RadarrMovie> _applyFilters(
    List<RadarrMovie> movies,
    String query,
    RadarrFilter filter,
  ) {
    var result = movies;

    // Text search
    if (query.isNotEmpty) {
      final q = query.toLowerCase();
      result = result
          .where((m) => m.title.toLowerCase().contains(q))
          .toList();
    }

    // Status filter
    result = switch (filter) {
      RadarrFilter.all => result,
      RadarrFilter.monitored =>
        result.where((m) => m.monitored).toList(),
      RadarrFilter.missing =>
        result.where((m) => m.monitored && !m.hasFile).toList(),
      RadarrFilter.downloaded =>
        result.where((m) => m.hasFile).toList(),
    };

    return result;
  }
}
