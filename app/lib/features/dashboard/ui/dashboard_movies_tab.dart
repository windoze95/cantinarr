import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/ui/category_row.dart';
import '../../radarr/data/radarr_api_service.dart';
import '../../radarr/data/radarr_models.dart';
import '../../radarr/logic/movie_discover_provider.dart';

/// Dashboard Movies tab: discovery rows + simplified Radarr library sections.
class DashboardMoviesTab extends ConsumerStatefulWidget {
  const DashboardMoviesTab({super.key});

  @override
  ConsumerState<DashboardMoviesTab> createState() =>
      _DashboardMoviesTabState();
}

class _DashboardMoviesTabState extends ConsumerState<DashboardMoviesTab> {
  List<RadarrMovie> _recentlyDownloaded = [];
  List<RadarrMovie> _missing = [];
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

      final downloaded = movies.where((m) => m.hasFile).toList()
        ..sort((a, b) => (b.added ?? DateTime(0)).compareTo(a.added ?? DateTime(0)));
      final missing = movies.where((m) => m.monitored && !m.hasFile).toList();

      setState(() {
        _recentlyDownloaded = downloaded.take(10).toList();
        _missing = missing.take(10).toList();
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

          // Simplified library sections
          if (_recentlyDownloaded.isNotEmpty || _missing.isNotEmpty) ...[
            _SectionHeader(title: 'Your Library'),
            if (_isLoadingLibrary)
              const Padding(
                padding: EdgeInsets.all(24),
                child: Center(
                    child:
                        CircularProgressIndicator(color: AppTheme.accent)),
              ),
            if (_recentlyDownloaded.isNotEmpty)
              _CompactMovieSection(
                title: 'Recently Downloaded',
                movies: _recentlyDownloaded,
                color: AppTheme.available,
              ),
            if (_missing.isNotEmpty)
              _CompactMovieSection(
                title: 'Missing',
                movies: _missing,
                color: AppTheme.requested,
              ),
          ],
        ],
      ),
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
          Expanded(child: Container(height: 1, color: AppTheme.border)),
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
          Expanded(child: Container(height: 1, color: AppTheme.border)),
        ],
      ),
    );
  }
}

class _CompactMovieSection extends StatelessWidget {
  final String title;
  final List<RadarrMovie> movies;
  final Color color;

  const _CompactMovieSection({
    required this.title,
    required this.movies,
    required this.color,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
          child: Row(
            children: [
              Container(
                width: 8,
                height: 8,
                decoration: BoxDecoration(color: color, shape: BoxShape.circle),
              ),
              const SizedBox(width: 8),
              Text(
                '$title (${movies.length})',
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
        ...movies.map((movie) => ListTile(
              dense: true,
              leading: Icon(Icons.movie_outlined,
                  color: color, size: 20),
              title: Text(
                movie.title,
                style: const TextStyle(
                    color: AppTheme.textPrimary, fontSize: 14),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              subtitle: Text(
                movie.year.toString(),
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 12),
              ),
            )),
      ],
    );
  }
}
