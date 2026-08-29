import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/automation/web_semantics.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/models/app_module.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/module_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/storage/preferences.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/search_bar.dart';
import '../../../core/widgets/shimmer_border.dart';
import '../../ai_assistant/logic/ai_chat_provider.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/logic/search_library_status.dart';
import '../../discover/ui/book_search_results_view.dart';
import '../../discover/ui/search_results_view.dart';
import '../../issues/logic/issues_provider.dart';
import '../../media_access/data/media_access_service.dart';
import '../../profile_proposals/logic/profile_proposals_provider.dart';
import '../../radarr/data/radarr_api_service.dart';
import '../../radarr/logic/radarr_movies_provider.dart';
import '../../request/logic/pending_approvals_provider.dart';
import '../../settings/logic/plex_invites_provider.dart';
import '../../settings/logic/setup_status_provider.dart';
import '../../sonarr/data/sonarr_api_service.dart';
import '../../sonarr/logic/sonarr_series_provider.dart';
import '../logic/shell_book_search_provider.dart';
import '../logic/shell_search_provider.dart';

/// The root shell widget with persistent search bar and navigation chrome.
/// On mobile/tablet that chrome is a hamburger drawer plus per-module bottom
/// navs (inner StatefulShellRoutes); on desktop it is a persistent sidebar
/// whose active module expands into its pages, replacing the bottom nav.
class AppShell extends ConsumerStatefulWidget {
  /// Current location inside the shell (e.g. `/radarr/queue`), used to
  /// highlight the active module and page in the desktop sidebar.
  final String currentPath;
  final Widget child;

  const AppShell({
    super.key,
    required this.currentPath,
    required this.child,
  });

  @override
  ConsumerState<AppShell> createState() => _AppShellState();
}

