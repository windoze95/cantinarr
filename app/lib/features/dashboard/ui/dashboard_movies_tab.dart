import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/ui/category_row.dart';
import '../../radarr/data/radarr_api_service.dart';
import '../../radarr/data/radarr_models.dart';
import '../../radarr/logic/movie_discover_provider.dart';

/// Dashboard Movies tab: discovery rows + Radarr library rows.
class DashboardMoviesTab extends ConsumerStatefulWidget {
  const DashboardMoviesTab({super.key});

  @override
  ConsumerState<DashboardMoviesTab> createState() =>
      _DashboardMoviesTabState();
}

class _DashboardMoviesTabState extends ConsumerState<DashboardMoviesTab> {
  List<RadarrMovie> _recentlyDownloaded = [];
  List<RadarrMovie> _downloadingSoon = [];
  Set<int> _downloadingMovieIds = {};
  bool _isLoadingLibrary = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(movieDiscoverProvider.notifier).bootstrap();
      _loadLibraryPreview();
    });
  }

  Future<void> _loadLibraryPreview() async {
    final auth = ref.read(authProvider).valueOrNull;
    final defaultRadarr = auth?.connection?.defaultRadarrInstance;
    if (defaultRadarr == null) return;

    setState(() => _isLoadingLibrary = true);
    try {
      final backendDio = ref.read(backendClientProvider);
      final service = RadarrApiService(
          backendDio: backendDio, instanceId: defaultRadarr.id);
      final movies = await service.getMovies();
      final queue = await service.getQueue();

      // Track which movies are actively downloading
      final downloadingIds = queue
          .map((r) => r['movieId'] as int?)
          .whereType<int>()
          .toSet();

      final downloaded = movies.where((m) => m.hasFile).toList()
        ..sort((a, b) => (b.added ?? DateTime(0)).compareTo(a.added ?? DateTime(0)));

      // "Downloading Soon" includes both actively downloading and monitored-waiting;
      // actively downloading items are shown first.
      final waitingMovies = movies.where((m) => m.monitored && !m.hasFile).toList();
      final downloading = waitingMovies.where((m) => downloadingIds.contains(m.id)).toList();
      final monitored = waitingMovies.where((m) => !downloadingIds.contains(m.id)).toList();
      final downloadingSoon = [...downloading, ...monitored];

      setState(() {
        _recentlyDownloaded = downloaded.take(10).toList();
        _downloadingSoon = downloadingSoon.take(10).toList();
        _downloadingMovieIds = downloadingIds;
        _isLoadingLibrary = false;
      });
    } catch (_) {
      setState(() => _isLoadingLibrary = false);
    }
  }

  Future<void> _onRefresh() async {
    await Future.wait([
      ref.read(movieDiscoverProvider.notifier).bootstrap(),
      _loadLibraryPreview(),
    ]);
  }

  @override
  Widget build(BuildContext context) {
    final discover = ref.watch(movieDiscoverProvider);

    return RefreshIndicator(
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

          // Radarr library rows (same style as discovery)
          if (_downloadingSoon.isNotEmpty || _isLoadingLibrary)
            _buildRow(
              title: 'Downloading Soon',
              items: _downloadingSoon,
              badgeBuilder: (movie) => _downloadingMovieIds.contains(movie.id)
                  ? (label: 'Downloading', color: AppTheme.downloading)
                  : (label: 'Monitored', color: AppTheme.requested),
            ),
          if (_recentlyDownloaded.isNotEmpty || _isLoadingLibrary)
            _buildRow(
              title: 'Recently Downloaded',
              items: _recentlyDownloaded,
              badgeBuilder: (_) =>
                  (label: 'Downloaded', color: AppTheme.available),
            ),
        ],
      ),
    );
  }

  Widget _buildRow({
    required String title,
    required List<RadarrMovie> items,
    required ({String label, Color color}) Function(RadarrMovie) badgeBuilder,
  }) {
    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Text(
              title,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 20,
                fontWeight: FontWeight.bold,
              ),
            ),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<RadarrMovie>(
            items: items,
            isLoading: _isLoadingLibrary,
            itemBuilder: (movie) {
              final badge = badgeBuilder(movie);
              return MediaCard(
                id: movie.id,
                title: movie.title,
                posterPath: movie.posterUrl,
                statusLabel: badge.label,
                statusColor: badge.color,
                width: 100,
                onTap: movie.tmdbId != null
                    ? () => context.push('/detail/movie/${movie.tmdbId}')
                    : null,
              );
            },
          ),
        ],
      ),
    );
  }
}
