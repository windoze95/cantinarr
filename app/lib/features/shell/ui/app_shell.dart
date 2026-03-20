import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/models/app_module.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/module_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/search_bar.dart';
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
    with SingleTickerProviderStateMixin {
  final _searchController = TextEditingController();
  final _searchFocusNode = FocusNode();
  RadarrMoviesNotifier? _radarrNotifier;
  SonarrSeriesNotifier? _sonarrNotifier;
  late final AnimationController _searchBarAnim;
  late final Animation<double> _searchBarCurve;

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
    } else if (auth?.connection?.services.radarr ?? false) {
      // Legacy fallback
      _radarrNotifier = RadarrMoviesNotifier(
        RadarrApiService(backendDio: backendDio),
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
    } else if (auth?.connection?.services.sonarr ?? false) {
      _sonarrNotifier = SonarrSeriesNotifier(
        SonarrApiService(backendDio: backendDio),
      );
      _sonarrNotifier!.addListener(_onLibraryChanged);
      _sonarrNotifier!.loadSeries();
    }
  }

  void _onLibraryChanged() {
    if (mounted) setState(() {});
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
    final libraryStatus = searchState.isSearching
        ? _buildLibraryStatus(searchState.searchResults)
        : const <int, LibraryStatus>{};

    final mobile = _isMobile(context);

    final searchBar = Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: CantinarrSearchBar(
        controller: _searchController,
        focusNode: _searchFocusNode,
        onChanged: (q) => searchNotifier.updateSearch(q),
        onClear: () => searchNotifier.updateSearch(''),
      ),
    );

    return Scaffold(
      body: SafeArea(
        bottom: false,
        child: Stack(
          children: [
            // Base layer: search bar + module content
            Column(
              children: [
                // Search bar: collapses on scroll (mobile only)
                if (mobile)
                  SizeTransition(
                    sizeFactor: _searchBarCurve,
                    axisAlignment: -1,
                    child: searchBar,
                  )
                else
                  searchBar,
                // Module content (includes its own bottom nav)
                Expanded(
                  child: NotificationListener<ScrollNotification>(
                    onNotification: mobile && !searchState.isSearching
                        ? _handleScrollNotification
                        : null,
                    child: widget.child,
                  ),
                ),
              ],
            ),

            // Overlay: search results when searching
            if (searchState.isSearching)
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

            // Bottom fade gradient
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
      drawer: _buildDrawer(context),
    );
  }

  Widget _buildDrawer(BuildContext context) {
    final moduleState = ref.watch(moduleProvider);

    return Drawer(
      backgroundColor: AppTheme.surface,
      child: SafeArea(
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

            // Module navigation items
            ...moduleState.modules.asMap().entries.map((entry) {
              final module = entry.value;
              final isActive = entry.key == moduleState.activeIndex;

              return _DrawerItem(
                icon: module.icon,
                title: module.label,
                selected: isActive,
                onTap: () {
                  Navigator.pop(context);
                  _navigateToModule(context, module);
                  ref.read(moduleProvider.notifier).setActiveModule(
                        module.type,
                        instanceId: module.instanceId,
                      );
                },
              );
            }),

            const Spacer(),
            const Divider(color: AppTheme.border),

            _DrawerItem(
              icon: Icons.play_circle_outline,
              title: 'Plex Setup Guide',
              onTap: () {
                Navigator.pop(context);
                context.push('/plex-guide');
              },
            ),
            _DrawerItem(
              icon: Icons.settings,
              title: 'Settings',
              onTap: () {
                Navigator.pop(context);
                context.push('/settings');
              },
            ),
            const SizedBox(height: 8),
          ],
        ),
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
