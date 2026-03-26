import 'dart:async';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/config/app_config.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/logic/paged_loader.dart';

/// The current mode of the shell search bar.
enum SearchMode {
  /// Normal search — bar at top, results overlay below.
  search,

  /// No results found and AI is available — glow transition state.
  noResults,

  /// AI ready — search bar stays at top, expands to multiline with shimmer glow.
  aiReady,

  /// AI chat mode — bar at bottom (multiline), chat messages above.
  aiChat,
}

/// Shell-level search state visible across all tabs.
class ShellSearchState {
  final String searchQuery;
  final List<MediaItem> searchResults;
  final bool isLoadingSearch;
  final String? error;
  final SearchMode searchMode;

  const ShellSearchState({
    this.searchQuery = '',
    this.searchResults = const [],
    this.isLoadingSearch = false,
    this.error,
    this.searchMode = SearchMode.search,
  });

  ShellSearchState copyWith({
    String? searchQuery,
    List<MediaItem>? searchResults,
    bool? isLoadingSearch,
    String? error,
    SearchMode? searchMode,
  }) =>
      ShellSearchState(
        searchQuery: searchQuery ?? this.searchQuery,
        searchResults: searchResults ?? this.searchResults,
        isLoadingSearch: isLoadingSearch ?? this.isLoadingSearch,
        error: error,
        searchMode: searchMode ?? this.searchMode,
      );

  bool get isSearching => searchQuery.isNotEmpty;
}

/// Manages TMDB multi-search from the shell search bar.
class ShellSearchNotifier extends StateNotifier<ShellSearchState> {
  final DiscoverApiService _api;
  final bool aiAvailable;
  final PagedLoader _searchLoader = PagedLoader();
  Timer? _searchDebounce;

  ShellSearchNotifier(this._api, {this.aiAvailable = false})
      : super(const ShellSearchState());

  void updateSearch(String query) {
    _searchDebounce?.cancel();

    if (query.isEmpty) {
      state = state.copyWith(
        searchQuery: '',
        searchResults: [],
        isLoadingSearch: false,
        searchMode: SearchMode.search,
      );
      _searchLoader.reset();
      return;
    }

    // In aiReady mode, just update the query text — don't re-search TMDB.
    if (state.searchMode == SearchMode.aiReady) {
      state = state.copyWith(searchQuery: query);
      return;
    }

    state = state.copyWith(
      searchQuery: query,
      isLoadingSearch: true,
      searchMode: SearchMode.search,
    );
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

      if (page.results.isEmpty && aiAvailable) {
        state = state.copyWith(
          searchResults: [],
          isLoadingSearch: false,
          searchMode: SearchMode.noResults,
        );
      } else {
        state = state.copyWith(
          searchResults: page.results,
          isLoadingSearch: false,
        );
      }
      _searchLoader.endLoading(page.totalPages);
    } catch (e) {
      _searchLoader.cancelLoading();
      state = state.copyWith(
        isLoadingSearch: false,
        error: 'Search failed: $e',
      );
    }
  }

  /// Transition from noResults to AI-ready mode (expanded search bar + shimmer).
  void activateAiChat() {
    if (state.searchMode == SearchMode.noResults) {
      state = state.copyWith(searchMode: SearchMode.aiReady);
    }
  }

  /// Transition from aiReady to full AI chat mode (after user submits).
  void submitAiReady() {
    if (state.searchMode == SearchMode.aiReady) {
      state = state.copyWith(searchMode: SearchMode.aiChat);
    }
  }

  /// Exit AI mode and return to normal search.
  void exitAiMode() {
    state = state.copyWith(
      searchMode: SearchMode.search,
      searchQuery: '',
      searchResults: [],
      isLoadingSearch: false,
    );
    _searchLoader.reset();
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
    final auth = ref.watch(authProvider).valueOrNull;
    final hasAi = auth?.connection?.services.ai ?? false;
    return ShellSearchNotifier(api, aiAvailable: hasAi);
  },
);
