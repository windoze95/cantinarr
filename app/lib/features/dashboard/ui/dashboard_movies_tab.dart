import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/featured_media_hero.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/logic/browse_query.dart';
import '../../discover/logic/library_snapshot_provider.dart';
import '../../discover/logic/search_library_status.dart';
import '../../discover/ui/category_row.dart';
import '../../discover/ui/genre_chip_strip.dart';
import '../../radarr/data/radarr_api_service.dart';
import '../../radarr/data/radarr_models.dart';
import '../../radarr/logic/movie_discover_provider.dart';
import '../logic/library_rows.dart';

/// Dashboard Movies tab: discovery rows + Radarr library rows.
class DashboardMoviesTab extends ConsumerStatefulWidget {
  const DashboardMoviesTab({super.key});

  @override
  ConsumerState<DashboardMoviesTab> createState() => _DashboardMoviesTabState();
}

class _DashboardMoviesTabState extends ConsumerState<DashboardMoviesTab>
    with WidgetsBindingObserver {
  List<RadarrMovie> _recentlyDownloaded = [];
  List<RadarrMovie> _downloadingSoon = [];
  Set<int> _downloadingMovieIds = {};
  bool _isLoadingLibrary = false;

  /// The full Radarr library, retained so Discover browse-row posters can be
  /// badged Available/Requested from the same fetch this tab already makes —
  /// no second Radarr call.
  List<RadarrMovie> _libraryMovies = [];

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(movieDiscoverProvider.notifier).bootstrap();
      _loadLibraryPreview();
    });
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // The library may have changed while the app was backgrounded (downloads
    // finishing, an admin working directly in the arr) — otherwise these rows
    // only refresh on pull-to-refresh and this tab is the landing screen.
    if (state == AppLifecycleState.resumed && !_isLoadingLibrary) {
      _loadLibraryPreview();
    }
  }

  Future<void> _loadLibraryPreview() async {
    final auth = ref.read(authProvider).valueOrNull;
    final defaultRadarr = auth?.connection?.defaultRadarrInstance;
    if (defaultRadarr == null) return;

    setState(() => _isLoadingLibrary = true);

    final backendDio = ref.read(backendClientProvider);
    final service =
        RadarrApiService(backendDio: backendDio, instanceId: defaultRadarr.id);

    List<RadarrMovie> movies = [];
    try {
      movies = await service.getMovies();
      if (!mounted) return;

      setState(() {
        _recentlyDownloaded = recentlyDownloadedMovies(movies);
        _libraryMovies = movies;
      });
      // A grid opened from this tab badges its posters from the same list.
      ref.read(librarySnapshotProvider.notifier).seed(movies: movies);
    } catch (_) {
      // Movie fetch failed; leave _recentlyDownloaded empty.
    }

    try {
      final queue = await service.getQueue();
      if (!mounted) return;

      // Track which movies are actively downloading
      final downloadingIds =
          queue.map((r) => r['movieId'] as int?).whereType<int>().toSet();

      // "Downloading Soon" includes both actively downloading and monitored-waiting;
      // actively downloading items are shown first.
      final waitingMovies =
          movies.where((m) => m.monitored && !m.hasFile).toList();
      final downloading =
          waitingMovies.where((m) => downloadingIds.contains(m.id)).toList();
      final monitored =
          waitingMovies.where((m) => !downloadingIds.contains(m.id)).toList();
      final downloadingSoon = [...downloading, ...monitored];

      setState(() {
        _downloadingSoon = downloadingSoon.take(10).toList();
        _downloadingMovieIds = downloadingIds;
      });
    } catch (_) {
      if (!mounted) return;
      // Queue fetch failed; still show monitored-but-missing movies without download status.
      final waitingMovies =
          movies.where((m) => m.monitored && !m.hasFile).toList();
      setState(() {
        _downloadingSoon = waitingMovies.take(10).toList();
        _downloadingMovieIds = {};
      });
    }

    if (mounted) setState(() => _isLoadingLibrary = false);
  }

  Future<void> _onRefresh() async {
    await Future.wait([
      ref.read(movieDiscoverProvider.notifier).bootstrap(),
      _loadLibraryPreview(),
    ]);
  }

  /// Opens a discovery row's feed as a full grid.
  void _seeAll(BrowseFeed feed, String title) => context.push(
        BrowseQuery(type: MediaType.movie, feed: feed, title: title)
            .toLocation(),
      );

  @override
  Widget build(BuildContext context) {
    final discover = ref.watch(movieDiscoverProvider);
    final discoverNotifier = ref.watch(movieDiscoverProvider.notifier);
    // searchResults is genuinely unused here: buildSearchLibraryStatus keys
    // movies straight off the Radarr list and returns early when series is
    // empty, so passing the browse-row items would build a list for nothing.
    // Computed inline rather than cached — build() fires on provider/state
    // change, not per scroll frame (ListView/HorizontalItemRow rebuild their
    // children through their own item builders), following app_shell.dart's
    // precedent for this same computation.
    final libraryStatus = buildSearchLibraryStatus(
      searchResults: const [],
      movies: _libraryMovies,
      series: const [],
    );

    return RefreshIndicator(
      onRefresh: _onRefresh,
      color: AppTheme.accent,
      child: ListView(
        padding: const EdgeInsets.only(bottom: 24),
        children: [
          if (discover.featured.isNotEmpty)
            FeaturedMediaHero(
              item: discover.featured.first,
              eyebrow: 'Movie spotlight',
              onTap: () => context.push(
                '/detail/movie/${discover.featured.first.id}',
              ),
            ),
          // Discovery rows
          CategoryRow(
            title: discover.featuredTitle,
            items: discover.featured.skip(1).toList(growable: false),
            isLoading: discover.isLoadingFeatured,
            isTvRow: false,
            libraryStatus: libraryStatus,
            // The grid continues whichever source answered; until one has,
            // there is nothing to continue.
            onSeeAll: discover.featuredSource.isEmpty
                ? null
                : () => _seeAll(BrowseFeed.featured, discover.featuredTitle),
          ),
          // Every row below grows as it is scrolled toward its end; the
          // headline row above is the one server-capped page.
          if (discover.nowPlaying.isNotEmpty)
            CategoryRow(
              title: 'In Theaters',
              items: discover.nowPlaying,
              isLoading: discover.isLoadingNowPlaying,
              isTvRow: false,
              libraryStatus: libraryStatus,
              onLoadMore: (_) => discoverNotifier.loadMoreNowPlaying(),
              onSeeAll: () => _seeAll(BrowseFeed.nowPlaying, 'In Theaters'),
            ),
          if (discover.topRated.isNotEmpty)
            CategoryRow(
              title: 'Top Rated',
              items: discover.topRated,
              isLoading: discover.isLoadingTopRated,
              isTvRow: false,
              libraryStatus: libraryStatus,
              onLoadMore: (_) => discoverNotifier.loadMoreTopRated(),
              onSeeAll: () => _seeAll(BrowseFeed.topRated, 'Top Rated'),
            ),
          if (discover.upcoming.isNotEmpty)
            CategoryRow(
              title: 'Coming Soon',
              items: discover.upcoming,
              isLoading: discover.isLoadingUpcoming,
              isTvRow: false,
              libraryStatus: libraryStatus,
              onLoadMore: (_) => discoverNotifier.loadMoreUpcoming(),
              onSeeAll: () => _seeAll(BrowseFeed.upcoming, 'Coming Soon'),
            ),
          if (discover.anticipated.isNotEmpty)
            CategoryRow(
              title: 'Most Anticipated',
              items: discover.anticipated,
              isLoading: discover.isLoadingAnticipated,
              isTvRow: false,
              libraryStatus: libraryStatus,
              onLoadMore: (_) => discoverNotifier.loadMoreAnticipated(),
              onSeeAll: () =>
                  _seeAll(BrowseFeed.anticipated, 'Most Anticipated'),
            ),
          GenreChipStrip(genres: discover.genres, mediaType: MediaType.movie),

          // Radarr library rows (same style as discovery)
          if (_downloadingSoon.isNotEmpty || _isLoadingLibrary)
            _buildRow(
              title: 'Downloading Soon',
              items: _downloadingSoon,
              badgeBuilder: (movie) => _downloadingMovieIds.contains(movie.id)
                  ? (label: 'Downloading', color: AppTheme.downloading)
                  : (label: 'Requested', color: AppTheme.requested),
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
    final viewportWidth = MediaQuery.sizeOf(context).width;
    final cardWidth =
        viewportWidth >= 900 ? 124.0 : (viewportWidth >= 600 ? 116.0 : 108.0);

    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: EdgeInsets.symmetric(
              horizontal: MediaQuery.sizeOf(context).width >= 900 ? 24 : 16,
            ),
            child: SectionHeader(title: title),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<RadarrMovie>(
            items: items,
            isLoading: _isLoadingLibrary,
            height: cardWidth * 1.5 + MediaCard.plainRowExtraHeight,
            itemBuilder: (movie) {
              final badge = badgeBuilder(movie);
              return MediaCard(
                id: movie.id,
                title: movie.title,
                posterPath: movie.posterUrl,
                statusLabel: badge.label,
                statusColor: badge.color,
                width: cardWidth,
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
