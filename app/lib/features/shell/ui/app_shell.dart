import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
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

/// The root shell widget with persistent search bar, bottom navigation,
/// and a drawer. 3 tabs: Movies | TV Shows | Assistant.
class AppShell extends ConsumerStatefulWidget {
  final int currentIndex;
  final Widget child;
  final ValueChanged<int> onTabChanged;

  const AppShell({
    super.key,
    required this.currentIndex,
    required this.child,
    required this.onTabChanged,
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
    if (auth?.connection?.services.radarr ?? false) {
      _radarrNotifier = RadarrMoviesNotifier(
        RadarrApiService(backendDio: backendDio),
      );
      _radarrNotifier!.addListener(_onLibraryChanged);
      _radarrNotifier!.loadMovies();
    }
    if (auth?.connection?.services.sonarr ?? false) {
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
        child: Stack(
          children: [
            // Base layer: search bar + tab content
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
                // Tab content
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
      bottomNavigationBar: _buildBottomNav(),
      drawer: _buildDrawer(context),
    );
  }

  Widget _buildBottomNav() {
    return Container(
      decoration: const BoxDecoration(
        border: Border(top: BorderSide(color: AppTheme.border, width: 0.5)),
      ),
      child: BottomNavigationBar(
        currentIndex: widget.currentIndex,
        onTap: widget.onTabChanged,
        items: const [
          BottomNavigationBarItem(
            icon: Icon(Icons.movie_outlined),
            activeIcon: Icon(Icons.movie),
            label: 'Movies',
          ),
          BottomNavigationBarItem(
            icon: Icon(Icons.tv_outlined),
            activeIcon: Icon(Icons.tv),
            label: 'TV Shows',
          ),
          BottomNavigationBarItem(
            icon: Icon(Icons.smart_toy_outlined),
            activeIcon: Icon(Icons.smart_toy),
            label: 'Assistant',
          ),
        ],
      ),
    );
  }

  Widget _buildDrawer(BuildContext context) {
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

            // Navigation items
            _DrawerItem(
              icon: Icons.movie,
              title: 'Movies',
              selected: widget.currentIndex == 0,
              onTap: () {
                Navigator.pop(context);
                widget.onTabChanged(0);
              },
            ),
            _DrawerItem(
              icon: Icons.tv,
              title: 'TV Shows',
              selected: widget.currentIndex == 1,
              onTap: () {
                Navigator.pop(context);
                widget.onTabChanged(1);
              },
            ),
            _DrawerItem(
              icon: Icons.smart_toy,
              title: 'AI Assistant',
              selected: widget.currentIndex == 2,
              onTap: () {
                Navigator.pop(context);
                widget.onTabChanged(2);
              },
            ),

            const Spacer(),
            const Divider(color: AppTheme.border),

            _DrawerItem(
              icon: Icons.play_circle_outline,
              title: 'Plex Setup Guide',
              onTap: () => Navigator.pop(context),
            ),
            _DrawerItem(
              icon: Icons.settings,
              title: 'Settings',
              onTap: () => Navigator.pop(context),
            ),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
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
