import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/ui/category_row.dart';
import '../data/radarr_api_service.dart';
import '../logic/movie_discover_provider.dart';
import '../logic/radarr_movies_provider.dart';
import 'radarr_movie_list.dart';

/// Composite Movies tab: discovery rows + Radarr library.
class MoviesTabScreen extends ConsumerStatefulWidget {
  const MoviesTabScreen({super.key});

  @override
  ConsumerState<MoviesTabScreen> createState() => _MoviesTabScreenState();
}

class _MoviesTabScreenState extends ConsumerState<MoviesTabScreen> {
  RadarrMoviesNotifier? _libraryNotifier;
  final _searchController = TextEditingController();
  bool _hasRadarr = false;

  @override
  void initState() {
    super.initState();
    // Bootstrap discovery data.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(movieDiscoverProvider.notifier).bootstrap();
      _initLibrary();
    });
  }

  void _initLibrary() {
    final auth = ref.read(authProvider).valueOrNull;
    final defaultRadarr = auth?.connection?.defaultRadarrInstance;
    _hasRadarr = defaultRadarr != null;
    if (_hasRadarr) {
      final backendDio = ref.read(backendClientProvider);
      final service = RadarrApiService(
        backendDio: backendDio,
        instanceId: defaultRadarr!.id,
      );
      _libraryNotifier = RadarrMoviesNotifier(service);
      _libraryNotifier!.loadMovies();
      setState(() {});
    }
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _onRefresh() async {
    await Future.wait([
      ref.read(movieDiscoverProvider.notifier).bootstrap(),
      if (_libraryNotifier != null) _libraryNotifier!.loadMovies(),
    ]);
  }

  @override
  Widget build(BuildContext context) {
    final discover = ref.watch(movieDiscoverProvider);

    return Scaffold(
      body: SafeArea(
        child: RefreshIndicator(
          onRefresh: _onRefresh,
          color: AppTheme.accent,
          child: ListView(
            padding: const EdgeInsets.only(bottom: 24),
            children: [
              // Discovery rows
              CategoryRow(
                title: 'Popular Movies',
                items: discover.popularMovies,
                isLoading: discover.isLoadingPopular,
              ),
              if (discover.topRated.isNotEmpty)
                CategoryRow(
                  title: 'Top Rated',
                  items: discover.topRated,
                  isLoading: discover.isLoadingTopRated,
                ),
              if (discover.upcoming.isNotEmpty)
                CategoryRow(
                  title: 'Coming Soon',
                  items: discover.upcoming,
                  isLoading: discover.isLoadingUpcoming,
                ),
              if (discover.anticipated.isNotEmpty)
                CategoryRow(
                  title: 'Most Anticipated',
                  items: discover.anticipated,
                  isLoading: discover.isLoadingAnticipated,
                ),

              // Library section
              _SectionHeader(title: 'Your Library'),

              if (_hasRadarr && _libraryNotifier != null)
                _LibrarySection(
                  notifier: _libraryNotifier!,
                  searchController: _searchController,
                )
              else
                _UnconfiguredPlaceholder(
                  icon: Icons.movie_outlined,
                  message: 'Radarr is not configured on this server.',
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _LibrarySection extends StatelessWidget {
  final RadarrMoviesNotifier notifier;
  final TextEditingController searchController;

  const _LibrarySection({
    required this.notifier,
    required this.searchController,
  });

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: notifier,
      builder: (context, _) {
        final state = notifier.state;
        return Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            // Stats bar
            Container(
              padding:
                  const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              color: AppTheme.surface,
              child: Row(
                mainAxisAlignment: MainAxisAlignment.spaceAround,
                children: [
                  _StatChip(
                      label: 'Total',
                      count: state.movies.length,
                      color: AppTheme.textPrimary),
                  _StatChip(
                      label: 'Downloaded',
                      count: state.downloadedCount,
                      color: AppTheme.available),
                  _StatChip(
                      label: 'Missing',
                      count: state.missingCount,
                      color: AppTheme.requested),
                ],
              ),
            ),

            // Search + filter
            Padding(
              padding: const EdgeInsets.all(12),
              child: Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: searchController,
                      onChanged: notifier.search,
                      decoration: InputDecoration(
                        hintText: 'Search movies...',
                        prefixIcon: const Icon(Icons.search),
                        suffixIcon: searchController.text.isNotEmpty
                            ? IconButton(
                                icon: const Icon(Icons.close),
                                onPressed: () {
                                  searchController.clear();
                                  notifier.search('');
                                },
                              )
                            : null,
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  PopupMenuButton<RadarrFilter>(
                    icon: const Icon(Icons.filter_list,
                        color: AppTheme.textPrimary),
                    onSelected: notifier.setFilter,
                    itemBuilder: (_) => RadarrFilter.values
                        .map((f) => PopupMenuItem(
                              value: f,
                              child: Row(
                                children: [
                                  if (f == state.filter)
                                    const Icon(Icons.check,
                                        size: 18, color: AppTheme.accent),
                                  if (f != state.filter)
                                    const SizedBox(width: 18),
                                  const SizedBox(width: 8),
                                  Text(f.name[0].toUpperCase() +
                                      f.name.substring(1)),
                                ],
                              ),
                            ))
                        .toList(),
                  ),
                ],
              ),
            ),

            if (state.error != null)
              ErrorBanner(
                message: state.error!,
                onRetry: notifier.loadMovies,
              ),

            // Movie list
            if (state.isLoading && state.movies.isEmpty)
              const Padding(
                padding: EdgeInsets.all(32),
                child: Center(
                  child:
                      CircularProgressIndicator(color: AppTheme.accent),
                ),
              )
            else
              RadarrMovieList(
                movies: state.filtered,
                onDelete: (id) =>
                    notifier.deleteMovie(id, deleteFiles: false),
                onSearch: notifier.searchForMovie,
                embedded: true,
              ),
          ],
        );
      },
    );
  }
}

class _SectionHeader extends StatelessWidget {
  final String title;
  const _SectionHeader({required this.title});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 28, 16, 8),
      child: Row(
        children: [
          Expanded(
            child: Container(height: 1, color: AppTheme.border),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12),
            child: Text(
              title,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                fontWeight: FontWeight.w600,
                letterSpacing: 0.5,
              ),
            ),
          ),
          Expanded(
            child: Container(height: 1, color: AppTheme.border),
          ),
        ],
      ),
    );
  }
}

class _UnconfiguredPlaceholder extends StatelessWidget {
  final IconData icon;
  final String message;

  const _UnconfiguredPlaceholder({
    required this.icon,
    required this.message,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(32),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 48, color: AppTheme.textSecondary),
          const SizedBox(height: 12),
          Text(
            message,
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 14),
            textAlign: TextAlign.center,
          ),
        ],
      ),
    );
  }
}

class _StatChip extends StatelessWidget {
  final String label;
  final int count;
  final Color color;

  const _StatChip({
    required this.label,
    required this.count,
    required this.color,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          count.toString(),
          style: TextStyle(
              color: color, fontSize: 20, fontWeight: FontWeight.bold),
        ),
        Text(label,
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 12)),
      ],
    );
  }
}
