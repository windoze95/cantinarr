import 'dart:async';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/config/app_config.dart';
import '../../../core/network/backend_client.dart';
import '../data/discover_api_service.dart';
import '../data/tmdb_models.dart';
import 'paged_loader.dart';

/// Provides the unified discover service backed by the server.
final discoverServiceProvider = Provider<DiscoverApiService>(
  (ref) => DiscoverApiService(backendDio: ref.watch(backendClientProvider)),
);

/// The main state for the discover screen.
class DiscoverState {
  final List<MediaItem> trending;
  final List<MediaItem> popularMovies;
  final List<MediaItem> popularTV;
  final List<MediaItem> topRated;
  final List<MediaItem> upcoming;
  final List<MediaItem> searchResults;
  final bool isLoadingTrending;
  final bool isLoadingPopularMovies;
  final bool isLoadingPopularTV;
  final bool isLoadingSearch;
  final String? error;
  final String searchQuery;

  const DiscoverState({
    this.trending = const [],
    this.popularMovies = const [],
    this.popularTV = const [],
    this.topRated = const [],
    this.upcoming = const [],
    this.searchResults = const [],
    this.isLoadingTrending = false,
    this.isLoadingPopularMovies = false,
    this.isLoadingPopularTV = false,
    this.isLoadingSearch = false,
    this.error,
    this.searchQuery = '',
  });

  DiscoverState copyWith({
    List<MediaItem>? trending,
    List<MediaItem>? popularMovies,
    List<MediaItem>? popularTV,
    List<MediaItem>? topRated,
    List<MediaItem>? upcoming,
    List<MediaItem>? searchResults,
    bool? isLoadingTrending,
    bool? isLoadingPopularMovies,
    bool? isLoadingPopularTV,
    bool? isLoadingSearch,
    String? error,
    String? searchQuery,
  }) =>
      DiscoverState(
        trending: trending ?? this.trending,
        popularMovies: popularMovies ?? this.popularMovies,
        popularTV: popularTV ?? this.popularTV,
        topRated: topRated ?? this.topRated,
        upcoming: upcoming ?? this.upcoming,
        searchResults: searchResults ?? this.searchResults,
        isLoadingTrending: isLoadingTrending ?? this.isLoadingTrending,
        isLoadingPopularMovies:
            isLoadingPopularMovies ?? this.isLoadingPopularMovies,
        isLoadingPopularTV: isLoadingPopularTV ?? this.isLoadingPopularTV,
        isLoadingSearch: isLoadingSearch ?? this.isLoadingSearch,
        error: error,
        searchQuery: searchQuery ?? this.searchQuery,
      );

  bool get isSearching => searchQuery.isNotEmpty;
}

/// Manages all discover-related data and search.
class DiscoverNotifier extends StateNotifier<DiscoverState> {
  final DiscoverApiService _api;
  final PagedLoader _trendingLoader = PagedLoader();
  final PagedLoader _searchLoader = PagedLoader();
  Timer? _searchDebounce;

  DiscoverNotifier(this._api) : super(const DiscoverState());

  /// Called on first load – fetches trending, popular, top rated, upcoming.
  Future<void> bootstrap() async {
    await Future.wait([
      _loadTrending(),
      _loadPopularMovies(),
      _loadPopularTV(),
      _loadTopRated(),
      _loadUpcoming(),
    ]);
  }

  // ─── Trending ───────────────────────────────────────

  Future<void> _loadTrending() async {
    if (!_trendingLoader.beginLoading()) return;
    state = state.copyWith(isLoadingTrending: true);
    try {
      final page =
          await _api.fetchTrending(page: _trendingLoader.page);
      state = state.copyWith(
        trending: [...state.trending, ...page.results],
        isLoadingTrending: false,
      );
      _trendingLoader.endLoading(page.totalPages);
    } catch (e) {
      _trendingLoader.cancelLoading();
      state = state.copyWith(
        isLoadingTrending: false,
        error: 'Failed to load trending: $e',
      );
    }
  }

  void loadMoreTrending(MediaItem current) {
    final idx = state.trending.indexOf(current);
    if (idx >= state.trending.length - AppConfig.prefetchThreshold) {
      _loadTrending();
    }
  }

  // ─── Popular ────────────────────────────────────────

  Future<void> _loadPopularMovies() async {
    state = state.copyWith(isLoadingPopularMovies: true);
    try {
      final page = await _api.fetchPopularMovies();
      state = state.copyWith(
        popularMovies: page.results,
        isLoadingPopularMovies: false,
      );
    } catch (_) {
      state = state.copyWith(isLoadingPopularMovies: false);
    }
  }

  Future<void> _loadPopularTV() async {
    state = state.copyWith(isLoadingPopularTV: true);
    try {
      final page = await _api.fetchPopularTV();
      state = state.copyWith(
        popularTV: page.results,
        isLoadingPopularTV: false,
      );
    } catch (_) {
      state = state.copyWith(isLoadingPopularTV: false);
    }
  }

  Future<void> _loadTopRated() async {
    try {
      final page = await _api.fetchTopRatedMovies();
      state = state.copyWith(topRated: page.results);
    } catch (_) {}
  }

  Future<void> _loadUpcoming() async {
    try {
      final page = await _api.fetchUpcomingMovies();
      state = state.copyWith(upcoming: page.results);
    } catch (_) {}
  }

  // ─── Search ─────────────────────────────────────────

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
      // Filter out people from search results for cleaner UX
      final filtered =
          page.results.where((m) => m.mediaType != MediaType.person).toList();
      state = state.copyWith(searchResults: filtered, isLoadingSearch: false);
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
      final filtered =
          page.results.where((m) => m.mediaType != MediaType.person).toList();
      state = state.copyWith(
        searchResults: [...state.searchResults, ...filtered],
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

/// Provider for the discover notifier.
final discoverProvider =
    StateNotifierProvider<DiscoverNotifier, DiscoverState>(
  (ref) {
    final api = ref.watch(discoverServiceProvider);
    return DiscoverNotifier(api);
  },
);