class _AppShellState extends ConsumerState<AppShell>
    with TickerProviderStateMixin, WidgetsBindingObserver {
  final _scaffoldKey = GlobalKey<ScaffoldState>();
  final _searchController = TextEditingController();
  final _searchFocusNode = FocusNode();
  RadarrMoviesNotifier? _radarrNotifier;
  SonarrSeriesNotifier? _sonarrNotifier;

  /// When the search-chip library snapshot was last (re)loaded, for throttling
  /// the passive refresh triggers.
  DateTime? _lastLibraryLoad;
  Timer? _libraryRefreshDebounce;

  /// Floor between passive snapshot refreshes (search focus, app resume).
  static const _libraryRefreshThrottle = Duration(seconds: 30);

  // Search bar collapse on scroll (mobile)
  late final AnimationController _searchBarAnim;
  late final Animation<double> _searchBarCurve;

  /// Last module route the search bar was shown over. Its collapse state
  /// belongs to that page, so it survives a pushed route and resets when a
  /// different module page takes over.
  String? _searchBarPath;

  // Shimmer sweep rotation for aiReady state
  late final AnimationController _shimmerRotationAnim;

  /// Idle gate for the Ask AI pill: true only once the user has typed and
  /// then paused. Focus alone never sets it, so the pill reads as a
  /// response to hesitation mid-entry, never a pop-up racing the keyboard.
  /// Never trust this flag alone: the clear X empties the field without
  /// firing onChanged, so pill visibility must also require non-empty text.
  bool _searchIdle = false;
  Timer? _askAiIdleTimer;

  /// Longer than a between-words typing pause, short enough to feel like a
  /// response to hesitation.
  static const _askAiPillIdleDelay = Duration(milliseconds: 1200);

  SearchMode _prevMode = SearchMode.search;
  bool? _prevReduceMotion;

  @override
  void initState() {
    super.initState();
    _searchBarAnim = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 200),
      value: 1.0,
    );
    _searchBarCurve = CurvedAnimation(
      parent: _searchBarAnim,
      curve: Curves.easeOut,
    );
    _shimmerRotationAnim = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 3000),
    );
    WidgetsBinding.instance.addObserver(this);
    _searchFocusNode.addListener(_onSearchFocusChanged);
    _searchBarPath = _searchBarPathFor(widget.currentPath);
    WidgetsBinding.instance.addPostFrameCallback((_) => _initLibraries());
  }

  @override
  void didUpdateWidget(covariant AppShell oldWidget) {
    super.didUpdateWidget(oldWidget);
    // A different module page means a fresh scroll view sitting at the top, so
    // the search bar comes back with it. Pushed routes (detail pages, forms)
    // aren't module pages, so popping back leaves the bar as its page had it.
    final path = _searchBarPathFor(widget.currentPath);
    if (path != null && path != _searchBarPath) {
      _searchBarPath = path;
      _searchBarAnim.forward();

      // SEARCH-04: a query must not outlive the discovery tab that produced
      // it. Clear the controller synchronously (no onChanged fires from a
      // programmatic clear, so this alone triggers no search) and cancel any
      // pending Ask AI idle timer. Both search notifiers are reset on the
      // next frame regardless of which tab is being entered or left, so no
      // stale debounce from the outgoing context can fire into the
      // incoming one.
      _searchController.clear();
      _cancelAskAiIdle();
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        ref.read(shellSearchProvider.notifier).updateSearch('');
        ref.read(shellBookSearchProvider.notifier).reset();
      });
    }
  }

  /// [path] when it owns the search bar, else null (pushed routes don't).
  static String? _searchBarPathFor(String path) =>
      _moduleTypeForPath(path) == null ? null : path;

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // The libraries may have changed while the app was backgrounded (downloads
    // finishing, an admin working directly in the arrs) — re-pull the chips'
    // snapshot.
    if (state == AppLifecycleState.resumed) _refreshLibraries();
  }

  void _onSearchFocusChanged() {
    // About to search: make sure the chips aren't serving a stale snapshot.
    if (_searchFocusNode.hasFocus) _refreshLibraries();
    // Losing focus ends any pending pause; a later refocus never resurfaces
    // the pill on its own — only typing does.
    if (!_searchFocusNode.hasFocus) _cancelAskAiIdle();
  }

  void _cancelAskAiIdle() {
    _askAiIdleTimer?.cancel();
    if (_searchIdle) setState(() => _searchIdle = false);
  }

  /// Re-arms the pause detector behind the Ask AI pill. Runs on every
  /// keystroke — and only there: the pill appears [_askAiPillIdleDelay]
  /// after the user stops typing, never from focus alone. An emptied field
  /// never arms it: nothing typed means nothing to hand the AI.
  void _resetAskAiIdle() {
    _cancelAskAiIdle();
    if (!_searchFocusNode.hasFocus) return;
    if (_searchController.text.trim().isEmpty) return;
    _askAiIdleTimer = Timer(_askAiPillIdleDelay, () {
      if (!mounted) return;
      setState(() => _searchIdle = true);
    });
  }

  void _initLibraries() {
    final auth = ref.read(authProvider).valueOrNull;
    final backendDio = ref.read(backendClientProvider);

    // Use instance-aware API services
    final defaultRadarr = auth?.connection?.defaultRadarrInstance;
    if (defaultRadarr != null) {
      _radarrNotifier = RadarrMoviesNotifier(
        RadarrApiService(backendDio: backendDio, instanceId: defaultRadarr.id),
      );
      _radarrNotifier!.addListener(_onLibraryChanged);
      _radarrNotifier!.loadMovies();
    }

    final defaultSonarr = auth?.connection?.defaultSonarrInstance;
    if (defaultSonarr != null) {
      _sonarrNotifier = SonarrSeriesNotifier(
        SonarrApiService(backendDio: backendDio, instanceId: defaultSonarr.id),
      );
      _sonarrNotifier!.addListener(_onLibraryChanged);
      _sonarrNotifier!.loadSeries();
    }

    _lastLibraryLoad = DateTime.now();
  }

  void _disposeLibraries() {
    _radarrNotifier?.removeListener(_onLibraryChanged);
    _sonarrNotifier?.removeListener(_onLibraryChanged);
    _radarrNotifier = null;
    _sonarrNotifier = null;
  }

  /// Re-pulls the search-chip library snapshot (see
  /// [buildSearchLibraryStatus]),
  /// which is otherwise loaded once per session and would drift whenever the
  /// libraries change without this app's involvement. Passive triggers are
  /// throttled by [_libraryRefreshThrottle]; [force] callers (websocket pings,
  /// a submitted request, an instance change) signal a real change and skip it.
  void _refreshLibraries({bool force = false}) {
    if (_radarrNotifier == null && _sonarrNotifier == null) {
      // Login-time init found no default instances; the connection may carry
      // some now (e.g. an admin granted one mid-session).
      _initLibraries();
      return;
    }
    final now = DateTime.now();
    if (!force &&
        _lastLibraryLoad != null &&
        now.difference(_lastLibraryLoad!) < _libraryRefreshThrottle) {
      return;
    }
    _lastLibraryLoad = now;
    _radarrNotifier?.loadMovies();
    _sonarrNotifier?.loadSeries();
  }

  /// Coalesces bursts of websocket pings into a single snapshot refresh.
  void _scheduleLibraryRefresh() {
    _libraryRefreshDebounce?.cancel();
    _libraryRefreshDebounce = Timer(const Duration(seconds: 3), () {
      if (mounted) _refreshLibraries(force: true);
    });
  }

  void _onLibraryChanged() {
    if (mounted) setState(() {});
  }

  void _dismissKeyboard() {
    _searchFocusNode.unfocus();
    FocusManager.instance.primaryFocus?.unfocus();
  }

  /// React to the AI-ready hand-off and drive its bounded visual state.
  void _onSearchModeChanged(SearchMode mode, {required bool reduceMotion}) {
    if (mode == _prevMode && reduceMotion == _prevReduceMotion) return;
    _prevMode = mode;
    _prevReduceMotion = reduceMotion;

    switch (mode) {
      case SearchMode.aiReady:
        if (reduceMotion) {
          _shimmerRotationAnim
            ..stop()
            ..value = 0;
        } else {
          _shimmerRotationAnim.repeat();
        }

      case SearchMode.search:
        _shimmerRotationAnim.stop();
        _shimmerRotationAnim.value = 0;
    }
  }

  void _exitAiMode() {
    _searchController.clear();
    ref.read(shellSearchProvider.notifier).exitAiMode();
    // GAP-SC1 desync: reset the book notifier too, so a book result hidden
    // by an AI-mode entry can never reappear stale after a clear.
    ref.read(shellBookSearchProvider.notifier).reset();
    _dismissKeyboard();
  }

  /// Explicit entry into AI mode from the search bar's "Ask AI" pill.
  void _enterAiMode() {
    // Reset unconditionally (not only on the Books tab): on other tabs the
    // book notifier is already at its default, so this is a no-op there,
    // and unconditional is what makes "at most one notifier ever holds a
    // live query" true by construction. On the Books tab this cancels the
    // pending debounce and drops any in-flight lookup via the generation
    // bump, so no Chaptarr request is issued for a query the user just
    // converted into an AI question.
    ref.read(shellBookSearchProvider.notifier).reset();
    ref.read(shellSearchProvider.notifier).enterAiMode();
    // The pill sits in a TextFieldTapRegion so tapping it doesn't blur the
    // field; re-assert focus anyway so typing can continue immediately.
    _searchFocusNode.requestFocus();
  }

  /// Fixed framing prepended to a Books-tab AI hand-off's wire payload only
  /// — never shown in the chat bubble. This is a compile-time literal with
  /// nothing interpolated into it: the only user-controlled text in the
  /// outgoing message stays the trimmed question appended after it
  /// (threat T-04-01).
  static const String _booksAiHandoffPrefix = 'Context: this question was '
      'asked from the Books tab of Cantinarr, which searches the user\'s '
      'book library. Treat it as a question about books, authors and '
      'reading.\n\n';

  /// Shows the books of an author the library does not hold, by running the
  /// search the user could have typed themselves.
  ///
  /// A metadata-only author has no detail screen to open — Cantinarr's author
  /// page renders *library* titles with ownership pills — so their books, in
  /// this same overlay, are the useful destination: each row is already a
  /// requestable book. Setting the field programmatically does not fire
  /// `onChanged` (see `_exitAiMode`), so the notifier is fed explicitly.
  void _searchAuthorBooks(String authorName) {
    final term = authorName.trim();
    if (term.isEmpty) return;
    _searchController.text = term;
    _searchController.selection =
        TextSelection.collapsed(offset: term.length);
    // Treat it as a fresh keystroke: the Ask AI pill's idle timer restarts
    // rather than firing off the tap that just happened.
    _resetAskAiIdle();
    ref.read(shellBookSearchProvider.notifier).updateSearch(term);
  }

  /// Submit top-bar input through the full-screen assistant route.
  void _submitSearchBarToAi() {
    final text = _searchController.text.trim();
    if (text.isEmpty) return;

    // Captured before `_exitAiMode()`/the route push change `currentPath` —
    // reading it from inside the post-frame callback below would ask "which
    // tab am I on?" of the assistant route and always answer "not Books".
    final wireContent = _isBooksTab(widget.currentPath)
        ? '$_booksAiHandoffPrefix$text'
        : null;

    _exitAiMode();
    context.push('/assistant');
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      ref.read(aiChatProvider).sendMessage(text, wireContent: wireContent);
    });
  }

  void _submitSearchBar() {
    final text = _searchController.text.trim();
    if (text.isEmpty) return;

    final searchState = ref.read(shellSearchProvider);
    if (searchState.searchMode == SearchMode.aiReady) {
      _submitSearchBarToAi();
      return;
    }

    _dismissKeyboard();
  }

  /// Module owning [path], or null for paths outside the module shells.
  static ModuleType? _moduleTypeForPath(String path) {
    if (path.startsWith('/dashboard')) return ModuleType.dashboard;
    if (path.startsWith('/radarr')) return ModuleType.radarr;
    if (path.startsWith('/sonarr')) return ModuleType.sonarr;
    if (path.startsWith('/chaptarr')) return ModuleType.chaptarr;
    if (path.startsWith('/downloads')) return ModuleType.downloads;
    if (path.startsWith('/tautulli')) return ModuleType.tautulli;
    return null;
  }

  /// True on the Books discovery tab, where the toolbar searches Chaptarr
  /// books instead of TMDB. Requires the same route-boundary check
  /// `_isWithinRoute` uses in `app_router.dart` — a bare prefix match would
  /// let a future sibling route sharing the `/dashboard/books` prefix
  /// silently impersonate the Books tab and have its query rerouted to
  /// Chaptarr (WR-01).
  static bool _isBooksTab(String path) =>
      path == '/dashboard/books' || path.startsWith('/dashboard/books/');

  bool _handleScrollNotification(ScrollNotification notification) {
    // Side-scrolling shelves (poster rows, chip strips) bubble their
    // notifications up to this listener too. Only the page's own vertical
    // scroll may drive the shell chrome: otherwise swiping a row sideways
    // hides the search bar, and swiping it back to the start pops it open.
    if (notification.metrics.axis != Axis.vertical) return false;

    final atTop =
        notification.metrics.pixels <= notification.metrics.minScrollExtent + 4;

    // EditableText programmatically scrolls its focused field into view when
    // the keyboard changes the viewport. Those notifications have no drag
    // details; dismissing focus for them makes the keyboard immediately close
    // again on fields near the bottom of long forms.
    final isUserDrag = switch (notification) {
      ScrollStartNotification(:final dragDetails) => dragDetails != null,
      ScrollUpdateNotification(:final dragDetails) => dragDetails != null,
      OverscrollNotification(:final dragDetails) => dragDetails != null,
      _ => false,
    };
    if (isUserDrag) {
      _dismissKeyboard();
    }

    // Pushed routes show the secondary bar, not the search bar. Scrolling one
    // must not decide what the module page underneath looks like once the user
    // pops back to it.
    if (_searchBarPathFor(widget.currentPath) == null) return false;

    if (atTop) {
      _searchBarAnim.forward();
      return false;
    }

    if (notification is ScrollUpdateNotification) {
      final delta = notification.scrollDelta ?? 0;
      if (delta > 2) {
        _searchBarAnim.reverse();
      } else if (delta < -2) {
        _searchBarAnim.forward();
      }
    }
    return false;
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _libraryRefreshDebounce?.cancel();
    _askAiIdleTimer?.cancel();
    _searchBarAnim.dispose();
    _shimmerRotationAnim.dispose();
    _disposeLibraries();
    _searchController.dispose();
    _searchFocusNode.removeListener(_onSearchFocusChanged);
    _searchFocusNode.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    // Keep the search-chip snapshot tracking reality: refresh on library-
    // affecting websocket pings and submitted requests, and rebuild the
    // notifiers when the user's default instances change (they're pinned to
    // an instance id at construction).
    ref.listen(libraryChangedEventsProvider, (_, next) {
      if (next.hasValue) _scheduleLibraryRefresh();
    });
    ref.listen(libraryRefreshTickProvider, (prev, next) {
      if (prev != next) _refreshLibraries(force: true);
    });
    ref.listen(
        authProvider.select((a) => (
              a.valueOrNull?.connection?.defaultRadarrInstance?.id,
              a.valueOrNull?.connection?.defaultSonarrInstance?.id,
            )), (prev, next) {
      if (prev != next) {
        _disposeLibraries();
        _initLibraries();
      }
    });
    // BOOK-07: an instance switch re-runs the currently typed book search
    // against the new Chaptarr instance rather than stranding it against the
    // old one. Tells the *existing* notifier instance to redo its work —
    // does not `ref.watch(instanceProvider...)` inside the provider's own
    // definition, which would tear down and rebuild the notifier and drop
    // the query under stale-looking typed text.
    ref.listen(
      instanceProvider.select((state) => state.activeChaptarrInstance?.id),
      (previous, next) {
        if (previous == next) return;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (!mounted) return;
          ref.read(shellBookSearchProvider.notifier).rerunForInstance();
        });
      },
    );

    final searchState = ref.watch(shellSearchProvider);
    final searchNotifier = ref.read(shellSearchProvider.notifier);
    final bookSearchState = ref.watch(shellBookSearchProvider);
    final bookSearchNotifier = ref.read(shellBookSearchProvider.notifier);
    final hasAi =
        ref.watch(authProvider).valueOrNull?.connection?.services.ai ?? false;
    final hasChaptarrService =
        ref.watch(authProvider).valueOrNull?.connection?.services.chaptarr ??
            false;
    // Admin approval queue depth — drives the hamburger dot (here) and the
    // drawer "Approvals" entry. Always 0 for non-admins.
    final pendingApprovals = ref.watch(pendingApprovalsProvider);
    // Open-issue count — drives the drawer "Issues" entry and contributes to
    // the hamburger dot. Always 0 for non-admins. Watched here (not just in
    // the drawer) so the badge stays live app-wide.
    final openIssues = ref.watch(openIssuesProvider);
    // Agent fixes are already represented by their parent open issue, so the
    // hamburger total deliberately does not add the proposal count again. The
    // drawer watches that count for its own dedicated badge.
    // External profile-change proposals have no parent issue, so unlike agent
    // fixes they DO count toward the hamburger dot — nothing else represents
    // them there.
    final pendingProfileProposals = ref.watch(pendingProfileProposalsProvider);
    // Users waiting for a Plex invite — drives the drawer "Plex invites"
    // entry (only shown while someone waits) and the hamburger dot.
    final plexInvitesWaiting = ref.watch(plexInvitesWaitingProvider);
    final menuBadgeCount = pendingApprovals +
        openIssues +
        pendingProfileProposals +
        plexInvitesWaiting;
    final showSearchResults = searchState.searchMode == SearchMode.search ||
        searchState.searchMode == SearchMode.aiReady;
    final libraryStatus = searchState.isSearching && showSearchResults
        ? buildSearchLibraryStatus(
            searchResults: searchState.searchResults,
            movies: _radarrNotifier?.state.movies ?? const [],
            series: _sonarrNotifier?.state.series ?? const [],
          )
        : const <(MediaType, int), LibraryStatus>{};

    final mobile = AppBreakpoints.isMobile(context);
    final desktop = AppBreakpoints.isDesktop(context);
    final showGlobalSearch = _moduleTypeForPath(widget.currentPath) != null;
    final reduceMotion = MediaQuery.disableAnimationsOf(context);

    // Drive animations from state changes
    _onSearchModeChanged(
      searchState.searchMode,
      reduceMotion: reduceMotion || !showGlobalSearch,
    );

    final isAiReady = searchState.searchMode == SearchMode.aiReady;
    // Single resolution point for the route decision (was five scattered
    // `_isBooksTab(widget.currentPath)` call sites) — every gate below reads
    // this local rather than re-deriving it.
    final booksTab = _isBooksTab(widget.currentPath);
    // The prefix badge's glyph: the sparkle while the bar is actually in AI
    // mode, otherwise the active discovery tab's own icon (read from the
    // same `modulePagesFor` table the drawer already uses — no second icon
    // map). Gated on `ModuleType.dashboard` specifically, not on
    // `showGlobalSearch`: Radarr/Sonarr/Chaptarr/Downloads/Tautulli get no
    // context of their own this phase (SEARCH-06, deferred) and keep the
    // generic search glyph. A dashboard path matching no tab (a bare
    // `/dashboard`, or `/dashboard/books` without the Chaptarr grant) also
    // keeps the generic glyph rather than borrowing another tab's icon —
    // the shell must not assert a context it has not determined.
    IconData contextIcon = Icons.search_rounded;
    if (isAiReady) {
      contextIcon = Icons.auto_awesome_rounded;
    } else if (_moduleTypeForPath(widget.currentPath) == ModuleType.dashboard) {
      final dashboardPages = modulePagesFor(ModuleType.dashboard,
          includeBooks: hasChaptarrService);
      for (final page in dashboardPages) {
        if (page.route == widget.currentPath) {
          contextIcon = page.activeIcon;
          break;
        }
      }
    }
    // Books-aware "a search is active" predicate: on the Books tab this
    // reads the Chaptarr notifier, never the TMDB one, so the overlay and
    // scroll gates cannot be driven by a notifier that no longer receives
    // Books-tab keystrokes.
    final searchOverlayActive =
        booksTab ? bookSearchState.isSearching : searchState.isSearching;

    final searchBar = Padding(
      padding: EdgeInsets.fromLTRB(desktop ? 24 : 6, 12, desktop ? 24 : 12, 10),
      child: AnimatedBuilder(
        animation: _shimmerRotationAnim,
        builder: (context, child) {
          return CustomPaint(
            foregroundPainter: isAiReady
                ? ShimmerBorderPainter(
                    progress: _shimmerRotationAnim.value,
                    borderRadius: AppTheme.radiusLarge,
                    accentColor: AppTheme.signal,
                  )
                : null,
            child: child,
          );
        },
        child: CantinarrSearchBar(
          controller: _searchController,
          focusNode: _searchFocusNode,
          hintText: isAiReady
              ? 'Ask the AI anything...'
              : (booksTab
                  ? 'Search books or authors...'
                  : (hasAi
                      ? 'Search or ask AI...'
                      : 'Search by title or person...')),
          aiEnabled: hasAi,
          contextIcon: contextIcon,
          onSubmitted: _submitSearchBar,
          onSend: isAiReady ? _submitSearchBarToAi : null,
          onChanged: (q) {
            _resetAskAiIdle();
            // Exclusive dispatch: exactly one notifier is ever fed by a
            // keystroke, chosen by the active discovery tab. While AI mode
            // is active on the Books tab, neither notifier is fed a non-empty
            // query — `_manualAiMode` keeps `searchMode` sticky without a
            // feed, the book overlay is suppressed anyway, and
            // `_submitSearchBarToAi` reads `_searchController.text`, not
            // provider state.
            if (booksTab) {
              if (!isAiReady) {
                bookSearchNotifier.updateSearch(q);
              } else if (q.trim().isEmpty) {
                // The one exception, for parity with every other discovery
                // tab: emptying the field by typing leaves AI mode. Only
                // `ShellSearchNotifier.updateSearch('')` clears
                // `_manualAiMode`, so without this the Books tab could be
                // left only via the explicit clear button. The empty string
                // cannot reopen the escalation path exclusive dispatch
                // closed: `updateSearch` short-circuits on empty — it resets
                // `searchMode` to `search` and returns before
                // `_executeSearch`, so neither `isAiPromptQuery` nor the
                // empty-results auto-escalation is ever consulted.
                searchNotifier.updateSearch('');
              }
            } else {
              searchNotifier.updateSearch(q);
            }
          },
          onClear: isAiReady
              ? _exitAiMode
              : () {
                  // Clear means "nothing is being searched anywhere" — both
                  // notifiers are reset in both directions so a stale query
                  // can never linger in the one that wasn't being fed.
                  if (booksTab) {
                    ref.read(shellBookSearchProvider.notifier).reset();
                    searchNotifier.updateSearch('');
                  } else {
                    searchNotifier.updateSearch('');
                    ref.read(shellBookSearchProvider.notifier).reset();
                  }
                },
        ),
      ),
    );

    // Top bar: hamburger + search on non-desktop; on desktop just the search
    // bar, capped to a readable width (the results overlay centers to match).
    Widget topBar;
    if (desktop) {
      topBar = Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(
            maxWidth: 880,
          ),
          child: searchBar,
        ),
      );
    } else {
      topBar = Row(
        children: [
          Padding(
            padding: const EdgeInsets.only(left: 10, top: 12, bottom: 10),
            child: DecoratedBox(
              decoration: BoxDecoration(
                color: AppTheme.surfaceVariant,
                borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
                border: Border.all(color: AppTheme.border),
                boxShadow: [
                  BoxShadow(
                    color: Colors.black.withValues(alpha: 0.2),
                    blurRadius: 12,
                    offset: const Offset(0, 6),
                  ),
                ],
              ),
              child: IconButton(
                icon: Badge(
                  isLabelVisible: menuBadgeCount > 0,
                  backgroundColor: AppTheme.accent,
                  smallSize: 9,
                  child: const Icon(
                    Icons.menu,
                    color: AppTheme.textPrimary,
                  ),
                ),
                tooltip: pendingApprovals > 0
                    ? '$pendingApprovals approval${pendingApprovals == 1 ? '' : 's'} waiting'
                    : 'Open navigation',
                onPressed: () {
                  _dismissKeyboard();
                  _scaffoldKey.currentState?.openDrawer();
                },
              ),
            ),
          ),
          Expanded(child: searchBar),
        ],
      );
    }

    final secondaryTopBar = Container(
      margin: const EdgeInsets.fromLTRB(10, 10, 10, 5),
      height: 48,
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant.withValues(alpha: 0.9),
        borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
        border: Border.all(color: AppTheme.border),
      ),
      child: Row(
        children: [
          IconButton(
            icon: Badge(
              isLabelVisible: menuBadgeCount > 0,
              backgroundColor: AppTheme.accent,
              smallSize: 9,
              child: const Icon(Icons.menu),
            ),
            tooltip: 'Open navigation',
            onPressed: () {
              _dismissKeyboard();
              _scaffoldKey.currentState?.openDrawer();
            },
          ),
          Container(width: 1, height: 20, color: AppTheme.border),
          const SizedBox(width: 12),
          Expanded(
            child: Text(
              _secondaryRouteLabel(widget.currentPath).toUpperCase(),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 10.5,
                fontWeight: FontWeight.w800,
                letterSpacing: 1.2,
              ),
            ),
          ),
          const Padding(
            padding: EdgeInsets.only(right: 15),
            child: Icon(
              Icons.blur_on_rounded,
              size: 16,
              color: AppTheme.signal,
            ),
          ),
        ],
      ),
    );

    final scaffold = Scaffold(
      key: _scaffoldKey,
      body: SafeArea(
        bottom: false,
        child: Stack(
          children: [
            // Base layer: search bar + module content
            Column(
              children: [
                // Search bar at top (hidden during AI mode). Navigating
                // between module and pushed routes swaps this slot (search
                // bar <-> secondary label bar, or nothing on desktop); the
                // swap cross-fades and height-morphs so the chrome glides
                // with the page transition below instead of snapping.
                AnimatedSize(
                  duration: const Duration(milliseconds: 220),
                  curve: Curves.easeOutCubic,
                  alignment: Alignment.topCenter,
                  child: AnimatedSwitcher(
                    duration: const Duration(milliseconds: 220),
                    layoutBuilder: (currentChild, previousChildren) => Stack(
                      alignment: Alignment.topCenter,
                      children: [
                        ...previousChildren,
                        if (currentChild != null) currentChild,
                      ],
                    ),
                    child: showGlobalSearch
                        ? KeyedSubtree(
                            key: const ValueKey('module-top-bar'),
                            child: mobile
                                ? SizeTransition(
                                    sizeFactor: _searchBarCurve,
                                    axisAlignment: -1,
                                    child: topBar,
                                  )
                                : topBar,
                          )
                        : !desktop
                            ? KeyedSubtree(
                                key: const ValueKey('secondary-top-bar'),
                                child: secondaryTopBar,
                              )
                            : const SizedBox.shrink(
                                key: ValueKey('no-top-bar'),
                              ),
                  ),
                ),
                // Module content (includes its own bottom nav)
                Expanded(
                  child: Stack(
                    children: [
                      NotificationListener<ScrollNotification>(
                        onNotification:
                            mobile && !searchOverlayActive && !isAiReady
                                ? _handleScrollNotification
                                : null,
                        child: widget.child,
                      ),
                      if (isAiReady)
                        Positioned.fill(
                          child: Container(
                            color: AppTheme.background.withValues(alpha: 0.96),
                            child: Column(
                              children: [
                                Padding(
                                  padding:
                                      const EdgeInsets.fromLTRB(16, 20, 16, 12),
                                  child: Column(
                                    children: [
                                      Icon(
                                        Icons.auto_awesome,
                                        size: 32,
                                        color: AppTheme.accent
                                            .withValues(alpha: 0.5),
                                      ),
                                      const SizedBox(height: 8),
                                      Text(
                                        _searchController.text.trim().isNotEmpty
                                            ? 'Press send to ask AI'
                                            : 'Type anything, then press send',
                                        style: const TextStyle(
                                          color: AppTheme.textSecondary,
                                          fontSize: 14,
                                        ),
                                      ),
                                    ],
                                  ),
                                ),
                                if (searchState.searchResults.isNotEmpty ||
                                    searchState.isLoadingSearch)
                                  Expanded(
                                    child: SearchResultsView(
                                      results: searchState.searchResults,
                                      isLoading: searchState.isLoadingSearch,
                                      query: searchState.searchQuery,
                                      onLoadMore: searchNotifier.loadMoreSearch,
                                      libraryStatus: libraryStatus,
                                      onResultTap: _dismissKeyboard,
                                    ),
                                  )
                                else
                                  const Spacer(),
                              ],
                            ),
                          ),
                        ),
                      // Search lives in the same measured content region as
                      // the module, so it always begins below the actual top
                      // bar height (including text scaling). On the Books tab
                      // this slot renders Chaptarr book results instead of
                      // TMDB results — one overlay, chosen by route. The
                      // gate itself is books-aware (`searchOverlayActive`)
                      // rather than reading the TMDB notifier's `searchMode`,
                      // so a completed Chaptarr search can never be
                      // suppressed by an AI-mode flip the Books tab no
                      // longer feeds. `!isAiReady` is equivalent to the old
                      // `searchMode == SearchMode.search` comparison
                      // (exactly two members), so Movies/TV/Releases
                      // behavior is bit-identical.
                      if (showGlobalSearch && !isAiReady && searchOverlayActive)
                        Positioned.fill(
                          child: ColoredBox(
                            color: AppTheme.background.withValues(alpha: 0.97),
                            child: booksTab
                                ? BookSearchResultsView(
                                    results: bookSearchState.results,
                                    authors: bookSearchState.authors,
                                    query: bookSearchState.searchQuery,
                                    isLoading: bookSearchState.isLoadingSearch,
                                    searched: bookSearchState.searched,
                                    error: bookSearchState.error,
                                    authorsUnavailable:
                                        bookSearchState.authorsUnavailable,
                                    onResultTap: _dismissKeyboard,
                                    onAuthorDrillDown: _searchAuthorBooks,
                                  )
                                : SearchResultsView(
                                    results: searchState.searchResults,
                                    isLoading: searchState.isLoadingSearch,
                                    query: searchState.searchQuery,
                                    onLoadMore: searchNotifier.loadMoreSearch,
                                    libraryStatus: libraryStatus,
                                    onResultTap: _dismissKeyboard,
                                  ),
                          ),
                        ),
                      // Floating "Ask AI" pill: the explicit door into AI
                      // mode. It appears only after the user types and then
                      // pauses — focus alone never surfaces it, and further
                      // typing hides it — so it reads as a suggestion on
                      // hesitation, not chrome racing the keyboard. Typing
                      // something question-shaped remains the implicit path;
                      // the pill is for prompts the heuristic would read as
                      // a title.
                      if (showGlobalSearch && hasAi && !isAiReady)
                        Positioned(
                          top: 6,
                          left: 0,
                          right: 0,
                          child: Center(
                            child: ConstrainedBox(
                              constraints: BoxConstraints(
                                maxWidth: desktop ? 880 : double.infinity,
                              ),
                              child: Align(
                                alignment: Alignment.centerRight,
                                child: Padding(
                                  padding: EdgeInsets.only(
                                    right: desktop ? 24 : 12,
                                  ),
                                  child: ListenableBuilder(
                                    // The controller matters too: the clear
                                    // X empties the text without firing
                                    // onChanged, and the pill must drop the
                                    // same instant.
                                    listenable: Listenable.merge([
                                      _searchFocusNode,
                                      _searchController,
                                    ]),
                                    builder: (context, child) {
                                      final visible =
                                          _searchFocusNode.hasFocus &&
                                              _searchIdle &&
                                              _searchController.text.trim().isNotEmpty;
                                      final duration = reduceMotion
                                          ? Duration.zero
                                          : AppTheme.motionFast;
                                      return IgnorePointer(
                                        ignoring: !visible,
                                        child: AnimatedSlide(
                                          offset: visible
                                              ? Offset.zero
                                              : const Offset(0, -0.4),
                                          duration: duration,
                                          curve: Curves.easeOutCubic,
                                          child: AnimatedOpacity(
                                            opacity: visible ? 1 : 0,
                                            duration: duration,
                                            child: child,
                                          ),
                                        ),
                                      );
                                    },
                                    child: TextFieldTapRegion(
                                      child:
                                          _AskAiPill(onTap: _enterAiMode),
                                    ),
                                  ),
                                ),
                              ),
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
              ],
            ),

            // Quiet depth cue above module navigation.
            if (showGlobalSearch)
              Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                height: 32,
                child: IgnorePointer(
                  child: DecoratedBox(
                    decoration: BoxDecoration(
                      gradient: LinearGradient(
                        begin: Alignment.topCenter,
                        end: Alignment.bottomCenter,
                        colors: [
                          AppTheme.background.withValues(alpha: 0),
                          AppTheme.signal.withValues(alpha: 0.035),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
          ],
        ),
      ),
      drawer: desktop ? null : _buildDrawer(context),
    );

    if (desktop) {
      return Row(
        children: [
          Container(
            width: AppBreakpoints.sidebarWidth,
            decoration: const BoxDecoration(
              gradient: LinearGradient(
                begin: Alignment.topLeft,
                end: Alignment.bottomRight,
                colors: [AppTheme.surfaceRaised, AppTheme.surface],
              ),
              border: Border(
                right: BorderSide(color: AppTheme.border),
              ),
            ),
            child: Material(
              color: Colors.transparent,
              child: _buildDrawerContent(context, isOverlay: false),
            ),
          ),
          Expanded(child: scaffold),
        ],
      );
    }

    return scaffold;
  }

  Widget _buildDrawer(BuildContext context) {
    return Drawer(
      backgroundColor: AppTheme.surface,
      child: _buildDrawerContent(context, isOverlay: true),
    );
  }

  static String _secondaryRouteLabel(String path) {
    if (path.startsWith('/detail/')) return 'Media details';
    if (path.startsWith('/settings')) return 'Settings';
    if (path.startsWith('/approvals')) return 'Approvals';
    if (path.startsWith('/issues')) return 'Issues';
    if (path.startsWith('/agent-')) return 'Agent workspace';
    if (path.startsWith('/assistant')) return 'AI assistant';
    if (path.startsWith('/setup')) return 'Setup';
    if (path.startsWith('/plex-guide')) return 'Watch on Plex';
    if (path.startsWith('/media-servers')) return 'Media server access';
    return 'Cantinarr';
  }

  Widget _buildDrawerContent(BuildContext context, {required bool isOverlay}) {
    final moduleState = ref.watch(moduleProvider);
    final instanceState = ref.watch(instanceProvider);
    final isAdmin = ref.watch(authProvider).valueOrNull?.user?.isAdmin ?? false;
    // Highlight the module that owns the current route; fall back to the
    // last drawer selection for locations outside the module shells.
    final pathModule = _moduleTypeForPath(widget.currentPath);
    final hasChaptarrService =
        ref.watch(authProvider).valueOrNull?.connection?.services.chaptarr ??
            false;
    // The backend lists a media server only for users an admin granted it,
    // so its presence alone decides whether the access guide is offered.
    final mediaServerInstances = ref
            .watch(authProvider)
            .valueOrNull
            ?.connection
            ?.mediaServerInstances ??
        const <ServiceInstance>[];
    final pendingApprovals = ref.watch(pendingApprovalsProvider);
    final approvalsStale = ref.watch(pendingApprovalsStaleProvider);
    final openIssues = ref.watch(openIssuesProvider);
    final activeIssues = ref.watch(activeIssuesProvider);
    final issuesStale = ref.watch(issueQueueCountsStaleProvider);
    final pendingAgentActions = ref.watch(pendingAgentActionsProvider);
    final agentActionsStale = ref.watch(pendingAgentActionsStaleProvider);
    final plexInvitesWaiting = ref.watch(plexInvitesWaitingProvider);
    // Setup reminder: unconfigured-feature count, shown while the admin
    // hasn't muted it from the checklist screen.
    final setupRemaining = ref.watch(setupStatusProvider)?.remaining ?? 0;
    final showSetupReminder =
        setupRemaining > 0 && ref.watch(setupReminderEnabledProvider);
    // Conditional entries assume an in-flight first load will land empty and
    // stay hidden — flashing every entry on cold start taught nothing. They
    // fail open only when a queue is genuinely unknowable (refresh failed).
    final showApprovals = !ref.watch(approvalsMenuOnlyWhenPendingProvider) ||
        approvalsStale ||
        pendingApprovals > 0;
    final showIssues = !ref.watch(issuesMenuOnlyWhenActiveProvider) ||
        issuesStale ||
        activeIssues > 0;
    final showAgentFixes =
        !ref.watch(agentFixesMenuOnlyWhenAwaitingReviewProvider) ||
            agentActionsStale ||
            pendingAgentActions > 0;
    final pendingProfileProposals = ref.watch(pendingProfileProposalsProvider);
    final profileProposalsStale =
        ref.watch(pendingProfileProposalsStaleProvider);
    final showProfileApprovals =
        !ref.watch(profileApprovalsMenuOnlyWhenPendingProvider) ||
            profileProposalsStale ||
            pendingProfileProposals > 0;
    final showNeedsAttentionSection = showApprovals ||
        showIssues ||
        showAgentFixes ||
        showProfileApprovals ||
        plexInvitesWaiting > 0 ||
        showSetupReminder;

    // AI Assistant is a tool, not a library, so it sits with the footer actions
    // instead of under the "Libraries" header. It's always last in
    // moduleState.modules, so pulling it out leaves the remaining indices (used
    // by the active-highlight fallback below) unchanged.
    AppModule? assistantModule;
    final libraryModules = <AppModule>[];
    for (final m in moduleState.modules) {
      if (m.type == ModuleType.assistant) {
        assistantModule = m;
      } else {
        libraryModules.add(m);
      }
    }

    // Builds one module row plus its desktop sub-pages when active.
    Widget buildModuleTile(AppModule module) {
      final isActive = pathModule != null && module.type == pathModule;
      final selectorInstances = isAdmin
          ? _instancesForModule(instanceState, module.type)
          : const <ServiceInstance>[];
      final activeInstance =
          _activeInstanceForModule(instanceState, module.type);
      // Downloads offers the aggregate "All" view above its clients; the raw
      // stored id is passed through so the sentinel can be marked active.
      final isDownloads = module.type == ModuleType.downloads;
      final activeInstanceId = isDownloads
          ? instanceState.activeDownloadInstanceId
          : activeInstance?.id;

      final item = _DrawerItem(
        icon: module.icon,
        title: module.label,
        semanticsIdentifier: 'nav-module-${module.type.name}',
        selected: isActive,
        trailing: selectorInstances.length > 1
            ? _InstanceSelector(
                appName: module.label,
                instances: selectorInstances,
                activeInstanceId: activeInstanceId,
                aggregateOption: isDownloads
                    ? (id: allDownloadInstancesId, label: 'All')
                    : null,
                onSelected: (instanceId) {
                  if (isOverlay) Navigator.pop(context);
                  _navigateToModule(
                    context,
                    module,
                    instanceId: instanceId,
                  );
                  if (module.type != ModuleType.assistant) {
                    ref
                        .read(moduleProvider.notifier)
                        .setActiveModule(module.type);
                  }
                },
              )
            : null,
        onTap: () {
          if (isOverlay) Navigator.pop(context);
          _navigateToModule(
            context,
            module,
            instanceId: _defaultInstanceIdForModule(
              instanceState,
              module.type,
            ),
          );
          if (module.type != ModuleType.assistant) {
            ref.read(moduleProvider.notifier).setActiveModule(
                  module.type,
                );
          }
        },
      );

      final pages = !isOverlay && isActive
          ? modulePagesFor(module.type, includeBooks: hasChaptarrService)
          : const <ModulePage>[];
      if (pages.isEmpty) return item;

      return Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          item,
          for (final page in pages)
            _DrawerSubItem(
              page: page,
              selected: widget.currentPath == page.route,
              onTap: () => context.go(page.route),
            ),
        ],
      );
    }

    return SafeArea(
      child: Column(
        children: [
          // Header
          Container(
            width: double.infinity,
            padding: const EdgeInsets.fromLTRB(18, 20, 18, 18),
            child: Row(
              children: [
                Container(
                  width: 50,
                  height: 50,
                  padding: const EdgeInsets.all(3),
                  decoration: BoxDecoration(
                    gradient: const LinearGradient(
                      begin: Alignment.topLeft,
                      end: Alignment.bottomRight,
                      colors: [AppTheme.accent, AppTheme.signal],
                    ),
                    borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
                    boxShadow: [
                      BoxShadow(
                        color: AppTheme.accent.withValues(alpha: 0.16),
                        blurRadius: 18,
                      ),
                    ],
                  ),
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(13),
                    child: Image.asset(
                      'assets/logo.png',
                      fit: BoxFit.cover,
                    ),
                  ),
                ),
                const SizedBox(width: 13),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        'CANTINARR',
                        style: Theme.of(context).textTheme.titleLarge?.copyWith(
                              color: AppTheme.textPrimary,
                              fontWeight: FontWeight.w800,
                              letterSpacing: 1.25,
                            ),
                      ),
                      const SizedBox(height: 3),
                      const Text(
                        'How you doing, you old pirate?',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          color: AppTheme.textMuted,
                          fontSize: 11,
                          fontWeight: FontWeight.w500,
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
          const Divider(color: AppTheme.border),

          // Admin action queues — kept above the modules so a waiting count is
          // the first thing an admin sees when the drawer opens.
          if (isAdmin && showNeedsAttentionSection) ...[
            const _DrawerSectionHeader('Needs attention'),
            if (showApprovals)
              _DrawerItem(
                icon: Icons.fact_check_outlined,
                title: 'Approvals',
                semanticsIdentifier: 'nav-action-approvals',
                badgeCount: pendingApprovals,
                onTap: () {
                  if (isOverlay) Navigator.pop(context);
                  context.push('/approvals');
                },
              ),
            if (showIssues)
              _DrawerItem(
                icon: Icons.flag_outlined,
                title: 'Issues',
                semanticsIdentifier: 'nav-action-issues',
                badgeCount: openIssues,
                onTap: () {
                  if (isOverlay) Navigator.pop(context);
                  context.push('/issues');
                },
              ),
            if (showAgentFixes)
              _DrawerItem(
                icon: Icons.build_circle_outlined,
                title: 'Agent fixes',
                semanticsIdentifier: 'nav-action-agent-fixes',
                badgeCount: pendingAgentActions,
                onTap: () {
                  if (isOverlay) Navigator.pop(context);
                  context.push('/agent-actions');
                },
              ),
            if (showProfileApprovals)
              _DrawerItem(
                icon: Icons.tune,
                title: 'Profile approvals',
                semanticsIdentifier: 'nav-action-profile-approvals',
                badgeCount: pendingProfileProposals,
                onTap: () {
                  if (isOverlay) Navigator.pop(context);
                  context.push('/settings/profile-approvals');
                },
              ),
            // Appears only while someone is waiting on a Plex invite (e.g.
            // the push was missed or an auto-invite failed); lands on the
            // Users screen where the invite is one tap.
            if (plexInvitesWaiting > 0)
              _DrawerItem(
                icon: Icons.play_circle_outline,
                title: 'Plex invites',
                semanticsIdentifier: 'nav-action-plex-invites',
                badgeCount: plexInvitesWaiting,
                onTap: () {
                  if (isOverlay) Navigator.pop(context);
                  context.push('/settings/users');
                },
              ),
            // Setup reminder: how many features are still unconfigured.
            // Muteable from the checklist; the Settings tile always remains.
            if (showSetupReminder)
              _DrawerItem(
                icon: Icons.checklist_outlined,
                title: 'Setup checklist',
                semanticsIdentifier: 'nav-action-setup-checklist',
                badgeCount: setupRemaining,
                onTap: () {
                  if (isOverlay) Navigator.pop(context);
                  context.push('/setup');
                },
              ),
            const Divider(color: AppTheme.border),
          ],

          // Module navigation. Discover (the browse/home surface) leads on its
          // own; the "Libraries" header groups the managed arr modules beneath
          // it. On desktop the active module also expands into its pages — those
          // replace the module shell's bottom nav there. The mobile drawer stays
          // modules-only because the bottom nav covers page switching.
          Expanded(
            child: ListView(
              padding: const EdgeInsets.fromLTRB(10, 8, 10, 12),
              children: [
                if (libraryModules.isNotEmpty)
                  buildModuleTile(libraryModules.first),
                if (libraryModules.length > 1) ...[
                  const _DrawerSectionHeader('Libraries'),
                  for (int i = 1; i < libraryModules.length; i++)
                    buildModuleTile(libraryModules[i]),
                ],
              ],
            ),
          ),

          const Divider(color: AppTheme.border),

          if (assistantModule != null)
            _DrawerItem(
              icon: assistantModule.icon,
              title: assistantModule.label,
              semanticsIdentifier: 'nav-action-ai-assistant',
              onTap: () {
                if (isOverlay) Navigator.pop(context);
                context.push('/assistant');
              },
            ),
          if (ref.watch(plexGuideEnabledProvider))
            _DrawerItem(
              icon: Icons.play_circle_outline,
              title: 'Watch on Plex',
              semanticsIdentifier: 'nav-action-watch-on-plex',
              onTap: () {
                if (isOverlay) Navigator.pop(context);
                context.push('/plex-guide');
              },
            ),
          if (mediaServerInstances.isNotEmpty)
            _DrawerItem(
              icon: Icons.live_tv_outlined,
              title: mediaServerGuideTitle(
                mediaServerInstances.map((i) => i.serviceType),
              ),
              semanticsIdentifier: 'nav-action-media-servers',
              onTap: () {
                if (isOverlay) Navigator.pop(context);
                context.push('/media-servers');
              },
            ),
          _DrawerItem(
            icon: Icons.settings,
            title: 'Settings',
            semanticsIdentifier: 'nav-action-settings',
            onTap: () {
              if (isOverlay) Navigator.pop(context);
              context.push('/settings');
            },
          ),
          const SizedBox(height: 8),
        ],
      ),
    );
  }

  List<ServiceInstance> _instancesForModule(
    InstanceState state,
    ModuleType type,
  ) {
    switch (type) {
      case ModuleType.radarr:
        return state.radarrInstances;
      case ModuleType.sonarr:
        return state.sonarrInstances;
      case ModuleType.downloads:
        return state.downloadInstances;
      case ModuleType.tautulli:
        return state.tautulliInstances;
      case ModuleType.chaptarr:
        return state.chaptarrInstances;
      default:
        return const [];
    }
  }

  ServiceInstance? _activeInstanceForModule(
    InstanceState state,
    ModuleType type,
  ) {
    switch (type) {
      case ModuleType.radarr:
        return state.activeRadarrInstance;
      case ModuleType.sonarr:
        return state.activeSonarrInstance;
      case ModuleType.downloads:
        return state.activeDownloadInstance;
      case ModuleType.tautulli:
        return state.activeTautulliInstance;
      case ModuleType.chaptarr:
        return state.activeChaptarrInstance;
      default:
        return null;
    }
  }

  /// The selection a plain module-row tap resets to. For downloads with
  /// several clients that is the aggregate "All" view, matching its default.
  String? _defaultInstanceIdForModule(
    InstanceState state,
    ModuleType type,
  ) {
    final instances = _instancesForModule(state, type);
    if (instances.isEmpty) return null;
    if (type == ModuleType.downloads && instances.length > 1) {
      return allDownloadInstancesId;
    }
    return instances
        .firstWhere(
          (instance) => instance.isDefault,
          orElse: () => instances.first,
        )
        .id;
  }

  void _navigateToModule(
    BuildContext context,
    AppModule module, {
    String? instanceId,
  }) {
    if (instanceId != null) {
      final instances = ref.read(instanceProvider.notifier);
      switch (module.type) {
        case ModuleType.radarr:
          instances.setActiveRadarrInstance(instanceId);
        case ModuleType.sonarr:
          instances.setActiveSonarrInstance(instanceId);
        case ModuleType.downloads:
          instances.setActiveDownloadInstance(instanceId);
        case ModuleType.tautulli:
          instances.setActiveTautulliInstance(instanceId);
        case ModuleType.chaptarr:
          instances.setActiveChaptarrInstance(instanceId);
        default:
          break;
      }
    }
    switch (module.type) {
      case ModuleType.dashboard:
        context.go('/dashboard/movies');
      case ModuleType.radarr:
        context.go('/radarr/library');
      case ModuleType.sonarr:
        context.go('/sonarr/library');
      case ModuleType.chaptarr:
        context.go('/chaptarr/library');
      case ModuleType.downloads:
        context.go('/downloads/queue');
      case ModuleType.tautulli:
        context.go('/tautulli/activity');
      case ModuleType.assistant:
        context.push('/assistant');
    }
  }
}

class _DrawerItem extends StatelessWidget {
  final IconData icon;
  final String title;
  final String semanticsIdentifier;
  final bool selected;
  final VoidCallback onTap;
  final Widget? trailing;

  /// When > 0, renders a trailing count pill (e.g. the pending-approvals count).
  final int badgeCount;

  const _DrawerItem({
    required this.icon,
    required this.title,
    required this.semanticsIdentifier,
    this.selected = false,
    required this.onTap,
    this.trailing,
    this.badgeCount = 0,
  });

  @override
  Widget build(BuildContext context) {
    final trailingWidget =
        badgeCount > 0 ? _CountPill(count: badgeCount) : trailing;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: AnimatedContainer(
        duration: AppTheme.motionMedium,
        decoration: BoxDecoration(
          gradient: selected
              ? LinearGradient(
                  colors: [
                    AppTheme.accent.withValues(alpha: 0.16),
                    AppTheme.signal.withValues(alpha: 0.045),
                  ],
                )
              : null,
          borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
          border: Border.all(
            color: selected
                ? AppTheme.accent.withValues(alpha: 0.28)
                : Colors.transparent,
          ),
        ),
        child: Semantics(
          identifier: semanticsIdentifier,
          label: e2eWebSemanticsEnabled ? 'Navigate to $title' : null,
          button: e2eWebSemanticsEnabled,
          selected: e2eWebSemanticsEnabled ? selected : null,
          excludeSemantics: e2eWebSemanticsEnabled,
          onTap: e2eWebSemanticsEnabled ? onTap : null,
          // Ink (ripple/hover) must find a Material *inside* the scrolling
          // list; otherwise it paints on the sidebar-level Material and
          // escapes the list's clip, bleeding over the fixed rows above.
          child: Material(
            type: MaterialType.transparency,
            child: ListTile(
              leading: AnimatedContainer(
                duration: AppTheme.motionFast,
                width: 34,
                height: 34,
                decoration: BoxDecoration(
                  color: selected
                      ? AppTheme.accent.withValues(alpha: 0.14)
                      : AppTheme.surfaceVariant.withValues(alpha: 0.7),
                  borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
                ),
                child: Icon(
                  icon,
                  size: 20,
                  color: selected ? AppTheme.accent : AppTheme.textSecondary,
                ),
              ),
              // The trailing widget rides inside the title row rather than in
              // ListTile's own trailing slot. ListTile measures trailing first
              // and hands the title whatever is left, so a 128px instance chip
              // left "Chaptarr" 31px of the sidebar's 167px title slot and it
              // wrapped mid-word to "Chapta / rr". Splitting the slot by flex
              // instead guarantees the module name enough room for the longest
              // label that can carry a chip, and makes the user-supplied
              // instance name the side that ellipsizes. Both children are
              // flexible, so no width can overflow this row.
              title: Row(
                children: [
                  Flexible(
                    flex: 3,
                    child: Text(
                      title,
                      maxLines: 1,
                      softWrap: false,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: selected
                            ? AppTheme.textPrimary
                            : AppTheme.textSecondary,
                        fontWeight:
                            selected ? FontWeight.w700 : FontWeight.w500,
                        letterSpacing: selected ? 0.05 : 0,
                      ),
                    ),
                  ),
                  if (trailingWidget != null) ...[
                    const SizedBox(width: 8),
                    Expanded(
                      flex: 2,
                      child: Align(
                        alignment: Alignment.centerRight,
                        child: trailingWidget,
                      ),
                    ),
                  ],
                ],
              ),
              selected: selected,
              selectedTileColor: Colors.transparent,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
              ),
              onTap: onTap,
            ),
          ),
        ),
      ),
    );
  }
}

/// A small caps label that segments the drawer into scannable groups
/// (e.g. "Needs attention", "Libraries"). Purely visual — not tappable.
class _DrawerSectionHeader extends StatelessWidget {
  final String label;

  const _DrawerSectionHeader(this.label);

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(10, 18, 10, 7),
      child: Text(
        label.toUpperCase(),
        style: const TextStyle(
          color: AppTheme.textMuted,
          fontSize: 10,
          fontWeight: FontWeight.w700,
          letterSpacing: 1.25,
        ),
      ),
    );
  }
}

/// A page entry nested under the active module in the desktop sidebar —
/// the desktop counterpart of one bottom-nav tab.
class _DrawerSubItem extends StatelessWidget {
  final ModulePage page;
  final bool selected;
  final VoidCallback onTap;

  const _DrawerSubItem({
    required this.page,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(left: 20, top: 1, bottom: 1),
      child: Semantics(
        identifier: 'nav-page-${page.route.substring(1).replaceAll('/', '-')}',
        label: e2eWebSemanticsEnabled ? 'Navigate to ${page.route}' : null,
        button: e2eWebSemanticsEnabled,
        selected: e2eWebSemanticsEnabled ? selected : null,
        excludeSemantics: e2eWebSemanticsEnabled,
        onTap: e2eWebSemanticsEnabled ? onTap : null,
        // The selected pill (selectedTileColor) is painted as ink on the
        // nearest Material. That Material must live *inside* the scrolling
        // list; otherwise the pill paints at the sidebar level, escaping the
        // list's clip and bleeding across the fixed rows while scrolling.
        child: Material(
          type: MaterialType.transparency,
          child: ListTile(
            dense: true,
            contentPadding: const EdgeInsets.only(left: 13, right: 12),
            minLeadingWidth: 0,
            horizontalTitleGap: 11,
            leading: Icon(
              selected ? page.activeIcon : page.icon,
              size: 18,
              color: selected ? AppTheme.signal : AppTheme.textMuted,
            ),
            title: Text(
              page.label,
              style: TextStyle(
                fontSize: 13,
                color: selected ? AppTheme.textPrimary : AppTheme.textSecondary,
                fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
              ),
            ),
            selected: selected,
            selectedTileColor: AppTheme.signal.withValues(alpha: 0.075),
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
            ),
            onTap: onTap,
          ),
        ),
      ),
    );
  }
}

class _InstanceSelector extends StatelessWidget {
  final String appName;
  final List<ServiceInstance> instances;
  final String? activeInstanceId;
  final ValueChanged<String> onSelected;

  /// Optional aggregate entry (the downloads "All" view) listed above the
  /// instances; selecting it reports its id through [onSelected].
  final ({String id, String label})? aggregateOption;

  const _InstanceSelector({
    required this.appName,
    required this.instances,
    required this.activeInstanceId,
    required this.onSelected,
    this.aggregateOption,
  });

  @override
  Widget build(BuildContext context) {
    final aggregate = aggregateOption;
    final aggregateActive =
        aggregate != null && activeInstanceId == aggregate.id;
    final activeInstance = instances.firstWhere(
      (instance) => instance.id == activeInstanceId,
      orElse: () => instances.firstWhere(
        (instance) => instance.isDefault,
        orElse: () => instances.first,
      ),
    );
    final activeName = aggregateActive ? aggregate.label : activeInstance.name;

    return PopupMenuButton<String>(
      tooltip: 'Choose $appName instance',
      color: AppTheme.surface,
      onSelected: onSelected,
      itemBuilder: (context) => [
        if (aggregate != null)
          PopupMenuItem<String>(
            value: aggregate.id,
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    aggregate.label,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                if (aggregateActive)
                  const Icon(
                    Icons.check,
                    size: 18,
                    color: AppTheme.accent,
                  ),
              ],
            ),
          ),
        for (final instance in instances)
          PopupMenuItem<String>(
            value: instance.id,
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    instance.name,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                if (!aggregateActive && instance.id == activeInstance.id)
                  const Icon(
                    Icons.check,
                    size: 18,
                    color: AppTheme.accent,
                  ),
              ],
            ),
          ),
      ],
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 128),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
          decoration: BoxDecoration(
            color: AppTheme.surfaceVariant.withValues(alpha: 0.8),
            border: Border.all(color: AppTheme.borderStrong),
            borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Flexible(
                child: Text(
                  activeName,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              const SizedBox(width: 4),
              const Icon(
                Icons.arrow_drop_down,
                color: AppTheme.textSecondary,
                size: 18,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// A small filled pill showing a count (capped at 99+), used for the drawer
/// approvals badge.
class _CountPill extends StatelessWidget {
  final int count;

  const _CountPill({required this.count});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: AppTheme.accent,
        borderRadius: BorderRadius.circular(AppTheme.radiusPill),
        boxShadow: [
          BoxShadow(
            color: AppTheme.accent.withValues(alpha: 0.2),
            blurRadius: 10,
          ),
        ],
      ),
      child: Text(
        count > 99 ? '99+' : '$count',
        style: const TextStyle(
          color: AppTheme.onAccent,
          fontSize: 12,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
  }
}

/// Floating capsule under the focused search bar offering the explicit
/// switch into AI mode (typing something question-shaped is the implicit
/// one).
class _AskAiPill extends StatelessWidget {
  final VoidCallback onTap;

  const _AskAiPill({required this.onTap});

  @override
  Widget build(BuildContext context) {
    return Semantics(
      identifier: 'search-ask-ai',
      button: true,
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(AppTheme.radiusPill),
          child: Ink(
            decoration: BoxDecoration(
              color: AppTheme.surfaceRaised,
              borderRadius: BorderRadius.circular(AppTheme.radiusPill),
              border: Border.all(
                color: AppTheme.signal.withValues(alpha: 0.45),
              ),
              boxShadow: [
                BoxShadow(
                  color: Colors.black.withValues(alpha: 0.35),
                  blurRadius: 12,
                  offset: const Offset(0, 4),
                ),
                BoxShadow(
                  color: AppTheme.signal.withValues(alpha: 0.18),
                  blurRadius: 14,
                ),
              ],
            ),
            child: const Padding(
              padding: EdgeInsets.symmetric(horizontal: 12, vertical: 7),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(
                    Icons.auto_awesome_rounded,
                    size: 14,
                    color: AppTheme.signal,
                  ),
                  SizedBox(width: 6),
                  Text(
                    'Ask AI',
                    style: TextStyle(
                      color: AppTheme.signal,
                      fontSize: 11.5,
                      fontWeight: FontWeight.w700,
                      letterSpacing: 0.4,
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
