import 'dart:async';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/config/app_config.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/logic/paged_loader.dart';

/// Shell-level search state visible across all tabs.
class ShellSearchState {
  final String searchQuery;
  final List<MediaItem> searchResults;
  final bool isLoadingSearch;
  final String? error;

  const ShellSearchState({
    this.searchQuery = '',
    this.searchResults = const [],
    this.isLoadingSearch = false,
    this.error,
  });

  ShellSearchState copyWith({
    String? searchQuery,
    List<MediaItem>? searchResults,
    bool? isLoadingSearch,
    String? error,
  }) =>
      ShellSearchState(
        searchQuery: searchQuery ?? this.searchQuery,
        searchResults: searchResults ?? this.searchResults,
        isLoadingSearch: isLoadingSearch ?? this.isLoadingSearch,
        error: error,
      );

  bool get isSearching => searchQuery.isNotEmpty;
}

/// Manages TMDB multi-search from the shell search bar.
class ShellSearchNotifier extends StateNotifier<ShellSearchState> {
  final DiscoverApiService _api;
  final PagedLoader _searchLoader = PagedLoader();
  Timer? _searchDebounce;

  ShellSearchNotifier(this._api) : super(const ShellSearchState());

  void updateSearch(String query) {
    state = state.copyWith(searchQuery: query);
    _searchDebounce?.cancel();

    if (query.isEmpty) {
      state = state.copyWith(searchResults: [], isLoadingSearch: false);
      _searchLoader.reset();
      return;
    }

    state = state.copyWith(isLoadingSearch: true);
    _searchDebounce = Timer(AppConfig.searchDebounce, () => _executeSearch());
  }

  Future<void> _executeSearch() async {
    _searchLoader.reset();
    if (!_searchLoader.beginLoading()) return;

    try {
      final page = await _api.multiSearch(
        query: state.searchQuery,
        page: _searchLoader.page,
      );
      state = state.copyWith(searchResults: page.results, isLoadingSearch: false);
      _searchLoader.endLoading(page.totalPages);
    } catch (e) {
      _searchLoader.cancelLoading();
      state = state.copyWith(
        isLoadingSearch: false,
        error: 'Search failed: $e',
      );
    }
  }

  void loadMoreSearch(MediaItem current) {
    final idx = state.searchResults.indexOf(current);
    if (idx >= state.searchResults.length - AppConfig.prefetchThreshold) {
      _loadMoreSearchResults();
    }
  }

  Future<void> _loadMoreSearchResults() async {
    if (!_searchLoader.beginLoading()) return;
    state = state.copyWith(isLoadingSearch: true);
    try {
      final page = await _api.multiSearch(
        query: state.searchQuery,
        page: _searchLoader.page,
      );
      state = state.copyWith(
        searchResults: [...state.searchResults, ...page.results],
        isLoadingSearch: false,
      );
      _searchLoader.endLoading(page.totalPages);
    } catch (_) {
      _searchLoader.cancelLoading();
      state = state.copyWith(isLoadingSearch: false);
    }
  }

  void clearError() => state = state.copyWith(error: null);

  @override
  void dispose() {
    _searchDebounce?.cancel();
    super.dispose();
  }
}

/// Provider for the shell search notifier.
final shellSearchProvider =
    StateNotifierProvider<ShellSearchNotifier, ShellSearchState>(
  (ref) {
    final api = ref.watch(discoverServiceProvider);
    return ShellSearchNotifier(api);
  },
);
