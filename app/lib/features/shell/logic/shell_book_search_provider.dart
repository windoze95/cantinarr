import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/config/app_config.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../chaptarr/data/chaptarr_api_service.dart';
import '../../chaptarr/data/chaptarr_models.dart';

/// The ways a Chaptarr book search can fail to return a usable result list.
/// Distinct from the success-but-empty case, which is expressed as
/// `error == null && searched == true && results.isEmpty` rather than a
/// fourth member here.
enum BookSearchError {
  /// No active Chaptarr instance is configured for this connection.
  noInstance,

  /// The active instance rejected the request (401/403).
  forbidden,

  /// Any other failure — network error, timeout, 5xx, malformed response.
  requestFailed,
}

/// Shell-level book-search state, scoped to the Books discovery tab.
class ShellBookSearchState {
  final String searchQuery;
  final List<ChaptarrBook> results;

  /// Author matches for [searchQuery], rendered below [results].
  final List<ChaptarrAuthor> authors;
  final bool isLoadingSearch;

  /// True once a lookup has completed successfully for [searchQuery] — the
  /// signal that distinguishes "no books found" from "hasn't searched yet".
  final bool searched;
  final BookSearchError? error;

  /// True when the book lookup succeeded but the author lookup did not.
  ///
  /// The two are separate Chaptarr calls, so an author failure must not throw
  /// away book results the user can still use. But it must not render as an
  /// empty author list either: per AGENTS.md an empty answer has to say whether
  /// it is absence or blindness, and "no authors matched" and "authors could
  /// not be searched" are different sentences.
  final bool authorsUnavailable;
  final bool authorsLoading;

  const ShellBookSearchState({
    this.searchQuery = '',
    this.results = const [],
    this.authors = const [],
    this.isLoadingSearch = false,
    this.searched = false,
    this.error,
    this.authorsUnavailable = false,
    this.authorsLoading = false,
  });

  /// [clearError] is explicit (not a plain nullable default) because a
  /// nullable-defaulted `copyWith` cannot set [error] back to null, and every
  /// successful result must clear a prior error.
  ShellBookSearchState copyWith({
    String? searchQuery,
    List<ChaptarrBook>? results,
    List<ChaptarrAuthor>? authors,
    bool? isLoadingSearch,
    bool? searched,
    BookSearchError? error,
    bool clearError = false,
    bool? authorsUnavailable,
    bool? authorsLoading,
  }) =>
      ShellBookSearchState(
        searchQuery: searchQuery ?? this.searchQuery,
        results: results ?? this.results,
        authors: authors ?? this.authors,
        isLoadingSearch: isLoadingSearch ?? this.isLoadingSearch,
        searched: searched ?? this.searched,
        error: clearError ? null : (error ?? this.error),
        authorsUnavailable: authorsUnavailable ?? this.authorsUnavailable,
        authorsLoading: authorsLoading ?? this.authorsLoading,
      );

  bool get isSearching => searchQuery.trim().isNotEmpty;
}

/// Manages Chaptarr book-lookup search from the shell search bar, scoped to
/// the Books discovery tab. Mirrors [ShellSearchNotifier]'s debounce and
/// generation-guard shape (`shell_search_provider.dart`), but with a typed
/// failure taxonomy instead of a free-text error, and no pagination —
/// `lookupBook` has no `page` parameter.
class ShellBookSearchNotifier extends StateNotifier<ShellBookSearchState> {
  final Ref _ref;
  Timer? _searchDebounce;
  int _searchGeneration = 0;
  CancelToken? _searchCancel;
  Timer? _searchDeadline;

  void _cancelSearch() {
    _searchCancel?.cancel('Search changed');
    _searchDeadline?.cancel();
  }

  ShellBookSearchNotifier(this._ref) : super(const ShellBookSearchState());

