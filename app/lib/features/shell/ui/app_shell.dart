import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
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
import '../../discover/ui/search_results_view.dart';
import '../../issues/logic/issues_provider.dart';
import '../../radarr/data/radarr_api_service.dart';
import '../../radarr/logic/radarr_movies_provider.dart';
import '../../request/logic/pending_approvals_provider.dart';
import '../../settings/logic/plex_invites_provider.dart';
import '../../settings/logic/setup_status_provider.dart';
import '../../sonarr/data/sonarr_api_service.dart';
import '../../sonarr/logic/sonarr_series_provider.dart';
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

  // Shimmer sweep rotation for aiReady state
  late final AnimationController _shimmerRotationAnim;

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
    WidgetsBinding.instance.addPostFrameCallback((_) => _initLibraries());
  }

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

  /// Re-pulls the search-chip library snapshot (see [_buildLibraryStatus]),
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
    _dismissKeyboard();
  }

  /// Submit top-bar input through the full-screen assistant route.
  void _submitSearchBarToAi() {
    final text = _searchController.text.trim();
    if (text.isEmpty) return;

    _exitAiMode();
    context.push('/assistant');
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      ref.read(aiChatProvider).sendMessage(text);
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

  /// Availability chips for search results, in the requester's vocabulary
  /// (Available / Partially Available / Requested) so they agree with what the
  /// detail page will say — never library-manager jargon (Complete / Missing /
  /// Unmonitored). A title that's in the library but has nothing on disk and
  /// isn't being fetched gets no chip: to a requester it's simply not
  /// available yet, same as a title that isn't in the library at all.
  Map<int, LibraryStatus> _buildLibraryStatus(List<MediaItem> searchResults) {
    final map = <int, LibraryStatus>{};

    const available = LibraryStatus(
      label: 'Available',
      color: AppTheme.available,
    );
    const partial = LibraryStatus(
      label: 'Partially Available',
      color: AppTheme.requested,
    );
    const requested = LibraryStatus(
      label: 'Requested',
      color: AppTheme.requested,
    );

    // Radarr: match by TMDB ID
    final movies = _radarrNotifier?.state.movies ?? [];
    for (final movie in movies) {
      final tmdbId = movie.tmdbId;
      if (tmdbId == null) continue;
      if (movie.hasFile) {
        map[tmdbId] = available;
      } else if (movie.monitored) {
        map[tmdbId] = requested;
      }
    }

    // Sonarr: match by title (Sonarr model lacks TMDB IDs)
    final seriesList = _sonarrNotifier?.state.series ?? [];
    if (seriesList.isNotEmpty) {
      final titleMap = {
        for (final s in seriesList) s.title.toLowerCase(): s,
      };
      for (final item in searchResults) {
        if (item.mediaType == MediaType.tv && !map.containsKey(item.id)) {
          final match = titleMap[item.title.toLowerCase()];
          if (match == null) continue;
          // episodeTotals, not percentComplete: percentComplete only counts
          // monitored episodes, so a series with one downloaded season and
          // the rest unmonitored would read "Available" while most of it is
          // missing.
          final (:files, :total) = match.episodeTotals;
          if (total > 0 && files >= total) {
            map[item.id] = available;
          } else if (files > 0) {
            map[item.id] = partial;
          } else if (match.monitored) {
            map[item.id] = requested;
          }
        }
      }
    }

    return map;
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

  bool _handleScrollNotification(ScrollNotification notification) {
    final atTop =
        notification.metrics.pixels <= notification.metrics.minScrollExtent + 4;

    if (notification is ScrollStartNotification ||
        notification is ScrollUpdateNotification ||
        notification is OverscrollNotification) {
      _dismissKeyboard();
    }

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

    final searchState = ref.watch(shellSearchProvider);
    final searchNotifier = ref.read(shellSearchProvider.notifier);
    final hasAi =
        ref.watch(authProvider).valueOrNull?.connection?.services.ai ?? false;
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
    // Users waiting for a Plex invite — drives the drawer "Plex invites"
    // entry (only shown while someone waits) and the hamburger dot.
    final plexInvitesWaiting = ref.watch(plexInvitesWaitingProvider);
    final menuBadgeCount = pendingApprovals + openIssues + plexInvitesWaiting;
    final showSearchResults = searchState.searchMode == SearchMode.search ||
        searchState.searchMode == SearchMode.aiReady;
    final libraryStatus = searchState.isSearching && showSearchResults
        ? _buildLibraryStatus(searchState.searchResults)
        : const <int, LibraryStatus>{};

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
              ? 'Edit your question or press send...'
              : (hasAi
                  ? 'Search or ask AI...'
                  : 'Search by title or person...'),
          aiEnabled: hasAi,
          onSubmitted: _submitSearchBar,
          onSend: isAiReady ? _submitSearchBarToAi : null,
          onChanged: (q) => searchNotifier.updateSearch(q),
          onClear:
              isAiReady ? _exitAiMode : () => searchNotifier.updateSearch(''),
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
                // Search bar at top (hidden during AI mode)
                if (showGlobalSearch) ...[
                  if (mobile)
                    SizeTransition(
                      sizeFactor: _searchBarCurve,
                      axisAlignment: -1,
                      child: topBar,
                    )
                  else
                    topBar,
                ] else if (!desktop)
                  secondaryTopBar,
                // Module content (includes its own bottom nav)
                Expanded(
                  child: Stack(
                    children: [
                      NotificationListener<ScrollNotification>(
                        onNotification:
                            mobile && !searchState.isSearching && !isAiReady
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
                                      const Text(
                                        'Press send to ask AI',
                                        style: TextStyle(
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
                      // bar height (including text scaling).
                      if (showGlobalSearch &&
                          searchState.searchMode == SearchMode.search &&
                          searchState.isSearching)
                        Positioned.fill(
                          child: ColoredBox(
                            color: AppTheme.background.withValues(alpha: 0.97),
                            child: SearchResultsView(
                              results: searchState.searchResults,
                              isLoading: searchState.isLoadingSearch,
                              query: searchState.searchQuery,
                              onLoadMore: searchNotifier.loadMoreSearch,
                              libraryStatus: libraryStatus,
                              onResultTap: _dismissKeyboard,
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
    final pendingApprovals = ref.watch(pendingApprovalsProvider);
    final openIssues = ref.watch(openIssuesProvider);
    final pendingAgentActions = ref.watch(pendingAgentActionsProvider);
    final plexInvitesWaiting = ref.watch(plexInvitesWaitingProvider);
    // Setup reminder: unconfigured-feature count, shown while the admin
    // hasn't muted it from the checklist screen.
    final setupRemaining = ref.watch(setupStatusProvider)?.remaining ?? 0;
    final showSetupReminder =
        setupRemaining > 0 && ref.watch(setupReminderEnabledProvider);

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

      final item = _DrawerItem(
        icon: module.icon,
        title: module.label,
        selected: isActive,
        trailing: selectorInstances.length > 1
            ? _InstanceSelector(
                appName: module.label,
                instances: selectorInstances,
                activeInstanceId: activeInstance?.id,
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
            instanceId: _defaultInstanceForModule(
              instanceState,
              module.type,
            )?.id,
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
          if (isAdmin) ...[
            const _DrawerSectionHeader('Needs attention'),
            _DrawerItem(
              icon: Icons.fact_check_outlined,
              title: 'Approvals',
              badgeCount: pendingApprovals,
              onTap: () {
                if (isOverlay) Navigator.pop(context);
                context.push('/approvals');
              },
            ),
            _DrawerItem(
              icon: Icons.flag_outlined,
              title: 'Issues',
              badgeCount: openIssues,
              onTap: () {
                if (isOverlay) Navigator.pop(context);
                context.push('/issues');
              },
            ),
            _DrawerItem(
              icon: Icons.build_circle_outlined,
              title: 'Agent fixes',
              badgeCount: pendingAgentActions,
              onTap: () {
                if (isOverlay) Navigator.pop(context);
                context.push('/agent-actions');
              },
            ),
            // Appears only while someone is waiting on a Plex invite (e.g.
            // the push was missed or an auto-invite failed); lands on the
            // Users screen where the invite is one tap.
            if (plexInvitesWaiting > 0)
              _DrawerItem(
                icon: Icons.play_circle_outline,
                title: 'Plex invites',
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
              onTap: () {
                if (isOverlay) Navigator.pop(context);
                context.push('/assistant');
              },
            ),
          if (ref.watch(plexGuideEnabledProvider))
            _DrawerItem(
              icon: Icons.play_circle_outline,
              title: 'Watch on Plex',
              onTap: () {
                if (isOverlay) Navigator.pop(context);
                context.push('/plex-guide');
              },
            ),
          _DrawerItem(
            icon: Icons.settings,
            title: 'Settings',
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

  ServiceInstance? _defaultInstanceForModule(
    InstanceState state,
    ModuleType type,
  ) {
    final instances = _instancesForModule(state, type);
    if (instances.isEmpty) return null;
    return instances.firstWhere(
      (instance) => instance.isDefault,
      orElse: () => instances.first,
    );
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
  final bool selected;
  final VoidCallback onTap;
  final Widget? trailing;

  /// When > 0, renders a trailing count pill (e.g. the pending-approvals count).
  final int badgeCount;

  const _DrawerItem({
    required this.icon,
    required this.title,
    this.selected = false,
    required this.onTap,
    this.trailing,
    this.badgeCount = 0,
  });

  @override
  Widget build(BuildContext context) {
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
          title: Text(
            title,
            style: TextStyle(
              color: selected ? AppTheme.textPrimary : AppTheme.textSecondary,
              fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
              letterSpacing: selected ? 0.05 : 0,
            ),
          ),
          trailing: badgeCount > 0 ? _CountPill(count: badgeCount) : trailing,
          selected: selected,
          selectedTileColor: Colors.transparent,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
          ),
          onTap: onTap,
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
    );
  }
}

class _InstanceSelector extends StatelessWidget {
  final String appName;
  final List<ServiceInstance> instances;
  final String? activeInstanceId;
  final ValueChanged<String> onSelected;

  const _InstanceSelector({
    required this.appName,
    required this.instances,
    required this.activeInstanceId,
    required this.onSelected,
  });

  @override
  Widget build(BuildContext context) {
    final activeInstance = instances.firstWhere(
      (instance) => instance.id == activeInstanceId,
      orElse: () => instances.firstWhere(
        (instance) => instance.isDefault,
        orElse: () => instances.first,
      ),
    );

    return PopupMenuButton<String>(
      tooltip: 'Choose $appName instance',
      color: AppTheme.surface,
      onSelected: onSelected,
      itemBuilder: (context) => [
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
                if (instance.id == activeInstance.id)
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
                  activeInstance.name,
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
