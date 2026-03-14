import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_models.dart';
import '../logic/radarr_movies_provider.dart';
import 'radarr_movie_list.dart';

/// Admin view for Radarr movie management.
class RadarrHomeScreen extends ConsumerStatefulWidget {
  const RadarrHomeScreen({super.key});

  @override
  ConsumerState<RadarrHomeScreen> createState() => _RadarrHomeScreenState();
}

class _RadarrHomeScreenState extends ConsumerState<RadarrHomeScreen> {
  late final RadarrMoviesNotifier _notifier;
  final _searchController = TextEditingController();

  @override
  void initState() {
    super.initState();
    final backendDio = ref.read(backendClientProvider);
    final service = RadarrApiService(backendDio: backendDio);
    _notifier = RadarrMoviesNotifier(service);
    _notifier.loadMovies();
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: _notifier,
      builder: (context, _) {
        final state = _notifier.state;

        return Scaffold(
          body: Column(
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
                        controller: _searchController,
                        onChanged: _notifier.search,
                        decoration: InputDecoration(
                          hintText: 'Search movies...',
                          prefixIcon: const Icon(Icons.search),
                          suffixIcon: _searchController.text.isNotEmpty
                              ? IconButton(
                                  icon: const Icon(Icons.close),
                                  onPressed: () {
                                    _searchController.clear();
                                    _notifier.search('');
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
                      onSelected: _notifier.setFilter,
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
                  onRetry: _notifier.loadMovies,
                ),

              // Movie list
              Expanded(
                child: state.isLoading && state.movies.isEmpty
                    ? const Center(
                        child:
                            CircularProgressIndicator(color: AppTheme.accent))
                    : RefreshIndicator(
                        onRefresh: _notifier.loadMovies,
                        color: AppTheme.accent,
                        child: RadarrMovieList(
                          movies: state.filtered,
                          onDelete: (id) =>
                              _notifier.deleteMovie(id, deleteFiles: false),
                          onSearch: _notifier.searchForMovie,
                        ),
                      ),
              ),
            ],
          ),
        );
      },
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
          style:
              TextStyle(color: color, fontSize: 20, fontWeight: FontWeight.bold),
        ),
        Text(label,
            style:
                const TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
      ],
    );
  }
}
