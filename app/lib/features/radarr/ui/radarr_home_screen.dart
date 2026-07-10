import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../core/widgets/library_command_header.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_models.dart';
import '../logic/radarr_movies_provider.dart';
import 'radarr_movie_detail_screen.dart';
import 'radarr_movie_list.dart';
import 'radarr_releases_screen.dart';

/// Radarr library management screen (used in the Radarr module).
/// Instance-aware: uses the active Radarr instance from the instance provider.
class RadarrHomeScreen extends ConsumerStatefulWidget {
  const RadarrHomeScreen({super.key});

  @override
  ConsumerState<RadarrHomeScreen> createState() => _RadarrHomeScreenState();
}

class _RadarrHomeScreenState extends ConsumerState<RadarrHomeScreen> {
  RadarrMoviesNotifier? _notifier;
  final _searchController = TextEditingController();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _initNotifier());
  }

  void _initNotifier() {
    final instanceState = ref.read(instanceProvider);
    final activeInstance = instanceState.activeRadarrInstance;
    if (activeInstance == null) return;

    final backendDio = ref.read(backendClientProvider);
    final service = RadarrApiService(
      backendDio: backendDio,
      instanceId: activeInstance.id,
    );
    _notifier = RadarrMoviesNotifier(service);
    _notifier!.loadMovies();
    setState(() {});
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _triggerAutomaticSearch(int movieId) async {
    try {
      await _notifier!.searchForMovie(movieId);
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Movie search started')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to start search: $e')));
    }
  }

  void _openInteractiveSearch(RadarrMovie movie) {
    final instanceId = ref.read(instanceProvider).activeRadarrInstance?.id;
    if (instanceId == null) return;
    Navigator.of(context, rootNavigator: true).push(
      MaterialPageRoute(
        builder: (_) => RadarrReleasesScreen(
          instanceId: instanceId,
          movieId: movie.id,
          movieTitle: movie.title,
        ),
      ),
    );
  }

  Future<void> _openMovie(RadarrMovie movie) async {
    final instanceId = ref.read(instanceProvider).activeRadarrInstance?.id;
    if (instanceId == null) return;
    await Navigator.of(context, rootNavigator: true).push(
      MaterialPageRoute(
        builder: (_) => RadarrMovieDetailScreen(
          instanceId: instanceId,
          movie: movie,
        ),
      ),
    );
    // The detail screen can edit or remove the movie; refresh on return.
    _notifier?.loadMovies();
  }

  @override
  Widget build(BuildContext context) {
    // Rebuild when active instance changes
    ref.listen(instanceProvider.select((s) => s.activeRadarrInstanceId),
        (_, __) => _initNotifier());

    if (_notifier == null) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }

    return ListenableBuilder(
      listenable: _notifier!,
      builder: (context, _) {
        final state = _notifier!.state;
        final instanceName =
            ref.watch(instanceProvider).activeRadarrInstance?.name ?? 'Radarr';

        return Column(
          children: [
            LibraryCommandHeader(
              title: 'Movie library',
              subtitle: '$instanceName  /  Radarr',
              stats: [
                LibraryStat(
                  label: 'Total',
                  value: state.movies.length,
                  color: AppTheme.textPrimary,
                ),
                LibraryStat(
                  label: 'Ready',
                  value: state.downloadedCount,
                  color: AppTheme.available,
                ),
                LibraryStat(
                  label: 'Missing',
                  value: state.missingCount,
                  color: AppTheme.requested,
                ),
              ],
              searchController: _searchController,
              onSearch: _notifier!.search,
              searchHint: 'Filter this movie library…',
              filter: PopupMenuButton<RadarrFilter>(
                tooltip: 'Filter movies',
                icon: const Icon(Icons.tune_rounded),
                onSelected: _notifier!.setFilter,
                itemBuilder: (_) => RadarrFilter.values
                    .map((f) => PopupMenuItem(
                          value: f,
                          child: Row(
                            children: [
                              if (f == state.filter)
                                const Icon(
                                  Icons.check,
                                  size: 18,
                                  color: AppTheme.accent,
                                ),
                              if (f != state.filter) const SizedBox(width: 18),
                              const SizedBox(width: 8),
                              Text(
                                f.name[0].toUpperCase() + f.name.substring(1),
                              ),
                            ],
                          ),
                        ))
                    .toList(),
              ),
            ),

            if (state.error != null)
              ErrorBanner(
                message: state.error!,
                onRetry: _notifier!.loadMovies,
              ),

            // Movie list
            Expanded(
              child: state.isLoading && state.movies.isEmpty
                  ? const Center(
                      child: CircularProgressIndicator(color: AppTheme.accent))
                  : RefreshIndicator(
                      onRefresh: _notifier!.loadMovies,
                      color: AppTheme.accent,
                      child: RadarrMovieList(
                        movies: state.filtered,
                        onDelete: (id, {bool deleteFiles = false}) => _notifier!
                            .deleteMovie(id, deleteFiles: deleteFiles),
                        onSearch: _triggerAutomaticSearch,
                        onInteractiveSearch: _openInteractiveSearch,
                        onOpen: _openMovie,
                      ),
                    ),
            ),
          ],
        );
      },
    );
  }
}
