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

  /// AI ready — search bar stays at top, expands to multiline with shimmer glow.
  aiReady,
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

  // Deliberately absent because they collide with real titles: bare
  // 'looking for ' (Looking for Alaska), 'in the mood' without the I'm
  // (In the Mood for Love), bare 'what to ' (What to Expect When You're
  // Expecting), 'what happens' (What Happens in Vegas), and bare
  // 'best '/'top ' (Best in Show, Top Gun).
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
    'i feel like ',
    "i'm in the mood",
    'im in the mood',
    'im looking for ',
    "i'm looking for ",
    'need something ',
    'movies like ',
    'shows like ',
    'films like ',
    'series like ',
    'books like ',
    'authors like ',
    'anime like ',
    'documentaries like ',
    'something like ',
    'more like ',
    'similar to ',
    'anything with ',
    'something with ',
    'something to watch',
    'what to watch',
    'what to read',
    'where to watch',
    'where to stream',
    'compare ',
    'explain ',
    'summarize ',
    'summarise ',
    'top 10 ',
    'top ten ',
    'any good ',
    'any recommendations',
    'any suggestions',
    'name a ',
    'name some ',
    'pick a ',
    'pick something ',
    'list all ',
    'list every ',
    'surprise me',
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
    RegExp(r'^(is|are)\s+there\b'),
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

  /// True from an explicit [enterAiMode] (the "Ask AI" pill) until
  /// [exitAiMode] or the field emptying. Unlike the typed-question
  /// heuristic, it pins aiReady through non-empty edits — deletions and
  /// rewrites included; reaching zero characters turns AI mode off.
  bool _manualAiMode = false;

  ShellSearchNotifier(this._api, {this.aiAvailable = false})
      : super(const ShellSearchState());

  void updateSearch(String query) {
    final trimmed = query.trim();
    final normalized = trimmed.toLowerCase();
    final previousNormalized = state.searchQuery.trim().toLowerCase();
    final continuingAiReadyInput = aiAvailable &&
        state.searchMode == SearchMode.aiReady &&
        previousNormalized.isNotEmpty &&
        normalized.length >= previousNormalized.length &&
        normalized.startsWith(previousNormalized);
    _searchDebounce?.cancel();
    final generation = ++_searchGeneration;
    final mode = _manualAiMode ||
            continuingAiReadyInput ||
            (aiAvailable && isAiPromptQuery(trimmed))
        ? SearchMode.aiReady
        : SearchMode.search;

    if (trimmed.isEmpty) {
      _manualAiMode = false;
      state = state.copyWith(
        searchQuery: '',
        searchResults: [],
        isLoadingSearch: false,
        searchMode: SearchMode.search,
      );
      _searchLoader.reset();
      return;
    }

    state = state.copyWith(
      searchQuery: query,
      searchResults: [],
      isLoadingSearch: true,
      searchMode: mode,
    );
    _searchDebounce = Timer(
      AppConfig.searchDebounce,
      () => _executeSearch(query: query, generation: generation, mode: mode),
    );
  }

  Future<void> _executeSearch({
    required String query,
    required int generation,
    required SearchMode mode,
  }) async {
    _searchLoader.reset();
    if (!_searchLoader.beginLoading()) return;

    try {
      final page = await _api.multiSearch(
        query: query,
        page: _searchLoader.page,
      );

      if (!mounted ||
          generation != _searchGeneration ||
          state.searchQuery != query ||
          state.searchMode != mode) {
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
      if (!mounted ||
          generation != _searchGeneration ||
          state.searchQuery != query ||
          state.searchMode != mode) {
        return;
      }
      _searchLoader.cancelLoading();
      state = state.copyWith(
        isLoadingSearch: false,
        error: 'Search failed: $e',
      );
    }
  }

  /// Explicitly enter AI mode (the search bar's "Ask AI" pill).
  ///
  /// Sticky, unlike the typed-question heuristic: the mode survives every
  /// non-empty edit until send or clear calls [exitAiMode], or the field
  /// empties. Already-typed text is kept, and a fetch that was scheduled
  /// under normal search is re-run so the results backing the aiReady
  /// overlay still arrive.
  void enterAiMode() {
    if (!aiAvailable || state.searchMode == SearchMode.aiReady) return;
    _manualAiMode = true;
    _searchDebounce?.cancel();
    final generation = ++_searchGeneration;
    final query = state.searchQuery;
    final needsFetch = query.trim().isNotEmpty &&
        (state.isLoadingSearch || state.searchResults.isEmpty);
    state = state.copyWith(
      searchMode: SearchMode.aiReady,
      isLoadingSearch: needsFetch,
    );
    if (needsFetch) {
      // The query is already settled (the user is switching modes, not
      // typing), so skip the debounce.
      _executeSearch(
        query: query,
        generation: generation,
        mode: SearchMode.aiReady,
      );
    }
  }

  /// Exit the AI-ready hand-off and return to normal search.
  void exitAiMode() {
    _manualAiMode = false;
    _searchDebounce?.cancel();
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
    final mode = state.searchMode;
    state = state.copyWith(isLoadingSearch: true);
    try {
      final page = await _api.multiSearch(
        query: query,
        page: _searchLoader.page,
      );
      if (!mounted ||
          generation != _searchGeneration ||
          state.searchQuery != query ||
          state.searchMode != mode) {
        return;
      }
      state = state.copyWith(
        searchResults: [...state.searchResults, ...page.results],
        isLoadingSearch: false,
      );
      _searchLoader.endLoading(page.totalPages);
    } catch (_) {
      if (!mounted ||
          generation != _searchGeneration ||
          state.searchQuery != query ||
          state.searchMode != mode) {
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