  void updateSearch(String query) {
    _searchDebounce?.cancel();
    _cancelSearch();
    final generation = ++_searchGeneration;

    if (query.trim().isEmpty) {
      state = const ShellBookSearchState();
      return;
    }

    state = state.copyWith(
      searchQuery: query,
      results: const [],
      authors: const [],
      isLoadingSearch: true,
      searched: false,
      clearError: true,
      authorsUnavailable: false,
      authorsLoading: true,
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
    bool superseded() =>
        !mounted ||
        generation != _searchGeneration ||
        state.searchQuery != query;

    // The no-instance check runs first and short-circuits before any
    // ChaptarrApiService is constructed or request issued — mirroring
    // dashboard_books_tab.dart's `_chaptarr() == null` guard — so no
    // 401/403 or generic failure can ever supersede FAIL-01.
    final instance = _ref.read(instanceProvider).activeChaptarrInstance;
    if (instance == null) {
      if (superseded()) return;
      state = state.copyWith(
        results: const [],
        authors: const [],
        isLoadingSearch: false,
        searched: false,
        authorsUnavailable: false,
        authorsLoading: false,
        error: BookSearchError.noInstance,
      );
      return;
    }

    final service = ChaptarrApiService(
      backendDio: _ref.read(backendClientProvider),
      instanceId: instance.id,
    );

    // Cancel both sockets at the interactive deadline, including connection
    // time. Each result publishes independently under the same generation.
    final token = CancelToken();
    _searchCancel = token;
    _searchDeadline = Timer(const Duration(seconds: 10), () {
      if (superseded()) return;
      token.cancel('Search timed out');
      state = state.copyWith(
        isLoadingSearch: false,
        authorsLoading: false,
        authorsUnavailable: state.authorsLoading,
        error:
            state.isLoadingSearch ? BookSearchError.requestFailed : state.error,
      );
    });
    void finishDeadline() {
      if (!superseded() && !state.isLoadingSearch && !state.authorsLoading) {
        _searchDeadline?.cancel();
      }
    }

    final term = query.trim();
    unawaited(() async {
      try {
        final authors = await service.lookupAuthor(term, cancelToken: token);
        if (superseded() || token.isCancelled) return;
        state = state.copyWith(
            authors: authors, authorsLoading: false, authorsUnavailable: false);
      } catch (_) {
        if (superseded() || token.isCancelled) return;
        state = state.copyWith(authorsLoading: false, authorsUnavailable: true);
      } finally {
        finishDeadline();
      }
    }());

    try {
      final books = await service.lookupBook(term, cancelToken: token);
      if (superseded() || token.isCancelled) return;
      state = state.copyWith(
        results: books,
        isLoadingSearch: false,
        searched: true,
        clearError: true,
      );
    } on DioException catch (e) {
      if (kDebugMode) {
        debugPrint(
          'ShellBookSearchNotifier: book lookup failed with '
          '${e.runtimeType}, status ${e.response?.statusCode}',
        );
      }
      if (superseded() || token.isCancelled) return;
      final code = e.response?.statusCode;
      state = state.copyWith(
        // A failed search owns the whole overlay, so the previous query's
        // authors must go with its books — leaving them rendered behind an
        // error message would attribute them to a search that never ran.
        results: const [],
        authors: const [],
        isLoadingSearch: false,
        searched: false,
        authorsUnavailable: false,
        error: code == 401 || code == 403
            ? BookSearchError.forbidden
            : BookSearchError.requestFailed,
      );
    } catch (error) {
      if (kDebugMode) {
        debugPrint(
          'ShellBookSearchNotifier: book lookup failed with '
          '${error.runtimeType}',
        );
      }
      if (superseded() || token.isCancelled) return;
      state = state.copyWith(
        results: const [],
        authors: const [],
        isLoadingSearch: false,
        searched: false,
        authorsUnavailable: false,
        error: BookSearchError.requestFailed,
      );
    } finally {
      finishDeadline();
    }
  }

  void reset() {
    _searchDebounce?.cancel();
    _cancelSearch();
    _searchGeneration++;
    state = const ShellBookSearchState();
  }

  /// BOOK-07: the active Chaptarr instance changed. Discard whatever the
  /// previous instance's results were and re-run the currently typed query
  /// against the new instance — an instance switch is a deliberate act, not
  /// a keystroke, so this issues the request immediately rather than
  /// re-debouncing. Bumping the generation before touching state means any
  /// in-flight response from the *old* instance lands on a stale generation
  /// and is dropped by [_executeSearch]'s existing guard.
  void rerunForInstance() {
    _searchDebounce?.cancel();
    _cancelSearch();
    final generation = ++_searchGeneration;
    final query = state.searchQuery;

    if (query.trim().isEmpty) {
      // Switching instances with an empty toolbar discards nothing and
      // searches nothing.
      state = const ShellBookSearchState();
      return;
    }

    state = state.copyWith(
      results: const [],
      authors: const [],
      authorsLoading: true,
      authorsUnavailable: false,
      isLoadingSearch: true,
      searched: false,
      clearError: true,
    );
    _executeSearch(query: query, generation: generation);
  }

  @override
  void dispose() {
    _searchDebounce?.cancel();
    _cancelSearch();
    super.dispose();
  }
}

final shellBookSearchProvider =
    StateNotifierProvider<ShellBookSearchNotifier, ShellBookSearchState>(
  (ref) => ShellBookSearchNotifier(ref),
);
