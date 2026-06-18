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

/// Lightweight client-side intent hint for the unified search/AI bar.
///
/// Title-like input still goes to TMDB search. Question phrases and common
/// assistant commands should move directly to AI so matching media titles do
/// not steal an obvious AI prompt.
bool isAiPromptQuery(String query) {
  final normalized = query.trim().toLowerCase();
  if (normalized.isEmpty) return false;
  if (normalized.endsWith('?')) return true;

  const commandPrefixes = [
    'tell me ',
    'recommend ',
    'suggest ',
    'show me ',
    'find me ',
    'help me ',
    'give me ',
    'i want ',
    'i need ',
    'im looking for ',
    "i'm looking for ",
    'movies like ',
    'shows like ',
  ];

  if (commandPrefixes.any(normalized.startsWith)) return true;

  final questionPatterns = [
    RegExp(
      r'^what\s+(is|are|was|were|should|can|could|do|does|did|would|will)\b',
    ),
    RegExp(r"^(whats|what's)\s+"),
    RegExp(
      r'^who\s+(is|are|was|were|plays|played|stars|starred|directed|wrote|made)\b',
    ),
    RegExp(r'^when\s+(is|are|was|were|did|does|do|will|can|should)\b'),
    RegExp(r'^where\s+(is|are|was|were|can|could|do|does|did)\b'),
    RegExp(r'^why\s+(is|are|was|were|did|does|do|would|should)\b'),
    RegExp(
      r'^how\s+(do|does|did|can|could|would|should|is|are|was|were|many|much|long|old|good)\b',
    ),
    RegExp(r'^which\s+'),
    RegExp(r'^(can|could|would|should|do|does|did)\s+(you|i|we)\b'),
    RegExp(
      r'^(is|are|was|were)\s+.+\b(available|streaming|worth|good|downloaded|missing|requested|on)\b',
    ),
  ];

  return questionPatterns.any((pattern) => pattern.hasMatch(normalized));
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
  int _searchGeneration = 0;

  ShellSearchNotifier(this._api, {this.aiAvailable = false})
      : super(const ShellSearchState());

  void updateSearch(String query) {
    final trimmed = query.trim();
    _searchDebounce?.cancel();
    final generation = ++_searchGeneration;

    if (trimmed.isEmpty) {
      state = state.copyWith(
        searchQuery: '',
        searchResults: [],
        isLoadingSearch: false,
        searchMode: SearchMode.search,
      );
      _searchLoader.reset();
      return;
    }

    if (aiAvailable && isAiPromptQuery(trimmed)) {
      state = state.copyWith(
        searchQuery: query,
        searchResults: [],
        isLoadingSearch: false,
        searchMode: SearchMode.aiReady,
      );
      _searchLoader.reset();
      return;
    }

    state = state.copyWith(
      searchQuery: query,
      isLoadingSearch: true,
      searchMode: SearchMode.search,
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
    _searchLoader.reset();
    if (!_searchLoader.beginLoading()) return;

    try {
      final page = await _api.multiSearch(
        query: query,
        page: _searchLoader.page,
      );

      if (generation != _searchGeneration ||
          state.searchQuery != query ||
          state.searchMode != SearchMode.search) {
        return;
      }

      if (page.results.isEmpty && aiAvailable) {
        state = state.copyWith(
          searchResults: [],
          isLoadingSearch: false,
          searchMode: SearchMode.aiReady,
        );
      } else {
        state = state.copyWith(
          searchResults: page.results,
          isLoadingSearch: false,
        );
      }
      _searchLoader.endLoading(page.totalPages);
    } catch (e) {
      if (generation != _searchGeneration ||
          state.searchQuery != query ||
          state.searchMode != SearchMode.search) {
        return;
      }
      _searchLoader.cancelLoading();
      state = state.copyWith(
        isLoadingSearch: false,
        error: 'Search failed: $e',
      );
    }
  }

  /// Transition from top-bar intent capture to full inline AI chat mode.
  void beginAiChat() {
    _searchDebounce?.cancel();
    _searchGeneration++;
    _searchLoader.reset();
    state = state.copyWith(
      searchMode: SearchMode.aiChat,
      searchQuery: '',
      searchResults: [],
      isLoadingSearch: false,
    );
  }

  /// Exit AI mode and return to normal search.
  void exitAiMode() {
    _searchGeneration++;
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
    final generation = _searchGeneration;
    final query = state.searchQuery;
    state = state.copyWith(isLoadingSearch: true);
    try {
      final page = await _api.multiSearch(
        query: query,
        page: _searchLoader.page,
      );
      if (generation != _searchGeneration ||
          state.searchQuery != query ||
          state.searchMode != SearchMode.search) {
        return;
      }
      state = state.copyWith(
        searchResults: [...state.searchResults, ...page.results],
        isLoadingSearch: false,
      );
      _searchLoader.endLoading(page.totalPages);
    } catch (_) {
      if (generation != _searchGeneration ||
          state.searchQuery != query ||
          state.searchMode != SearchMode.search) {
        return;
      }
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
