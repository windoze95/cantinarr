import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/models/app_module.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/module_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/search_bar.dart';
import '../../../core/widgets/shimmer_border.dart';
import '../../ai_assistant/data/ai_chat_service.dart';
import '../../ai_assistant/data/ai_models.dart';
import '../../ai_assistant/logic/ai_chat_provider.dart';
import '../../ai_assistant/ui/chat_bubble.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/ui/search_results_view.dart';
import '../../radarr/data/radarr_api_service.dart';
import '../../radarr/logic/radarr_movies_provider.dart';
import '../../sonarr/data/sonarr_api_service.dart';
import '../../sonarr/logic/sonarr_series_provider.dart';
import '../logic/shell_search_provider.dart';

/// The root shell widget with persistent search bar and drawer.
/// Each module provides its own bottom nav via inner StatefulShellRoutes.
class AppShell extends ConsumerStatefulWidget {
  final Widget child;

  const AppShell({
    super.key,
    required this.child,
  });

  @override
  ConsumerState<AppShell> createState() => _AppShellState();
}

class _AppShellState extends ConsumerState<AppShell>
    with TickerProviderStateMixin {
  final _scaffoldKey = GlobalKey<ScaffoldState>();
  final _searchController = TextEditingController();
  final _searchFocusNode = FocusNode();
  final _chatScrollController = ScrollController();
  RadarrMoviesNotifier? _radarrNotifier;
  SonarrSeriesNotifier? _sonarrNotifier;

  // Search bar collapse on scroll (mobile)
  late final AnimationController _searchBarAnim;
  late final Animation<double> _searchBarCurve;

  // AI mode layout flip
  late final AnimationController _aiModeAnim;
  late final Animation<double> _aiModeCurve;

  // Shimmer sweep rotation for aiReady state
  late final AnimationController _shimmerRotationAnim;

  // Shell-scoped AI chat notifier (lazy)
  AiChatNotifier? _aiChatNotifier;
  SearchMode _prevMode = SearchMode.search;

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
    _aiModeAnim = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 500),
    );
    _aiModeCurve = CurvedAnimation(
      parent: _aiModeAnim,
      curve: Curves.easeInOutCubic,
    );
    _shimmerRotationAnim = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 3000),
    );
    WidgetsBinding.instance.addPostFrameCallback((_) => _initLibraries());
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
  }

  void _onLibraryChanged() {
    if (mounted) setState(() {});
  }

  /// Lazily create the shell-scoped AI chat notifier.
  AiChatNotifier _getOrCreateAiChat() {
    if (_aiChatNotifier == null) {
      final backendDio = ref.read(backendClientProvider);
      _aiChatNotifier = AiChatNotifier(
        chatService: AiChatService(backendDio: backendDio),
      );
      _aiChatNotifier!.addListener(_onAiChatChanged);
    }
    return _aiChatNotifier!;
  }

  void _onAiChatChanged() {
    if (mounted) {
      setState(() {});
      // Auto-scroll chat to bottom
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_chatScrollController.hasClients) {
          _chatScrollController.animateTo(
            _chatScrollController.position.maxScrollExtent,
            duration: const Duration(milliseconds: 200),
            curve: Curves.easeOut,
          );
        }
      });
    }
  }

  /// React to search mode changes and drive animations.
  void _onSearchModeChanged(SearchMode mode) {
    if (mode == _prevMode) return;
    _prevMode = mode;

    switch (mode) {
      case SearchMode.noResults:
      case SearchMode.aiReady:
        _shimmerRotationAnim.repeat();
        _getOrCreateAiChat();

      case SearchMode.aiChat:
        _shimmerRotationAnim.stop();
        _shimmerRotationAnim.value = 0;
        _aiModeAnim.forward();
        // Transfer query text to the bottom input and re-request focus
        final query = ref.read(shellSearchProvider).searchQuery;
        if (query.isNotEmpty && _searchController.text != query) {
          _searchController.text = query;
          _searchController.selection = TextSelection.fromPosition(
            TextPosition(offset: query.length),
          );
        }
        _getOrCreateAiChat();
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted && !_searchFocusNode.hasFocus) {
            _searchFocusNode.requestFocus();
          }
        });

      case SearchMode.search:
        _shimmerRotationAnim.stop();
        _shimmerRotationAnim.value = 0;
        _aiModeAnim.reverse();
    }
  }

  void _exitAiMode() {
    _searchController.clear();
    ref.read(shellSearchProvider.notifier).exitAiMode();
    _searchFocusNode.unfocus();
  }

  void _sendAiMessage() {
    final text = _searchController.text.trim();
    if (text.isEmpty || _aiChatNotifier == null) return;
    _searchController.clear();
    _aiChatNotifier!.sendMessage(text);
  }

  /// Submit from the aiReady state: transition to full chat and send message.
  void _submitFromAiReady() {
    final text = _searchController.text.trim();
    if (text.isEmpty) return;
    ref.read(shellSearchProvider.notifier).submitAiReady();
    _searchController.clear();
    _getOrCreateAiChat().sendMessage(text);
  }

  Map<int, LibraryStatus> _buildLibraryStatus(
      List<MediaItem> searchResults) {
    final map = <int, LibraryStatus>{};

    // Radarr: match by TMDB ID
    final movies = _radarrNotifier?.state.movies ?? [];
    for (final movie in movies) {
      if (movie.tmdbId != null) {
        if (movie.hasFile) {
          map[movie.tmdbId!] = const LibraryStatus(
            label: 'Downloaded',
            color: AppTheme.available,
          );
        } else if (movie.monitored) {
          map[movie.tmdbId!] = const LibraryStatus(
            label: 'Missing',
            color: AppTheme.requested,
          );
        } else {
          map[movie.tmdbId!] = const LibraryStatus(
            label: 'Unmonitored',
            color: AppTheme.unavailable,
          );
        }
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
          if (match != null) {
            if (match.percentComplete >= 1.0) {
              map[item.id] = const LibraryStatus(
                label: 'Complete',
                color: AppTheme.available,
              );
            } else if (match.status == 'continuing') {
              map[item.id] = const LibraryStatus(
                label: 'Continuing',
                color: AppTheme.downloading,
              );
            } else {
              map[item.id] = const LibraryStatus(
                label: 'Ended',
                color: AppTheme.unavailable,
              );
            }
          }
        }
      }
    }

    return map;
  }

  bool _isMobile(BuildContext context) =>
      MediaQuery.sizeOf(context).shortestSide < 600;

  bool _isDesktop(BuildContext context) =>
      MediaQuery.sizeOf(context).width >= 900;

  bool _handleScrollNotification(ScrollNotification notification) {
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
    _searchBarAnim.dispose();
    _aiModeAnim.dispose();
    _shimmerRotationAnim.dispose();
    _chatScrollController.dispose();
    _aiChatNotifier?.removeListener(_onAiChatChanged);
    _aiChatNotifier?.dispose();
    _radarrNotifier?.removeListener(_onLibraryChanged);
    _sonarrNotifier?.removeListener(_onLibraryChanged);
    _searchController.dispose();
    _searchFocusNode.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final searchState = ref.watch(shellSearchProvider);
    final searchNotifier = ref.read(shellSearchProvider.notifier);
    final hasAi = ref.watch(authProvider).valueOrNull?.connection?.services.ai ?? false;
    final libraryStatus = searchState.isSearching &&
            searchState.searchMode == SearchMode.search
        ? _buildLibraryStatus(searchState.searchResults)
        : const <int, LibraryStatus>{};

    final mobile = _isMobile(context);
    final desktop = _isDesktop(context);

    // Drive animations from state changes
    _onSearchModeChanged(searchState.searchMode);

    final isAiMode = searchState.searchMode == SearchMode.aiChat;
    final isAiReady = searchState.searchMode == SearchMode.aiReady;

    final searchBar = Padding(
      padding: EdgeInsets.fromLTRB(desktop ? 16 : 4, 8, 16, 8),
      child: AnimatedBuilder(
        animation: _shimmerRotationAnim,
        builder: (context, child) {
          return CustomPaint(
            foregroundPainter: isAiReady
                ? ShimmerBorderPainter(
                    progress: _shimmerRotationAnim.value,
                    borderRadius: 14.0,
                    accentColor: AppTheme.accent,
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
              : (hasAi ? 'Search or ask AI...' : 'Search movies & TV shows...'),
          aiEnabled: hasAi,
          onSend: isAiReady ? _submitFromAiReady : null,
          onChanged: isAiMode ? null : (q) => searchNotifier.updateSearch(q),
          onClear: isAiReady || isAiMode
              ? _exitAiMode
              : () => searchNotifier.updateSearch(''),
        ),
      ),
    );

    // Top bar: hamburger + search on non-desktop, just search on desktop
    Widget topBar;
    if (desktop) {
      topBar = searchBar;
    } else {
      topBar = Row(
        children: [
          Padding(
            padding: const EdgeInsets.only(left: 4, top: 8, bottom: 8),
            child: IconButton(
              icon: const Icon(Icons.menu, color: AppTheme.textPrimary),
              onPressed: () => _scaffoldKey.currentState?.openDrawer(),
            ),
          ),
          Expanded(child: searchBar),
        ],
      );
    }

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
                if (!isAiMode) ...[
                  if (mobile)
                    SizeTransition(
                      sizeFactor: _searchBarCurve,
                      axisAlignment: -1,
                      child: topBar,
                    )
                  else
                    topBar,
                ],
                // Module content (includes its own bottom nav)
                Expanded(
                  child: Stack(
                    children: [
                      NotificationListener<ScrollNotification>(
                        onNotification:
                            mobile && !searchState.isSearching && !isAiMode && !isAiReady
                                ? _handleScrollNotification
                                : null,
                        child: widget.child,
                      ),
                      if (isAiReady)
                        Positioned.fill(
                          child: Container(
                            color: AppTheme.background,
                            child: Column(
                              children: [
                                const SizedBox(height: 48),
                                Icon(
                                  Icons.auto_awesome,
                                  size: 32,
                                  color: AppTheme.accent.withValues(alpha: 0.5),
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
                        ),
                    ],
                  ),
                ),
              ],
            ),

            // Overlay: search results (normal search mode)
            if (searchState.searchMode == SearchMode.search &&
                searchState.isSearching)
              Positioned.fill(
                top: 60, // below the search bar
                child: Container(
                  color: AppTheme.background,
                  child: SearchResultsView(
                    results: searchState.searchResults,
                    isLoading: searchState.isLoadingSearch,
                    query: searchState.searchQuery,
                    onLoadMore: searchNotifier.loadMoreSearch,
                    libraryStatus: libraryStatus,
                  ),
                ),
              ),

            // Overlay: AI chat mode
            if (isAiMode) _buildAiChatOverlay(),

            // Bottom fade gradient (only when not in AI mode)
            if (!isAiMode)
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
                          AppTheme.accent.withValues(alpha: 0),
                          AppTheme.accent.withValues(alpha: 0.08),
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
          SizedBox(
            width: 280,
            child: Material(
              color: AppTheme.surface,
              child: _buildDrawerContent(context, isOverlay: false),
            ),
          ),
          const VerticalDivider(width: 1, thickness: 1, color: AppTheme.border),
          Expanded(child: scaffold),
        ],
      );
    }

    return scaffold;
  }

  /// AI chat overlay: messages above, multiline input at bottom.
  Widget _buildAiChatOverlay() {
    final chatState = _aiChatNotifier?.state;
    final messages = chatState?.messages ?? [];
    final isLoading = chatState?.isLoading ?? false;

    return AnimatedBuilder(
      animation: _aiModeCurve,
      builder: (context, _) {
        return Positioned.fill(
          child: FadeTransition(
            opacity: _aiModeCurve,
            child: Container(
              color: AppTheme.background,
              child: Column(
                children: [
                  // Header
                  Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 12, vertical: 8),
                    decoration: const BoxDecoration(
                      border: Border(
                        bottom: BorderSide(color: AppTheme.border),
                      ),
                    ),
                    child: Row(
                      children: [
                        IconButton(
                          icon: const Icon(Icons.arrow_back,
                              color: AppTheme.textSecondary, size: 22),
                          onPressed: _exitAiMode,
                          tooltip: 'Back to search',
                        ),
                        Icon(
                          Icons.auto_awesome,
                          size: 18,
                          color: AppTheme.accent.withValues(alpha: 0.8),
                        ),
                        const SizedBox(width: 8),
                        const Text(
                          'AI',
                          style: TextStyle(
                            color: AppTheme.textPrimary,
                            fontSize: 16,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                        const Spacer(),
                        if (messages.length > 1)
                          IconButton(
                            icon: const Icon(Icons.delete_outline,
                                color: AppTheme.textSecondary, size: 20),
                            onPressed: () {
                              _aiChatNotifier?.clearChat();
                            },
                            tooltip: 'Clear chat',
                          ),
                      ],
                    ),
                  ),

                  // Chat messages
                  Expanded(
                    child: ListView.builder(
                      controller: _chatScrollController,
                      padding: const EdgeInsets.all(16),
                      itemCount: messages.length +
                          (isLoading && (messages.isEmpty ||
                                  messages.last.role != ChatRole.assistant)
                              ? 1
                              : 0),
                      itemBuilder: (context, index) {
                        if (index >= messages.length) {
                          return const _TypingIndicator();
                        }
                        // Skip the initial welcome message
                        final msg = messages[index];
                        if (index == 0 && msg.role == ChatRole.assistant) {
                          return const SizedBox.shrink();
                        }
                        return ChatBubble(message: msg);
                      },
                    ),
                  ),

                  // Error
                  if (chatState?.error != null)
                    Padding(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 16, vertical: 4),
                      child: Text(
                        chatState!.error!,
                        style: const TextStyle(
                            color: AppTheme.error, fontSize: 12),
                        maxLines: 2,
                      ),
                    ),

                  // Bottom input bar
                  Container(
                    decoration: BoxDecoration(
                      color: AppTheme.surface,
                      border: const Border(
                        top: BorderSide(color: AppTheme.border),
                      ),
                      boxShadow: [
                        BoxShadow(
                          color: AppTheme.accent.withValues(alpha: 0.05),
                          blurRadius: 12,
                          offset: const Offset(0, -2),
                        ),
                      ],
                    ),
                    padding: const EdgeInsets.fromLTRB(12, 8, 12, 0),
                    child: SafeArea(
                      top: false,
                      child: CantinarrSearchBar(
                        controller: _searchController,
                        focusNode: _searchFocusNode,
                        hintText: 'Ask about movies, shows...',
                        aiEnabled: true,
                        multiline: true,
                        onSend: _sendAiMessage,
                        onChanged: (_) => setState(() {}),
                        onClear: _exitAiMode,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }

  Widget _buildDrawer(BuildContext context) {
    return Drawer(
      backgroundColor: AppTheme.surface,
      child: _buildDrawerContent(context, isOverlay: true),
    );
  }

  Widget _buildDrawerContent(BuildContext context, {required bool isOverlay}) {
    final moduleState = ref.watch(moduleProvider);

    return SafeArea(
      child: Column(
        children: [
          // Header
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(24),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Container(
                  width: 48,
                  height: 48,
                  decoration: BoxDecoration(
                    color: AppTheme.accent.withValues(alpha: 0.15),
                    borderRadius: BorderRadius.circular(12),
                  ),
                  child: const Icon(Icons.movie_filter,
                      color: AppTheme.accent, size: 28),
                ),
                const SizedBox(height: 12),
                const Text(
                  'Cantinarr',
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 24,
                    fontWeight: FontWeight.bold,
                  ),
                ),
                const Text(
                  'Your media companion',
                  style:
                      TextStyle(color: AppTheme.textSecondary, fontSize: 14),
                ),
              ],
            ),
          ),
          const Divider(color: AppTheme.border),

          // Scrollable module navigation items
          Expanded(
            child: ListView(
              padding: EdgeInsets.zero,
              children: moduleState.modules.asMap().entries.map((entry) {
                final module = entry.value;
                final isActive = entry.key == moduleState.activeIndex;

                return _DrawerItem(
                  icon: module.icon,
                  title: module.label,
                  selected: isActive,
                  onTap: () {
                    if (isOverlay) Navigator.pop(context);
                    _navigateToModule(context, module);
                    ref.read(moduleProvider.notifier).setActiveModule(
                          module.type,
                          instanceId: module.instanceId,
                        );
                  },
                );
              }).toList(),
            ),
          ),

          const Divider(color: AppTheme.border),

          _DrawerItem(
            icon: Icons.play_circle_outline,
            title: 'Plex Setup Guide',
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

  void _navigateToModule(BuildContext context, AppModule module) {
    switch (module.type) {
      case ModuleType.dashboard:
        context.go('/dashboard/movies');
      case ModuleType.radarr:
        context.go('/radarr/library');
      case ModuleType.sonarr:
        context.go('/sonarr/library');
      case ModuleType.assistant:
        context.go('/assistant');
    }
  }
}

class _DrawerItem extends StatelessWidget {
  final IconData icon;
  final String title;
  final bool selected;
  final VoidCallback onTap;

  const _DrawerItem({
    required this.icon,
    required this.title,
    this.selected = false,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: Icon(icon,
          color: selected ? AppTheme.accent : AppTheme.textSecondary),
      title: Text(
        title,
        style: TextStyle(
          color: selected ? AppTheme.accent : AppTheme.textPrimary,
          fontWeight: selected ? FontWeight.w600 : FontWeight.w400,
        ),
      ),
      selected: selected,
      selectedTileColor: AppTheme.accent.withValues(alpha: 0.08),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
      onTap: onTap,
    );
  }
}

/// Typing indicator with animated dots.
class _TypingIndicator extends StatelessWidget {
  const _TypingIndicator();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Row(
        children: [
          Container(
            padding:
                const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
            decoration: BoxDecoration(
              color: AppTheme.surfaceVariant,
              borderRadius: BorderRadius.circular(16),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: List.generate(
                3,
                (i) => Padding(
                  padding: EdgeInsets.only(left: i > 0 ? 4 : 0),
                  child: _Dot(delay: i * 200),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _Dot extends StatefulWidget {
  final int delay;
  const _Dot({required this.delay});

  @override
  State<_Dot> createState() => _DotState();
}

class _DotState extends State<_Dot> with SingleTickerProviderStateMixin {
  late AnimationController _controller;
  late Animation<double> _animation;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      duration: const Duration(milliseconds: 600),
      vsync: this,
    );
    _animation = Tween(begin: 0.0, end: 1.0).animate(
      CurvedAnimation(parent: _controller, curve: Curves.easeInOut),
    );
    Future.delayed(Duration(milliseconds: widget.delay), () {
      if (mounted) _controller.repeat(reverse: true);
    });
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: _animation,
      builder: (_, __) => Container(
        width: 8,
        height: 8,
        decoration: BoxDecoration(
          color: AppTheme.textSecondary
              .withValues(alpha: 0.3 + _animation.value * 0.7),
          shape: BoxShape.circle,
        ),
      ),
    );
  }
}
