import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/widgets/featured_media_hero.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/logic/browse_query.dart';
import '../../discover/logic/cover_4k_badges_provider.dart';
import '../../discover/logic/library_snapshot_provider.dart';
import '../../discover/logic/search_library_status.dart';
import '../../discover/ui/category_row.dart';
import '../../discover/ui/discover_refresh.dart';
import '../../discover/logic/discover_session.dart';
import '../../../core/widgets/error_banner.dart';
import '../../discover/ui/genre_chip_strip.dart';
import '../../radarr/data/radarr_models.dart';
import '../../radarr/logic/movie_discover_provider.dart';

/// Dashboard Movies tab: discovery rows + Radarr library rows.
class DashboardMoviesTab extends ConsumerStatefulWidget {
  const DashboardMoviesTab({super.key});

  @override
  ConsumerState<DashboardMoviesTab> createState() => _DashboardMoviesTabState();
}

class _DashboardMoviesTabState extends ConsumerState<DashboardMoviesTab> {
  LibrarySnapshot get _library => ref.read(librarySnapshotProvider);
  bool get _isLoadingLibrary => _library.moviesLoading;
  List<RadarrMovie> get _recentlyDownloaded => _library.recentMovies;
  List<RadarrMovie> get _downloadingSoon => _library.downloadingMovies;
  Set<int> get _downloadingMovieIds => _library.downloadingMovieIds;
  List<RadarrMovie> get _libraryMovies => _library.movies;

  Future<void> _refresh() async {
    await Future.wait([
      ref.read(movieDiscoverProvider.notifier).bootstrap(),
      ref.read(librarySnapshotProvider.notifier)
          .refresh(force: true, type: MediaType.movie),
    ]);
  }

  Future<void> _onRefresh() async {
    ref.read(libraryRefreshTickProvider.notifier).state++;
    await _refresh();
  }

  /// Opens a discovery row's feed as a full grid.
  void _seeAll(BrowseFeed feed, String title) => context.push(
        BrowseQuery(type: MediaType.movie, feed: feed, title: title)
            .toLocation(),
      );

  @override
  Widget build(BuildContext context) {
    final library = ref.watch(librarySnapshotProvider);
    final scope = ref.watch(discoverSessionProvider);
    final discover = ref.watch(movieDiscoverProvider);
    final discoverNotifier = ref.watch(movieDiscoverProvider.notifier);
    final show4K = ref.watch(cover4KBadgesProvider);
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
      show4K: show4K,
    );

    return PageStorage(
      bucket: ref.watch(discoverPageStorageProvider),
      child: DiscoverRefresh(
        key: ValueKey(scope),
        path: '/dashboard/movies',
        onRefresh: _refresh,
        child: RefreshIndicator(
          onRefresh: _onRefresh,
          color: AppTheme.accent,
          child: Stack(children: [
            ListView(
            key: PageStorageKey(('discover-movie', scope)),
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
                paginationRevision: discover.refreshRevision,
                key: const PageStorageKey('featured'),
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
                  paginationRevision: discover.refreshRevision,
                  key: const PageStorageKey('In Theaters'),
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
                  paginationRevision: discover.refreshRevision,
                  key: const PageStorageKey('Top Rated'),
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
                  paginationRevision: discover.refreshRevision,
                  key: const PageStorageKey('Coming Soon'),
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
                  paginationRevision: discover.refreshRevision,
                  key: const PageStorageKey('Most Anticipated'),
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
                  mark4K: show4K,
                ),
            ],
          ),
            if (discover.failedRows.isNotEmpty || library.moviesFailed)
              Align(
                alignment: Alignment.bottomCenter,
                child: SafeArea(child: ErrorBanner(
                  message: 'Some titles could not be loaded. Retry to check for updates.',
                  onRetry: _refresh,
                )),
              ),
          ]),
        ),
      ),
    );
  }

  /// [mark4K] tags movies whose file measures 4K. Only the downloaded row
  /// sets it: beside Downloading or Requested it would describe a file the
  /// badge says is not the one arriving.
  Widget _buildRow({
    required String title,
    required List<RadarrMovie> items,
    required ({String label, Color color}) Function(RadarrMovie) badgeBuilder,
    bool mark4K = false,
  }) {
    final viewportWidth = MediaQuery.sizeOf(context).width;
    final cardWidth =
        viewportWidth >= 900 ? 124.0 : (viewportWidth >= 600 ? 116.0 : 108.0);

    return Padding(
      key: PageStorageKey(title),
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
            itemKey: (item) => item.id,
            itemExtent: cardWidth + 14,
            isLoading: _isLoadingLibrary,
            height: cardWidth * 1.5 + MediaCard.rowExtraHeight(context, withSubtitle: false),
            itemBuilder: (movie) {
              final badge = badgeBuilder(movie);
              return MediaCard(
                id: movie.id,
                title: movie.title,
                posterPath: movie.posterUrl,
                statusLabel: badge.label,
                statusColor: badge.color,
                is4K: mark4K && (movie.movieFile?.measures4K ?? false),
                width: cardWidth,
                onTap: (movie.tmdbId ?? 0) > 0
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
