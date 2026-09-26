import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/storage/library_sort_preferences.dart';
import '../../../core/widgets/library_sort_menu.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/storage/library_view_preferences.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../core/widgets/library_actions.dart';
import '../../../core/widgets/library_command_header.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_models.dart';
import '../logic/radarr_movies_provider.dart';
import 'movie_actions.dart';
import 'radarr_movie_detail_screen.dart';
import 'radarr_movie_list.dart';

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
    // Listen before the first frame so preference restoration cannot race the
    // instance notifier's creation. Its initial selection is also read below.
    ref.listenManual(librarySortProvider('radarr'), (_, selection) =>
        _notifier?.sorting.setSelection(selection));
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
    _notifier?.dispose();
    _notifier = RadarrMoviesNotifier(service);
    _notifier!.sorting.setSelection(ref.read(librarySortProvider('radarr')));
    _notifier!.loadMovies();
    setState(() {});
  }

  @override
  void dispose() {
    _notifier?.dispose();
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _openMovie(RadarrMovie movie) async {
    final instanceId = ref.read(instanceProvider).activeRadarrInstance?.id;
    if (instanceId == null) return;
    final notifier = _notifier;
    await Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => RadarrMovieDetailScreen(
          instanceId: instanceId,
          movie: movie,
        ),
      ),
    );
    // The detail screen can edit or remove the movie; refresh on return.
    if (mounted && identical(_notifier, notifier)) notifier?.loadMovies();
  }

  /// Tile and detail menus run the same actions.
  void _showMovieActions(RadarrMovie movie, LibraryAction action) {
    final instanceId = ref.read(instanceProvider).activeRadarrInstance?.id;
    if (instanceId == null) return;
    final notifier = _notifier;
    void reload() {
      if (mounted && identical(_notifier, notifier)) {
        notifier?.loadMovies();
      }
    }
    showMovieActions(
      context,
      service: RadarrApiService(
        backendDio: ref.read(backendClientProvider),
        instanceId: instanceId,
      ),
      instanceId: instanceId,
      movie: movie,
      selectedAction: action,
      onChanged: reload,
      onRemoved: reload,
    );
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

    final sort = ref.watch(librarySortProvider('radarr'));
    final viewMode = ref.watch(libraryViewModeProvider('radarr'));
    final instanceId = ref.watch(instanceProvider).activeRadarrInstance?.id;

    return ListenableBuilder(
      listenable: _notifier!,
      builder: (context, _) {
        final state = _notifier!.state;
        final instanceName =
            ref.watch(instanceProvider).activeRadarrInstance?.name ?? 'Radarr';

        return LibraryCommandLayout(
          key: ValueKey('radarr-$instanceId'),
          headerBuilder: (collapsed) => LibraryCommandHeader(
              collapsed: collapsed,
              sort: LibrarySortMenu(module: 'radarr', selection: sort,
                onSelected: (field) => ref.read(librarySortProvider('radarr').notifier).select(field)),
              viewMode: viewMode,
              onViewModeChanged: (value) => ref
                  .read(libraryViewModeProvider('radarr').notifier).set(value),
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

          children: [
            if (_notifier!.sorting.notice != null)
              ErrorBanner(message: _notifier!.sorting.notice!, maxLines: null,
                onRetry: _notifier!.sorting.canRetry ? _notifier!.sorting.refresh : null),
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
                  : state.error != null && state.movies.isEmpty
                      ? const SizedBox.shrink()
                      : RefreshIndicator(
                          onRefresh: _notifier!.loadMovies,
                          color: AppTheme.accent,
                          child: RadarrMovieList(
                            viewMode: viewMode,
                            scrollKey: 'radarr-$instanceId-${_notifier!.sorting.effectiveSelection.key}',
                            movies: state.filtered,
                            onOpen: _openMovie,
                            onAction: _showMovieActions,
                          ),
                        ),
            ),
          ],
        );
      },
    );
  }
}
